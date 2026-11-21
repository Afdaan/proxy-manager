# Managed NGINX Revisions Plan

Status: initial implementation complete. Certificate orchestration, host editing, GeoIP policy UI, and rollback UI remain follow-up work.

## Goal

Connect the existing Proxy Host database records to the privileged agent so a reviewed deployment can generate, validate, activate, and roll back NGINX configuration safely.

The result must remain readable to an operator:

- One generated file per Proxy Host.
- Multiple domain aliases may share that file when their upstream and policy are identical.
- Global HTTP directives stay in separate generated files.
- Every deployment creates an immutable revision directory.
- NGINX switches revisions through one active symlink.
- PostgreSQL remains the source of truth. Generated files are not edited manually.

## Frozen Decisions

1. The control-plane server and PostgreSQL run in Docker Compose.
2. NGINX, Certbot, and the privileged agent run on the gateway host.
3. The server talks to the agent only through `/run/proxy-manager/agent.sock`.
4. The server cannot write NGINX files or execute NGINX and Certbot.
5. Changes are saved first, then deployed explicitly. Creating a Proxy Host does not silently reload NGINX.
6. A deployment always renders the complete desired state, not only the changed host.
7. Raw NGINX snippets are not accepted from users.
8. HTTP reverse proxying is in scope. TCP/UDP streams remain out of scope.
9. The initial product manages one gateway VM and one public IP address.
10. Load balancing distributes requests across private application backends behind that gateway. It does not create additional public ingress addresses or an active-active gateway cluster.

## Deployment Topology

The initial topology is intentionally small:

```text
Internet
  -> one public IP
  -> NAT ports 80 and 443
  -> one NGINX gateway VM
  -> one or more private backend VMs
```

Example:

```text
app.afdaan.com
  -> public IP
  -> NGINX gateway
  -> 10.10.10.20:3000 weight=2
  -> 10.10.10.21:3000 weight=1
```

The load-balancer settings are useful when one application has multiple private replicas. A Proxy Host with one backend continues to work as a normal reverse proxy.

The Alibaba-style reference applies only to information architecture: grouped backend, timeout, retry, rate-limit, and security controls. Cloud load-balancer topology, multiple ingress addresses, autoscaling, service discovery, and provider-managed high availability are not copied.

This gateway remains a single point of failure and a bandwidth bottleneck. Future gateway high availability would require a separate design such as two gateway nodes with a virtual IP or an upstream load balancer. It is not part of this implementation.

## Current State

Already implemented:

- Authentication, sessions, CSRF protection, and RBAC.
- Proxy Host list and create APIs.
- PostgreSQL tables for revisions and deployment runs.
- Typed gateway configuration validation.
- NGINX rendering into one combined file.
- Agent validate, apply, reload, rollback, and stale-generation checks.
- Agent authentication through socket permissions and a bearer token.

Missing connection:

```text
PostgreSQL desired state -> server reconciler -> agent -> NGINX
```

Until this work is complete, creating a Proxy Host in the UI changes PostgreSQL only.

## Runtime Architecture

```text
Browser
  -> React UI
  -> Go API
  -> PostgreSQL desired state
  -> deployment request
  -> deterministic GatewayConfig
  -> Unix socket agent client
  -> privileged agent
  -> generated revision directory
  -> nginx -t
  -> active symlink switch
  -> NGINX reload
```

Traffic does not pass through the Go server:

```text
Internet -> NGINX -> private VM IP and port
```

The UI may be unavailable while NGINX continues serving the last active revision.

## Filesystem Layout

Static NGINX integration:

```text
/etc/nginx/conf.d/proxy-manager-ui.conf
/etc/nginx/conf.d/proxy-manager-managed.conf
/etc/nginx/proxy-manager/active.conf
```

`proxy-manager-managed.conf` contains only:

```nginx
include /etc/nginx/proxy-manager/active.conf;
```

Generated application state:

```text
/var/lib/proxy-manager/
├── revisions/
│   └── 2026-11-21_03-30-42Z--rev-42--a81c7f/
│       ├── nginx.conf
│       ├── manifest.json
│       ├── globals/
│       │   ├── 10-rate-limit-zones.conf
│       │   ├── 20-geoip-maps.conf
│       │   └── 30-waf.conf
│       └── hosts/
│           ├── dashboard.afdaan.com--bb819a.conf
│           └── api.afdaan.com--8d129a.conf
├── previous
└── deploy.lock
```

`/etc/nginx/proxy-manager/active.conf` is a symlink to the active revision's `nginx.conf`.

The revision index contains absolute includes for its own immutable files:

```nginx
include /var/lib/proxy-manager/revisions/<revision>/globals/*.conf;
include /var/lib/proxy-manager/revisions/<revision>/hosts/*.conf;
```

## Naming Rules

Revision directory:

```text
<UTC timestamp>--rev-<generation>--<short revision ID>
```

Example:

```text
2026-11-21_03-30-42Z--rev-42--a81c7f
```

Proxy Host file:

```text
<normalized primary domain>--<short host ID>.conf
```

Examples:

```text
dashboard.afdaan.com--bb819a.conf
api.afdaan.com--8d129a.conf
wildcard.afdaan.com--19ad02.conf
```

Rules:

- Filenames are generated only from validated domains and database IDs.
- Wildcard `*.afdaan.com` becomes `wildcard.afdaan.com` in the filename.
- Internationalized domains use their normalized ASCII form.
- User display names never become filesystem paths.
- The complete host UUID remains in `manifest.json` and the file header.

## Generated Host File

Each Proxy Host file contains its upstream and HTTP server blocks. It starts with an operator-readable header:

```nginx
# Managed by Proxy Manager. DO NOT EDIT.
# Host ID: <full UUID>
# Name: Internal Dashboard
# Domains: dashboard.afdaan.com, dashboard.internal.example
# Upstream: http://10.10.10.20:3000
# TLS: automatic
# WAF: detect
# Rate limit: 100 requests/minute
# Revision: 42
```

Generated comments contain no secrets.

## Operator Experience and Configuration Model

The UI should borrow the useful structure of a managed application load balancer without copying cloud-provider complexity.

The main object remains a Proxy Host:

```text
Proxy Host
├── Domains and TLS listener
├── Default route
├── Backend pool
├── Load-balancing policy
├── Timeouts and retry policy
├── Rate-limit policy
├── WAF and access policy
└── Observability settings
```

The first release does not introduce separate listener and server-group pages. Those concepts remain sections inside one Proxy Host form so normal reverse-proxy setup stays quick.

### Basic mode

Basic mode contains only the settings required by most deployments:

- Name.
- Primary domain and aliases.
- One backend URL by default.
- Automatic HTTPS.
- Optional rate-limit preset.

An `Add backend` action enables the backend-pool and load-balancing controls. Algorithm, weight, backup, and balancing-specific settings remain hidden while only one backend exists.

Multiple backends on the same physical node may distribute traffic across processes or containers, but do not provide node-level high availability. The UI must not describe that topology as highly available.

### Advanced mode

Advanced mode uses grouped sections instead of raw directives:

1. **Backends**
   - Address, port, protocol, weight, backup status, and enabled status.
   - Maximum connections.
   - Passive failure threshold and failure timeout.
2. **Load balancing**
   - Weighted round robin.
   - Least connections.
   - Client IP hash for session affinity.
   - Upstream keepalive connection count.
3. **Failure handling**
   - Connect, send, and read timeouts.
   - Retry conditions.
   - Maximum retry attempts and retry time budget.
   - Retries for non-idempotent requests remain disabled.
4. **Request limits**
   - Maximum request body size.
   - Per-client-IP request rate.
   - Total Proxy Host request rate.
   - Burst capacity and reject status.
   - Observe-only mode when supported by the installed NGINX version.
5. **Security**
   - WAF off, detect, or block.
   - Country allow or block policy.
   - CIDR allow and deny rules.
   - Safe response-header preset.
   - Custom CSP remains application-owned.
6. **Proxy behavior**
   - WebSocket support.
   - Request and response buffering.
   - Preserve or replace the upstream Host header.
   - Optional upstream TLS verification.

The UI shows safe defaults and short explanations. Advanced settings stay collapsed until requested.

### Deferred routing rules

Path, header, method, query-string, cookie, redirect, rewrite, and fixed-response rules are useful but significantly expand validation and conflict handling. The first reconciler release supports one default `/` route per Proxy Host.

Structured routing rules may be added after deployment, rollback, TLS, and policy handling are stable. Raw location blocks remain prohibited.

## Generated NGINX Policy

Generated files should be explicit and readable, but only include directives supported by the detected NGINX build.

Global generated files:

```text
globals/
├── 10-real-ip.conf
├── 20-log-format.conf
├── 30-rate-limit-zones.conf
├── 40-geoip-maps.conf
└── 50-waf.conf
```

Host files use consistent sections:

```nginx
# Managed by Proxy Manager. DO NOT EDIT.
# Metadata

# Backend pool
upstream pm_<host-id> {
    least_conn;
    keepalive 32;

    server 10.10.10.20:3000 weight=2 max_conns=100 max_fails=3 fail_timeout=10s;
    server 10.10.10.21:3000 weight=1 max_conns=100 max_fails=3 fail_timeout=10s;
}

# HTTP listener and ACME challenge
server {
    listen 80;
    server_name dashboard.afdaan.com;

    location ^~ /.well-known/acme-challenge/ {
        root /var/lib/proxy-manager/acme;
    }

    location / {
        return 308 https://$host$request_uri;
    }
}

# HTTPS listener and proxy policy
server {
    listen 443 ssl;
    server_name dashboard.afdaan.com;

    ssl_certificate /etc/letsencrypt/live/dashboard.afdaan.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/dashboard.afdaan.com/privkey.pem;

    client_max_body_size 10m;
    limit_req zone=pm_ip_<host-id> burst=20 nodelay;
    limit_req_status 429;

    location / {
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Connection "";

        proxy_connect_timeout 5s;
        proxy_send_timeout 60s;
        proxy_read_timeout 60s;
        proxy_next_upstream error timeout http_502 http_503 http_504;
        proxy_next_upstream_tries 2;
        proxy_next_upstream_timeout 10s;

        proxy_pass http://pm_<host-id>;
    }
}
```

This example is illustrative. The renderer emits only settings enabled by the typed policy and supported by the host capability report.

## Capability Detection

The deployed NGINX version and compiled modules cannot be assumed. Before accepting advanced policies, the agent reports capabilities derived from the installed binary and known module files.

Required capability checks include:

- NGINX version.
- HTTP SSL module.
- HTTP real IP module.
- HTTP limit request module.
- HTTP upstream features used by the selected policy.
- ModSecurity module and OWASP CRS paths.
- GeoIP2 module and database path.

The server stores the latest capability report and the UI disables unsupported controls with a direct explanation.

The renderer never silently drops a requested security policy. Deployment fails before activation when the host cannot provide a requested feature.

Active health checks, dynamic upstream membership, slow start, and connection draining are not promised for the open-source NGINX target. The first release uses passive failure detection through upstream failure counters and proxy retry policy.

## Security Defaults

Default generated behavior:

- TLS is automatic when explicitly enabled and certificates exist.
- TLS files are referenced only from fixed Certbot directories.
- Upstream TLS verification is enabled only when a trust store and expected server name are configured.
- Non-idempotent requests are not retried after being sent upstream.
- Client IP rate limiting uses the trusted client address determined by NGINX.
- Forwarded client-IP headers are trusted only from explicitly configured proxy CIDRs.
- The default rate-limit rejection status is `429`.
- WAF starts in detection mode unless blocking is explicitly selected.
- CSP is not generated automatically.
- Server tokens are disabled globally when supported.
- Generated headers never contain credentials.

Trusted proxy CIDRs are a platform-level setting, not a per-host setting. This prevents clients from bypassing IP policies or rate limits by spoofing forwarding headers.

## Phase 1: Multi-file Renderer

Replace the current single-byte-buffer renderer with a deterministic revision bundle.

Primary files:

- `internal/nginx/render.go`
- `internal/nginx/config.tmpl`
- `internal/nginx/render_test.go`

Planned changes:

1. Return an ordered list of generated files containing relative path, content, and mode.
2. Generate `nginx.conf`, `manifest.json`, global files, and one file per Proxy Host.
3. Sort hosts by normalized primary domain and stable ID.
4. Sort aliases, upstreams, CIDRs, and country codes before rendering.
5. Use host IDs for unique upstream and rate-limit zone names.
6. Reject duplicate generated paths before writing anything.
7. Keep all path generation internal; no API accepts a target path.

Tests:

- Same desired state produces byte-identical files.
- One Proxy Host produces one host file.
- Aliases with the same policy remain in one file.
- Different Proxy Hosts remain in separate files.
- Wildcard and internationalized domains produce safe filenames.
- Global directives are not rendered inside `server` blocks.

## Phase 2: Agent Revision Writer

Update the agent to write complete revision directories instead of one `nginx.conf` file.

Primary files:

- `internal/agent/apply.go`
- `internal/agent/apply_test.go`
- `cmd/agent/main.go`
- `deploy/systemd/proxy-manager-agent.service`

Apply sequence:

1. Acquire the deployment lock.
2. Reject stale or conflicting generations.
3. Render the complete revision bundle.
4. Write into a temporary directory under `/var/lib/proxy-manager/revisions`.
5. Set directories to `0750` and files to `0640`.
6. Rename the complete temporary directory to its final immutable name.
7. Switch `active.conf` to the candidate revision using an atomic symlink replacement.
8. Run `nginx -t`.
9. Restore the old symlink immediately if validation fails.
10. Reload NGINX only after validation succeeds.
11. Restore and reload the previous revision if reload fails.
12. Record the previous working target.
13. Delete old inactive revisions after success, retaining the latest ten.

The active and previous revisions must never be removed by retention cleanup.

Tests:

- Partial writes never become active.
- Validation failure preserves the previous revision.
- Reload failure restores the previous revision.
- Reapplying the same generation and revision is idempotent.
- A stale generation is rejected.
- Retention keeps active and previous revisions.
- Generated paths cannot escape the revision directory.

## Phase 3: Desired-state Builder and Agent Client

Add the missing server-side reconciliation path.

Planned components:

- Store query that loads every enabled Proxy Host, domain, upstream, and policy.
- Deterministic conversion from database rows to `model.GatewayConfig`.
- Unix-socket HTTP client with request timeout and bearer token.
- Transactional revision and deployment-run creation.
- Deployment status transitions with bounded error messages.

Deployment flow:

```text
queued -> validating -> applying -> succeeded
                              \-> failed
```

Generation allocation must be serialized in PostgreSQL. Concurrent deploy requests must not create the same generation or apply out of order.

The revision ID will be derived from the canonical desired-state digest. Secrets are excluded from the digest and stored revision document.

New API surface:

```text
POST /api/v1/deployments
GET  /api/v1/deployments
GET  /api/v1/deployments/current
POST /api/v1/deployments/{id}/rollback
```

Deployment requests require an explicit permission. Rollback requires platform administration for the first release.

## Phase 4: Deployment UI

Add a small deployment workflow instead of automatically applying every edit.

UI behavior:

- Show `Changes not deployed` after desired-state edits.
- Show the latest active generation and deployment time.
- Provide a `Deploy changes` button to authorized users.
- Show queued, validating, applying, active, failed, and rolled-back states.
- Display safe validation errors without leaking tokens or filesystem internals.
- Show the deployment summary and affected Proxy Hosts.
- Provide rollback for platform administrators.

The UI does not expose raw generated NGINX editing.

## Phase 5: Automatic TLS

Automatic TLS uses two deployments:

1. Deploy an HTTP-only host with the fixed ACME webroot location.
2. Ask the agent to issue the certificate through Certbot.
3. Verify the expected certificate files exist.
4. Create and apply a new revision with HTTPS enabled.

Failure behavior:

- Certificate failure does not generate broken HTTPS directives.
- The HTTP route remains available unless the user requires HTTPS-only behavior.
- Renewal uses a root-owned fixed hook that validates and reloads NGINX.
- Certificate status and last error are stored in PostgreSQL.

## Phase 6: Rate Limit, WAF, and GeoIP Activation

Rate limiting:

- Define unique `limit_req_zone` directives in `globals/10-rate-limit-zones.conf`.
- Apply `limit_req` inside the owning Proxy Host file.
- Keep zone names stable by using the Proxy Host ID.

WAF:

- Detect the ModSecurity module and OWASP CRS paths before enabling a policy.
- Refuse `detect` or `block` when required host capabilities are missing.
- Start new WAF configurations in detection mode unless the user explicitly chooses blocking.

GeoIP:

- Detect the GeoIP2 module and database before enabling country policies.
- Keep shared maps in `globals/20-geoip-maps.conf`.
- Apply the allow or deny decision inside the Proxy Host file.

These controls cannot mitigate volumetric attacks that saturate the public connection before NGINX receives the request.

## Migration and Deployment

The first successful deployment performs the migration naturally:

1. Keep the existing `active.conf` untouched while rendering the first revision.
2. Validate the generated revision.
3. Switch `active.conf` only after the candidate directory is complete.
4. Preserve the previous active target for rollback.

Installer changes:

- Create the revisions directory with restricted permissions.
- Keep the static managed include unchanged.
- Wait for the agent socket before starting the server.
- Verify the agent can read NGINX and write only its approved paths.

No existing non-Proxy-Manager NGINX files are moved or rewritten.

## Backup and Recovery

Required backups:

- PostgreSQL data.
- `/opt/proxy-manager/.env` stored securely.
- `/etc/letsencrypt`.

Generated revisions are useful for immediate rollback but are not the primary backup. They can be regenerated from PostgreSQL after restoring secrets and certificates.

Recovery order:

1. Restore PostgreSQL and `.env`.
2. Restore `/etc/letsencrypt`.
3. Start the agent and control plane.
4. Generate and deploy a fresh revision.
5. Validate external domains individually.

## Acceptance Criteria

The work is complete when:

- One Proxy Host generates exactly one readable host file.
- The active revision contains every enabled Proxy Host and no deleted host.
- The same database state renders the same configuration bytes.
- A failed validation never reloads NGINX.
- A failed reload restores the previous working revision.
- Rollback can activate the previous revision without database corruption.
- Concurrent deployment requests cannot apply out of order.
- The server never writes NGINX paths directly.
- The agent rejects raw paths, raw directives, and stale generations.
- NGINX continues serving the active revision when the UI, server, or database is unavailable.
- Tests cover renderer determinism, path safety, apply, rollback, retention, and deployment state transitions.

## Explicitly Out of Scope

- Raw NGINX configuration snippets.
- TCP and UDP stream proxying.
- Kubernetes ingress integration.
- Docker socket access.
- Multi-gateway synchronization.
- Plugin execution and custom Lua.
- Volumetric DDoS mitigation.

These features require separate designs and will not be added while completing the HTTP reverse-proxy deployment path.
