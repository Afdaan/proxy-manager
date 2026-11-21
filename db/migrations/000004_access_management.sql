ALTER TABLE users
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    ADD COLUMN last_login_at TIMESTAMPTZ,
    ADD COLUMN password_changed_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX users_created_at_idx ON users(created_at DESC);
CREATE INDEX users_active_idx ON users(id) WHERE disabled_at IS NULL;
CREATE INDEX audit_logs_created_at_idx ON audit_logs(created_at DESC);
