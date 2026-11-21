ALTER TABLE proxy_hosts
    ADD COLUMN load_balancing TEXT NOT NULL DEFAULT 'round_robin'
        CHECK (load_balancing IN ('round_robin', 'least_conn', 'ip_hash')),
    ADD COLUMN security_headers BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN max_body_size_mb INTEGER NOT NULL DEFAULT 10
        CHECK (max_body_size_mb BETWEEN 1 AND 10240);

ALTER TABLE upstreams
    ADD COLUMN backup BOOLEAN NOT NULL DEFAULT false;
