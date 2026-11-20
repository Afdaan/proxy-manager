# Architecture

Proxy Manager controls NGINX. NGINX still handles traffic. Certbot handles ACME, ModSecurity runs the WAF, and GeoIP2 supplies location data.

```text
Browser -> server -> authenticated Unix socket -> agent -> NGINX and Certbot
                  PostgreSQL               versioned config
```

## Server

The server owns control-plane state:

- Login, TOTP, sessions, and CSRF checks.
- Platform roles and application memberships.
- Proxy hosts, upstreams, certificates, WAF, rate-limit, GeoIP, and IP policies.
- PostgreSQL transactions, optimistic version checks, audit records, and deployment jobs.
- Desired-state generation from typed records.
- Traffic and security analytics APIs.
- Agent calls through a local Unix socket.

The server runs without root access. It cannot write NGINX configuration or signal NGINX directly.

## Agent

The agent stays small because it runs with elevated permissions:

- Validates every desired-state document again.
- Renders an allow-listed set of NGINX directives.
- Rejects stale deployment generations.
- Writes immutable revision directories.
- Switches the active config using an atomic symlink.
- Runs `nginx -t` before reload.
- Restores the last working revision after validation or reload failure.
- Issues and renews certificates through Certbot without shell interpolation.
- Manages fixed WAF and GeoIP file locations.

The local API uses socket permissions and a bearer token. It never accepts executable paths, shell commands, arbitrary file paths, or raw NGINX configuration.

## Feature map

The scope borrows useful policy concepts from BunkerWeb without copying its platform complexity.

| Capability | Implementation |
| --- | --- |
| Reverse proxy | NGINX upstreams generated from typed records |
| Load balancing | Round robin, least connections, or IP hash |
| Certificates | Certbot webroot and renewal hook |
| WAF | ModSecurity v3 and OWASP CRS; off, detect, or block |
| Rate limit | Dedicated NGINX request zone per proxy host |
| GeoIP | Country allow or block policy using GeoIP2 |
| IP controls | CIDR allow and deny rules |
| Security headers | Safe preset; CSP remains application-owned |
| RBAC | Platform roles and per-application memberships |
| Recovery | Generation ordering, validation, atomic apply, and rollback |

References:

- https://docs.bunkerweb.io/latest/features/
- https://docs.bunkerweb.io/latest/concepts/
- https://nginx.org/en/docs/http/ngx_http_limit_req_module.html
- https://github.com/owasp-modsecurity/ModSecurity-nginx
- https://coreruleset.org/docs/

## Delivery order

1. Authentication, RBAC, ownership, and audit trail.
2. Proxy host CRUD, desired-state generation, apply, and rollback.
3. Automatic certificates and renewal status.
4. Rate-limit, IP-access, and header policies.
5. GeoIP analytics and country policies.
6. ModSecurity and CRS with detection-first rollout.
7. Optional active/standby gateway synchronization.

TCP streams, Kubernetes ingress, plugins, custom Lua, raw NGINX snippets, multi-region control planes, and volumetric DDoS mitigation are outside the initial scope.
