ALTER TABLE deployment_runs
    DROP CONSTRAINT deployment_runs_status_check;

ALTER TABLE deployment_runs
    ADD CONSTRAINT deployment_runs_status_check
        CHECK (status IN (
            'queued',
            'validating',
            'applying',
            'retrying',
            'succeeded',
            'failed',
            'superseded',
            'rolled_back'
        )),
    ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE proxy_hosts
    ADD COLUMN desired_generation BIGINT NOT NULL DEFAULT 0 CHECK (desired_generation >= 0);

UPDATE permissions
SET description = 'Retry failed gateway configuration and roll back revisions'
WHERE name = 'deployments.write';

CREATE TABLE gateway_state (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    active_generation BIGINT CHECK (active_generation > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO gateway_state (singleton, active_generation)
SELECT true, MAX(config_revisions.generation)
FROM deployment_runs
JOIN config_revisions ON config_revisions.id = deployment_runs.config_revision_id
WHERE deployment_runs.status = 'succeeded';

UPDATE proxy_hosts
SET desired_generation = COALESCE((SELECT MAX(generation) FROM config_revisions), 0);

CREATE INDEX deployment_runs_reconcile_idx
    ON deployment_runs (next_attempt_at, created_at)
    WHERE status IN ('queued', 'validating', 'applying', 'retrying');

CREATE UNIQUE INDEX deployment_runs_revision_idx
    ON deployment_runs (config_revision_id);
