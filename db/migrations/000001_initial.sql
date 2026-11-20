CREATE TABLE users (
    id UUID PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    totp_secret_encrypted BYTEA,
    disabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE roles (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    scope TEXT NOT NULL CHECK (scope IN ('platform', 'application')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE permissions (
    name TEXT PRIMARY KEY,
    description TEXT NOT NULL
);

CREATE TABLE role_permissions (
    role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_name TEXT NOT NULL REFERENCES permissions(name) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_name)
);

CREATE TABLE user_platform_roles (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE applications (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_by UUID NOT NULL REFERENCES users(id),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE application_memberships (
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES roles(id),
    PRIMARY KEY (application_id, user_id)
);

CREATE TABLE proxy_hosts (
    id UUID PRIMARY KEY,
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    primary_domain TEXT NOT NULL UNIQUE,
    tls_policy JSONB NOT NULL DEFAULT '{}',
    rate_limit_policy JSONB NOT NULL DEFAULT '{}',
    waf_policy JSONB NOT NULL DEFAULT '{}',
    geo_policy JSONB NOT NULL DEFAULT '{}',
    access_policy JSONB NOT NULL DEFAULT '{}',
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE proxy_host_domains (
    proxy_host_id UUID NOT NULL REFERENCES proxy_hosts(id) ON DELETE CASCADE,
    domain TEXT NOT NULL UNIQUE,
    PRIMARY KEY (proxy_host_id, domain)
);

CREATE TABLE upstreams (
    id UUID PRIMARY KEY,
    proxy_host_id UUID NOT NULL REFERENCES proxy_hosts(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    weight INTEGER NOT NULL DEFAULT 1 CHECK (weight BETWEEN 1 AND 100),
    max_fails INTEGER NOT NULL DEFAULT 3 CHECK (max_fails BETWEEN 1 AND 100),
    fail_after_seconds INTEGER NOT NULL DEFAULT 10 CHECK (fail_after_seconds BETWEEN 1 AND 3600),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE config_revisions (
    id UUID PRIMARY KEY,
    generation BIGINT NOT NULL UNIQUE CHECK (generation > 0),
    revision TEXT NOT NULL UNIQUE,
    desired_state JSONB NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE deployment_runs (
    id UUID PRIMARY KEY,
    config_revision_id UUID NOT NULL REFERENCES config_revisions(id),
    status TEXT NOT NULL CHECK (status IN ('queued', 'validating', 'applying', 'succeeded', 'failed', 'rolled_back')),
    error_message TEXT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_user_id UUID REFERENCES users(id),
    application_id UUID REFERENCES applications(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT,
    request_id TEXT NOT NULL,
    source_ip INET,
    details JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);
CREATE INDEX user_platform_roles_role_id_idx ON user_platform_roles(role_id);
CREATE INDEX application_memberships_user_id_idx ON application_memberships(user_id);
CREATE INDEX proxy_hosts_application_id_idx ON proxy_hosts(application_id);
CREATE INDEX deployment_runs_status_idx ON deployment_runs(status);
CREATE INDEX audit_logs_application_created_idx ON audit_logs(application_id, created_at DESC);

INSERT INTO permissions (name, description) VALUES
    ('platform.admin', 'Full platform administration'),
    ('application.view', 'View an application and its proxy hosts'),
    ('proxy_hosts.write', 'Create and edit proxy hosts'),
    ('security.write', 'Edit WAF, rate-limit, GeoIP, and network policies'),
    ('deployments.write', 'Apply and roll back gateway configuration'),
    ('members.write', 'Manage application membership');

INSERT INTO roles (id, name, scope) VALUES
    ('00000000-0000-4000-8000-000000000001', 'Platform administrator', 'platform'),
    ('00000000-0000-4000-8000-000000000101', 'Owner', 'application'),
    ('00000000-0000-4000-8000-000000000102', 'Security administrator', 'application'),
    ('00000000-0000-4000-8000-000000000103', 'Operator', 'application'),
    ('00000000-0000-4000-8000-000000000104', 'Viewer', 'application');

INSERT INTO role_permissions (role_id, permission_name)
SELECT '00000000-0000-4000-8000-000000000001', name FROM permissions;

INSERT INTO role_permissions (role_id, permission_name) VALUES
    ('00000000-0000-4000-8000-000000000101', 'application.view'),
    ('00000000-0000-4000-8000-000000000101', 'proxy_hosts.write'),
    ('00000000-0000-4000-8000-000000000101', 'security.write'),
    ('00000000-0000-4000-8000-000000000101', 'deployments.write'),
    ('00000000-0000-4000-8000-000000000101', 'members.write'),
    ('00000000-0000-4000-8000-000000000102', 'application.view'),
    ('00000000-0000-4000-8000-000000000102', 'proxy_hosts.write'),
    ('00000000-0000-4000-8000-000000000102', 'security.write'),
    ('00000000-0000-4000-8000-000000000102', 'deployments.write'),
    ('00000000-0000-4000-8000-000000000103', 'application.view'),
    ('00000000-0000-4000-8000-000000000103', 'proxy_hosts.write'),
    ('00000000-0000-4000-8000-000000000103', 'deployments.write'),
    ('00000000-0000-4000-8000-000000000104', 'application.view');
