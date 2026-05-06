# install-proxy: a small Go service that replaces the per-tool Cloudflare Workers

## Context

Today, every tool we want to install via a one-liner (`curl -fsSL https://<host>/install | sh`) gets its own Cloudflare Worker. The Workers do almost nothing — they fetch `install.sh` from the latest GitHub Release of a given repo, return it with `text/x-shellscript` content type for curl/wget, and serve a small HTML landing page for browsers. That's it.

Cloudflare bills per Worker. As we add more tools (deployer, cpu_bench_go, future things), the cost grows linearly even though the logic is identical. We want a single self-hosted Go service that handles all of them, deployed on a VPS, fronted by the same Traefik instance the rest of our tools already use. Adding a new tool should be a 3-line YAML edit, not a new Cloudflare project.

The service should be deployable through `deployer` itself — that's a useful end-to-end test of the whole pipeline (one repo, one domain, deploy command, live behind Zero Trust if we want).

## Goals

- One Go binary serves install-script endpoints for many tools, routed by `Host` header.
- Behavior matches the existing Worker exactly: HTML landing for browsers, raw shell script for curl/wget, sensible 502/503 messages when GitHub is unreachable or the release has no `install.sh`.
- Config is a single YAML file mapping host → GitHub repo. Reload by restart.
- Static binary, no CGO, fits the deployer/Traefik/Zero Trust pattern we use everywhere else.
- Lives in its own repo (`hra42/install-proxy`) so it's reusable beyond this project.

## Non-goals

- Hot config reload. Restart-on-change is fine; deploys are cheap.
- Authentication. The /install endpoint is public by design.
- Multi-source fetching (GitLab, S3, etc.). GitHub Releases only — extend later if needed.
- Metrics/observability beyond stdout logging. Add later when there's a reason.
- Caching layer. Traefik (or Cloudflare DNS in front of it) handles edge caching; the upstream `Cache-Control: max-age=300` header is enough.

## Architecture

Single binary, two files of meaningful logic:

```
install-proxy/
├── main.go              # flag parsing, config load, http.Server boot
├── proxy.go             # the handler — host lookup, browser detection, upstream fetch
├── config.go            # YAML schema, Load()
├── config.example.yml
├── Dockerfile
├── docker-compose.yml   # Traefik labels, joins external network — same shape deployer expects
├── README.md
└── go.mod
```

Built on **Fiber v3** (`github.com/gofiber/fiber/v3`) for the HTTP layer, **Cobra** (`github.com/spf13/cobra`) for the CLI/flag layer, and `gopkg.in/yaml.v3` for config file parsing (already used by `deployer`, so we know it works). Fiber gives us batteries-included middleware (logger, recover, request ID) and ergonomic routing as we grow into more endpoints (`/healthz`, per-tool dashboards, etc.). Cobra gives us a clean root command + room to grow subcommands (`serve`, `validate`, `version`) without re-architecting `main.go`. Note: Fiber sits on top of `fasthttp`, so the upstream GitHub fetch uses a `net/http` client paired with `c.SendStream(...)` to pipe the body back — see the proxy.go task for specifics.

### Request flow

1. Request lands on `:8080` (Traefik terminates TLS upstream).
2. Fiber handler looks up `c.Hostname()` in the loaded config. Unknown host → 404.
3. If path is not `/install` → 404 with a "see <releases-page>" hint. (Routed via `app.Get("/install", …)` and a catch-all `app.Use(notFound)`.)
4. Browser detection: `Accept: text/html` AND user-agent does not match `curl|wget|fetch` → return inline HTML landing page (title and curl example templated from config). Cache 5 min.
5. Otherwise: fetch `https://github.com/<repo>/releases/latest/download/install.sh` with a 10s timeout, follow redirects.
   - 404 upstream → 503 "no install.sh in latest release of <repo> yet".
   - non-2xx upstream → 502 "upstream returned <status>".
   - network error → 502 "upstream fetch failed: <err>".
   - 2xx → stream body through via `c.SendStream(resp.Body)` with `Content-Type: text/x-shellscript`, `Cache-Control: public, max-age=300`, `X-Source: <upstream-url>`.

This is a near-line-for-line port of the existing Worker. No new behavior on day one.

### Config shape

```yaml
# config.yml
listen: ":8080"   # optional, default :8080

tools:
  - host: deployer.hra42.lol
    repo: hra42/deployer
    display_name: deployer
  - host: cpu-bench.hra42.lol
    repo: hra42/cpu_bench_go
    display_name: cpu_bench_go
```

`display_name` only feeds the HTML landing page. Everything else derives from `host` + `repo`.

## Tasks

### 1. Bootstrap the repo

Create `hra42/install-proxy` with:
- `go mod init github.com/hra42/install-proxy`
- `go.mod` requiring `github.com/gofiber/fiber/v3`, `github.com/spf13/cobra`, and `gopkg.in/yaml.v3`
- `.gitignore` (binary, `dist/`)
- Empty `main.go`, `proxy.go`, `config.go`

### 2. Implement `config.go`

- `type Config struct { Listen string; Tools []Tool }`
- `type Tool struct { Host, Repo, DisplayName string }`
- `Load(path string) (*Config, error)` — read, YAML unmarshal, validate (every tool has host + repo, no duplicate hosts).
- Default `Listen` to `:8080` when empty.
- Build a `map[string]Tool` keyed by lowercase host for O(1) lookup; expose via `(*Config) Lookup(host string) (Tool, bool)`.

### 3. Implement `proxy.go`

- `Register(app *fiber.App, cfg *Config)` wires routes onto a Fiber app: `app.Get("/install", handleInstall(cfg))` plus a catch-all 404.
- Constants for the HTML template (mirror the existing Worker's HTML, parameterized by `display_name` and `host`).
- `isBrowser(c fiber.Ctx) bool` — `Accept` contains `text/html` AND UA doesn't match `(?i)curl|wget|fetch`.
- Reusable `*http.Client` with `Timeout: 10s` for the upstream GitHub fetch (we keep `net/http` for the *outbound* call to avoid pulling in fasthttp client quirks; Fiber only handles the *inbound* request).
- Helper `plain(c, status, body)` for text/plain error responses with `Cache-Control: public, max-age=60`.
- Stream the upstream body via `c.SendStream(resp.Body)` — don't buffer.

### 4. Implement `main.go`

- Cobra root command `install-proxy` with a persistent `--config` flag (default `/etc/install-proxy/config.yml`).
- Default action (root `RunE`) is "serve" — load config, build a Fiber app, register routes via `proxy.Register`, and listen on `cfg.Listen`.
- Configure Fiber with sensible timeouts via `fiber.Config{ReadTimeout: 10s, WriteTimeout: 30s, IdleTimeout: 60s}`. (Fiber maps these to fasthttp's read/write/idle timeouts; no separate `ReadHeaderTimeout`.)
- Add `app.Use(logger.New())` for one-line-per-request stdout logs (`method host path status duration` shape) — Fiber's built-in logger middleware covers this. Add `recover.New()` so panics don't kill the server.
- Graceful shutdown on SIGINT/SIGTERM via `app.ShutdownWithTimeout(5 * time.Second)`.
- Leave room for future subcommands (e.g. `install-proxy validate --config …`, `install-proxy version`) by structuring `main.go` so the root command's `RunE` is the only one wired now, but the cmd tree is easy to extend.

### 5. Containerize

- Multi-stage `Dockerfile`: `golang:1.26-alpine` builder → `gcr.io/distroless/static-debian12` runtime. `CGO_ENABLED=0`, `-trimpath`, `-ldflags=-s -w`. Final image well under 20 MB.
- `docker-compose.yml` joining the external Traefik network, with the standard labels `deployer` expects (`traefik.enable=true`, host rule, `entrypoints=websecure`, cert resolver). Mount `./config.yml` at `/etc/install-proxy/config.yml`.
- The compose file should match the template in `deployer`'s README so dogfooding works without edits.

### 6. README

Cover:
- What it does (one paragraph).
- Quick start: clone, edit `config.yml`, `docker compose up -d`.
- Adding a tool: append a YAML entry, point Cloudflare DNS / Traefik host rule, restart.
- Required GitHub Release shape: must publish `install.sh` as a release asset (the deployer release workflow already does this).
- Behavior reference: HTML for browsers, shell for curl, error responses.
- Non-goals (mirror this section).

### 7. Dogfood-deploy via deployer

Once the repo is live and a `v0.1.0` tag is cut:
1. Run `deployer deploy --repo hra42/install-proxy --domain installs.hra42.lol` against the target VPS.
2. Confirm install-proxy comes up behind Traefik with TLS.
3. Point `deployer.hra42.lol` and `cpu-bench.hra42.lol` DNS at the same VPS (same Traefik instance picks them up via the host rule on the install-proxy container).
4. Curl-test each: `curl -fsSL https://deployer.hra42.lol/install | head` should return the deployer install script. Same for cpu-bench.
5. Decommission the corresponding Cloudflare Workers.

## Critical files

- New repo: `hra42/install-proxy` (everything above lives there).
- This deployer repo is unaffected; the existing `scripts/install.sh` and release workflow keep working unchanged. The proxy is a transparent passthrough.

## Verification

- `go build ./...` and `go vet ./...` pass.
- Local smoke test: `go run . --config config.example.yml`, hit `localhost:8080` with `Host: deployer.hra42.lol`:
  - `curl -H 'Host: deployer.hra42.lol' http://localhost:8080/install` returns the live deployer install script with `text/x-shellscript`.
  - `curl -H 'Host: deployer.hra42.lol' -H 'Accept: text/html' -H 'User-Agent: Mozilla/5.0' http://localhost:8080/install` returns the HTML landing page.
  - `curl -H 'Host: unknown.example.com' http://localhost:8080/install` returns 404.
  - `curl -H 'Host: deployer.hra42.lol' http://localhost:8080/foo` returns 404.
- Container: `docker build .` produces an image under 20 MB; `docker run` with a mounted config serves the same responses.
- End-to-end: deploy via `deployer`, confirm both `deployer.hra42.lol/install` and `cpu-bench.hra42.lol/install` work, then delete the Workers.

## Done when

- `hra42/install-proxy` repo exists, has a tagged release, and runs cleanly in Docker behind Traefik.
- A single VPS with one install-proxy container serves install scripts for both deployer and cpu_bench_go.
- The Cloudflare Workers for both tools are deleted.
- Adding a third tool is verifiably a 3-line config change + restart.
