CREATE TABLE github_bindings (
    goal_id uuid PRIMARY KEY REFERENCES goals(id),
    organization text NOT NULL,
    installation_id bigint NOT NULL,
    repository_id bigint NOT NULL,
    pull_request bigint NOT NULL,
    head_sha text NOT NULL,
    run_id bigint NOT NULL,
    run_attempt integer NOT NULL,
    workflow_id bigint NOT NULL,
    UNIQUE (organization,installation_id,repository_id,run_id,run_attempt)
);
CREATE TABLE github_deliveries (
    organization text NOT NULL,
    delivery_id uuid NOT NULL,
    digest text NOT NULL,
    disposition text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(organization,delivery_id)
);
INSERT INTO schema_migrations(version) VALUES (3);
