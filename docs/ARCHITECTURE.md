# Architecture

Proxy Manager controls NGINX. NGINX still handles traffic. Certbot handles ACME, ModSecurity runs the WAF, and GeoIP2 supplies location data.

```text
Browser -> server -> authenticated Unix socket -> agent -> NGINX and Certbot
                  PostgreSQL               versioned config
```

## Server

The server owns control-plane state:

- Password login, server-side sessions, secure cookies, and CSRF checks.
- Platform administration, application memberships, user disablement, and password reset.
- Proxy hosts, upstreams, certificates, WAF, rate-limit, GeoIP, and IP policies.
- PostgreSQL transactions, optimistic version checks, audit records, and deployment jobs.
- Desired-state generation from typed records.
- Automatic reconciliation with durable retries and PostgreSQL-backed ownership.
- Traffic and security analytics APIs.
- Agent calls through a local Unix socket.

The server runs without root access. It cannot write NGINX configuration or signal NGINX directly.

Saving a gateway resource and queueing its revision happen in one transaction.
One reconciler owns NGINX activation at a time through a PostgreSQL advisory
lock, so concurrent users and multiple server instances cannot apply revisions
out of order. Temporary agent failures retry while permanent validation errors
remain visible for operator correction.

Proxy Host create, update, disable, and delete operations all use this mutation
path. Updates use optimistic record versions; deletes remove the desired route
before generating the next full revision. The active NGINX revision remains
unchanged until the replacement validates and reloads successfully.

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
| Certificates | Two-phase Certbot webroot issuance, HTTPS activation, and renewal status |
| WAF | ModSecurity v3 and OWASP CRS; off, detect, or block |
| Rate limit | Dedicated NGINX request zone per proxy host |
| GeoIP | Country allow or block policy using GeoIP2 |
| IP controls | CIDR allow and deny rules |
| Security headers | Safe preset; CSP remains application-owned |
| RBAC | Platform roles and per-application memberships |
| User administration | Provision, disable, role assignment, password reset, and session revocation |
| Audit trail | Actor, action, resource, request ID, source IP, and structured details |
| Recovery | Generation ordering, validation, atomic apply, and rollback |

References:

- https://docs.bunkerweb.io/latest/features/
- https://docs.bunkerweb.io/latest/concepts/
- https://nginx.org/en/docs/http/ngx_http_limit_req_module.html
- https://github.com/owasp-modsecurity/ModSecurity-nginx
- https://coreruleset.org/docs/

## Delivery order

1. Authentication, RBAC, user administration, ownership, and audit trail.
2. Proxy host CRUD, desired-state generation, apply, and rollback.
3. Automatic certificates and renewal status.
4. Rate-limit, IP-access, and header policies.
5. GeoIP analytics and country policies.
6. ModSecurity and CRS with detection-first rollout.
7. Optional active/standby gateway synchronization.

TCP streams, Kubernetes ingress, plugins, custom Lua, raw NGINX snippets, multi-region control planes, and volumetric DDoS mitigation are outside the initial scope.

TOTP, SSO/OIDC, SCIM, custom roles, and external audit export remain future
identity features. They should be added only with complete recovery and
administrator lockout handling.
