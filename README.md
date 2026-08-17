# install-proxy

A single Go binary that serves install-script endpoints for many tools, routed by the `Host` header. It replaces a fleet of per-tool Cloudflare Workers: for each configured host, `GET /install` returns either a small HTML landing page (for browsers) or the raw `install.sh` from that tool's latest GitHub Release (for `curl`/`wget`). Fronted by Traefik, deployed as a single container — either with plain `docker compose` on a VPS or [on Dokploy](#deploying-on-dokploy).

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
  - host: ai.hra42.lol
    repo: hra42/ai
    display_name: ai
```

Config can come from a file or straight from the environment. Resolution order:

1. `INSTALL_PROXY_CONFIG` — inline YAML, the whole config as a string. Wins over everything.
2. `--config` / `-c` — when explicitly passed.
3. `CONFIG_PATH` — path to a config file.
4. `--config`'s default, `/etc/install-proxy/config.yml`.

An explicit `--config` beats `CONFIG_PATH`, so a stray exported `CONFIG_PATH` can't silently hijack a local run. On startup the service logs which source it used.

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
3. Restart: `docker compose restart install-proxy`. On Dokploy, edit `INSTALL_PROXY_CONFIG` and redeploy instead.

That's the whole flow.

## Coexisting with other services

The default `docker-compose.yml` uses `HostRegexp(`^.+$`)` with `priority=1`, so install-proxy acts as a catch-all for every hostname Traefik sees and only loses to routers with a specific `Host(...)` rule. That is fine when install-proxy is the only thing on the host.

If you run other services on the same host (especially under a different domain), restrict install-proxy to the domain you actually use — otherwise unknown hostnames in *other* domains will hit install-proxy and return its `404 not found` instead of Traefik's default 404. Do this with a `docker-compose.override.yml` next to the repo file:

```yaml
services:
  install-proxy:
    labels: !override
      - "traefik.enable=true"
      - "traefik.http.routers.install-proxy.rule=HostRegexp(`^.+\\.example\\.com$$`)"
      - "traefik.http.routers.install-proxy.priority=1"
      - "traefik.http.routers.install-proxy.entrypoints=websecure"
      - "traefik.http.routers.install-proxy.tls=true"
      - "traefik.http.services.install-proxy.loadbalancer.server.port=8080"
```

The `$$` is Compose's escape for a literal `$`. The `!override` tag replaces the labels list completely instead of merging it with the one from the repo file.

Note this recipe keeps `tls=true` but drops `certresolver`, so it relies on a Traefik-level default resolver. See [Certificates](#certificates) — a `HostRegexp` router never requests a certificate on its own.

## Deploying on Dokploy

Dokploy deploys by cloning this repo fresh on every deploy, so the `./config.yml` bind mount from `docker-compose.yml` won't exist there. Use `docker-compose.dokploy.yml` instead, which takes its config from the environment and joins `dokploy-network` rather than `traefik`.

1. **Create the service.** Projects → Create → Compose. Point it at this repo, pick the branch, and set **Compose Path** to `docker-compose.dokploy.yml`.
2. **Set the config.** In the Environment tab, add `INSTALL_PROXY_CONFIG`. Multi-line YAML works, but Compose's `.env` parser only accepts it when the value is single-quoted:
   ```
   INSTALL_PROXY_CONFIG='tools:
     - host: deployer.hra42.lol
       repo: hra42/deployer
       display_name: deployer
   '
   ```
   YAML flow style is a single-line equivalent if the quoting is awkward:
   ```
   INSTALL_PROXY_CONFIG={tools: [{host: deployer.hra42.lol, repo: hra42/deployer, display_name: deployer}]}
   ```
3. **Point DNS at the server** for each tool host, **proxied** (orange cloud) in Cloudflare.
4. **Set Cloudflare SSL/TLS mode to `Full`.** Deploy.

Startup logs should read `listening on :8080, N tools configured from $INSTALL_PROXY_CONFIG`. If they instead show `read config /etc/install-proxy/config.yml: no such file`, the variable never reached the container — check the name and that `env_file: - .env` is present.

### Certificates

Traefik derives ACME domains from `Host()`/`HostSNI()` matchers or an explicit `tls.domains`. The catch-all `HostRegexp` router gives it neither, so **it never requests a certificate** and serves Traefik's self-signed default instead. The symptom is a TLS trust error, not a 404.

Cloudflare in front is what makes the catch-all workable: it terminates TLS publicly, and `Full` mode accepts the self-signed origin cert. Use `Full (strict)` only after installing a Cloudflare Origin Certificate as Traefik's default certificate. The alternatives, if you'd rather not use Cloudflare, are a wildcard cert via DNS-01 or one `Host()` router per tool.

### Adding a tool on Dokploy

Append the entry to `INSTALL_PROXY_CONFIG`, add the proxied DNS record, and redeploy. No compose edit, no new router, no cert work.

### Config via File Mount instead

For a config too large to sit comfortably in an env var: Advanced → Volumes → File Mount, name it `config.yml`, then add to the service

```yaml
    volumes:
      - ../files/config.yml:/etc/install-proxy/config.yml:ro
```

The `../` is required because Dokploy runs Compose from the `code` directory while mounts live in a sibling `files` directory. No `CONFIG_PATH` is needed since that is already the default path.

### Escaping `$`

Compose interpolates `$` in compose files, so a literal `$` in a Traefik rule must be written `$$` — as in `HostRegexp(`^.+$$`)`. An unescaped `$` makes Compose treat `$)` as an invalid interpolation and reject the file. Same rule as the override recipe above.

## Required GitHub Release shape

The proxy fetches `https://github.com/<repo>/releases/latest/download/install.sh`. For this to work, the repo must publish `install.sh` as an asset on its latest release. (The `deployer` release workflow already does this — copy that pattern.)

## Behavior reference

| Request | Response |
|---|---|
| `/healthz`, any host | `200 ok` (text/plain), no host lookup |
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

# Health check -> 200 regardless of Host
curl -i http://localhost:8080/healthz
```

Config straight from the environment, no file needed:

```sh
INSTALL_PROXY_CONFIG='{tools: [{host: deployer.hra42.lol, repo: hra42/deployer}]}' go run . validate
```

The `healthcheck` subcommand probes a running instance and exits non-zero on failure — this is what the container `HEALTHCHECK` runs, since the distroless image has no shell or curl:

```sh
go run . healthcheck
go run . healthcheck --addr 127.0.0.1:9090
```

`go build ./...` and `go vet ./...` should pass.

## Non-goals

- **Hot config reload.** Restart on change; deploys are cheap.
- **Authentication.** `/install` is public by design.
- **Multi-source fetching** (GitLab, S3, etc.). GitHub Releases only.
- **Built-in metrics, observability, or a caching layer.** Stdout request logs only; edge caching is Traefik/Cloudflare's job, and the upstream `Cache-Control: max-age=300` header is enough.
