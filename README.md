# install-proxy

A single Go binary that serves install-script endpoints for many tools, routed by the `Host` header. It replaces a fleet of per-tool Cloudflare Workers: for each configured host, `GET /install` returns either a small HTML landing page (for browsers) or the raw `install.sh` from that tool's latest GitHub Release (for `curl`/`wget`). Fronted by Traefik, deployed as a single container.

## Quick start

```sh
git clone https://github.com/hra42/install-proxy.git
cd install-proxy
cp config.example.yml config.yml
# edit config.yml — add your tools
docker compose up -d
```

Prerequisites (see the comment block at the top of `docker-compose.yml`):

- An external Docker network named `traefik` that your Traefik instance is attached to.
- A Traefik cert resolver named `letsencrypt`. Edit the compose file if yours is named differently.

## Configuration

`config.yml` (modeled on `config.example.yml`):

```yaml
listen: ":8080"   # optional, default :8080

tools:
  - host: deployer.hra42.lol
    repo: hra42/deployer
    display_name: deployer
  - host: cpu-bench.hra42.lol
    repo: hra42/cpu_bench_go
    display_name: cpu_bench_go
```

Fields:

- `listen` — bind address. Optional; defaults to `:8080`.
- `tools[].host` — the public hostname clients hit. Lookups are case-insensitive (the host is lowercased internally).
- `tools[].repo` — GitHub `owner/name`. The proxy fetches `https://github.com/<repo>/releases/latest/download/install.sh`.
- `tools[].display_name` — shown on the HTML landing page only. Falls back to `repo` when omitted.

Validation (enforced at startup): every tool entry must have both `host` and `repo`, and hosts must be unique. The service exits with an error otherwise.

## Adding a tool

1. Append a three-line entry to `config.yml`:
   ```yaml
     - host: newtool.example.com
       repo: hra42/newtool
       display_name: newtool
   ```
2. Point `newtool.example.com` DNS at the VPS running install-proxy. The catch-all Traefik `HostRegexp` router on the install-proxy container will pick it up — no new router, no new container, no new Worker.
3. Restart: `docker compose restart install-proxy`.

That's the whole flow.

## Required GitHub Release shape

The proxy fetches `https://github.com/<repo>/releases/latest/download/install.sh`. For this to work, the repo must publish `install.sh` as an asset on its latest release. (The `deployer` release workflow already does this — copy that pattern.)

## Behavior reference

| Request | Response |
|---|---|
| Unknown host, any path | `404 not found` (text/plain) |
| Known host, path ≠ `/install` | `404` with a "see https://github.com/<repo>/releases" hint |
| `/install`, browser (Accept contains `text/html`, UA not `curl`/`wget`/`fetch`) | HTML landing page, `Cache-Control: public, max-age=300` |
| `/install`, curl/wget/fetch | Streamed `install.sh`; `Content-Type: text/x-shellscript; charset=utf-8`; `Cache-Control: public, max-age=300`; `X-Source: <upstream-url>` |
| Upstream returns 404 | `503` "no install script in the latest release of <repo> yet" |
| Upstream returns other non-2xx | `502 upstream returned <status>` |
| Upstream network error or 10s timeout | `502 upstream fetch failed: <err>` |

All error responses are `text/plain` with `Cache-Control: public, max-age=60`.

## Local development

```sh
go run . --config config.example.yml
```

Smoke tests against `localhost:8080` using `Host:` overrides:

```sh
# Shell script (curl default UA)
curl -H 'Host: deployer.hra42.lol' http://localhost:8080/install

# HTML landing page
curl -H 'Host: deployer.hra42.lol' \
     -H 'Accept: text/html' \
     -H 'User-Agent: Mozilla/5.0' \
     http://localhost:8080/install

# Unknown host -> 404
curl -i -H 'Host: unknown.example.com' http://localhost:8080/install

# Wrong path on a known host -> 404 with releases hint
curl -i -H 'Host: deployer.hra42.lol' http://localhost:8080/foo
```

`go build ./...` and `go vet ./...` should pass.

## Non-goals

- **Hot config reload.** Restart on change; deploys are cheap.
- **Authentication.** `/install` is public by design.
- **Multi-source fetching** (GitLab, S3, etc.). GitHub Releases only.
- **Built-in metrics, observability, or a caching layer.** Stdout request logs only; edge caching is Traefik/Cloudflare's job, and the upstream `Cache-Control: max-age=300` header is enough.
