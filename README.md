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

The agent intentionally refuses to start without `PROXY_MANAGER_AGENT_TOKEN`. It should run only on the gateway host under systemd.

See `docs/ARCHITECTURE.md` for security boundaries and the implementation roadmap.
