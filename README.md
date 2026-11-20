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

The agent intentionally refuses to start without `PROXY_MANAGER_AGENT_TOKEN`. It should run only on the gateway host under systemd.

See `docs/ARCHITECTURE.md` for security boundaries and the implementation roadmap.
