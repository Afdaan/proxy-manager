# Proxy Manager

Independent web control plane for NGINX reverse proxies, automatic certificates, rate limiting, WAF policies, GeoIP controls, and application-scoped RBAC.

## Repository

```text
apps/web/          React, Vite, TanStack Router and Query UI
cmd/server/        Unprivileged HTTP API and web server
cmd/agent/         Privileged local NGINX and certificate agent
internal/model/    Declarative gateway configuration contract
internal/nginx/    Validated NGINX configuration renderer
internal/agent/    Atomic apply, reload and rollback implementation
db/migrations/     PostgreSQL schema
deploy/systemd/    Service definitions
docs/              Architecture and feature scope
```

## Development

Requirements: Go 1.27+, Node.js 24+, and pnpm.

```bash
pnpm install
pnpm dev:web
pnpm dev:server
```

Run checks:

```bash
pnpm test
pnpm lint
pnpm build
```

Production builds are written to `bin/proxy-manager-server`,
`bin/proxy-manager-agent`, and `apps/web/dist`.

## Expose the control plane

Keep the Go server on `127.0.0.1:8080`. Copy
`deploy/nginx/proxy-manager-ui.conf.example` into the NGINX HTTP configuration,
replace `proxy.example.com`, validate with `nginx -t`, then reload NGINX.

Set `PROXY_MANAGER_COOKIE_SECURE=true` after HTTPS is active. The generated
gateway routes also require this line inside NGINX's `http` block:

```nginx
include /etc/nginx/proxy-manager/active.conf;
```

## Docker

The safe Docker layout keeps the privileged agent on the NGINX host. Only the
web server and PostgreSQL run in Compose:

```bash
cp .env.docker.example .env
docker compose up -d --build
```

The server publishes only `127.0.0.1:8080`; expose it through the host NGINX.
The agent Unix socket is bind-mounted into the server container. Set
`PROXY_MANAGER_AGENT_GID` to the numeric ID of the host
`proxy-manager-agent` group.

Do not put the agent in a privileged container and do not mount the Docker
daemon socket into it. The agent intentionally remains a small systemd service
with access limited to NGINX, Certbot, and its own state directories.

The agent intentionally refuses to start without `PROXY_MANAGER_AGENT_TOKEN`. It should run only on the gateway host under systemd.

## Apply proxy hosts

Saving a Proxy Host writes the desired state and a durable reconciliation job
in one PostgreSQL transaction. The server immediately validates and applies the
new complete gateway configuration. Concurrent saves are serialized by
PostgreSQL locks, stale revisions cannot replace newer revisions, and temporary
agent failures retry automatically. NGINX keeps serving the last valid revision
until a newer revision passes validation and reloads successfully.

The Overview page shows configuration history and exposes a retry action only
when a revision fails permanently. A normal save requires no separate deploy
step.

Each successful deployment creates an immutable revision:

```text
/var/lib/proxy-manager/revisions/
└── 2026-11-21_03-30-42Z--rev-42--a81c7f/
    ├── nginx.conf
    ├── manifest.json
    ├── globals/
    │   ├── 00-runtime.conf
    │   ├── 10-rate-limit-zones.conf
    │   └── 20-geoip-maps.conf
    └── hosts/
        └── dashboard.example.com--bb819a.conf
```

`/etc/nginx/proxy-manager/active.conf` remains a symlink to the active
revision's `nginx.conf`. The agent keeps the ten newest revisions and preserves
the previous revision for rollback.

See `docs/ARCHITECTURE.md` for security boundaries and the implementation roadmap.
