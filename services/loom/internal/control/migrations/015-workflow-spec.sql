CREATE TABLE workflow_definitions (
    id uuid PRIMARY KEY,
    organization text NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    state text NOT NULL DEFAULT 'draft' CHECK (state IN ('draft','published','archived')),
    draft_spec jsonb NOT NULL DEFAULT '{}',
    latest_version integer NOT NULL DEFAULT 0 CHECK (latest_version >= 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (organization, name)
);

ALTER TABLE integration_instances ADD COLUMN routing_identity text;
UPDATE integration_instances
SET routing_identity = CASE
    WHEN plugin_id = 'github' THEN 'installation:' || external_instance_id
    ELSE plugin_id || ':' || external_instance_id
END;
ALTER TABLE integration_instances ALTER COLUMN routing_identity SET NOT NULL;
CREATE UNIQUE INDEX integration_instance_routing_identity
    ON integration_instances(organization, plugin_id, routing_identity);

CREATE TABLE workflow_versions (
    id uuid PRIMARY KEY,
    definition_id uuid NOT NULL REFERENCES workflow_definitions(id),
    organization text NOT NULL,
    version integer NOT NULL CHECK (version > 0),
    spec jsonb NOT NULL,
    spec_digest text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (definition_id, version)
);

CREATE TABLE workflow_trigger_bindings (
    id uuid PRIMARY KEY,
    organization text NOT NULL,
    workflow_version_id uuid NOT NULL REFERENCES workflow_versions(id),
    integration_instance_id uuid NOT NULL REFERENCES integration_instances(id),
    source text NOT NULL,
    ingress_instance text NOT NULL,
    event_type text NOT NULL,
    resource text NOT NULL DEFAULT '*',
    version text NOT NULL DEFAULT '*',
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX workflow_trigger_route ON workflow_trigger_bindings
    (organization, source, ingress_instance, event_type) WHERE enabled;

ALTER TABLE runs DROP CONSTRAINT IF EXISTS runs_goal_id_key;
ALTER TABLE runs
    ADD COLUMN workflow_definition_id uuid REFERENCES workflow_definitions(id),
    ADD COLUMN workflow_version_id uuid REFERENCES workflow_versions(id),
    ADD COLUMN parent_run_id uuid REFERENCES runs(id),
    ADD COLUMN invoking_step_key text,
    ADD COLUMN state text NOT NULL DEFAULT 'legacy',
    ADD COLUMN current_step_key text,
    ADD COLUMN orchestration_reference text,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN ended_at timestamptz;
CREATE UNIQUE INDEX one_active_root_workflow_run_per_goal ON runs(goal_id)
    WHERE parent_run_id IS NULL AND state IN ('pending','running','waiting');

CREATE TABLE workflow_step_runs (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES runs(id),
    step_key text NOT NULL,
    step_type text NOT NULL CHECK (step_type IN ('agent','wait_event','condition','subflow','complete')),
    position integer NOT NULL CHECK (position >= 0),
    state text NOT NULL CHECK (state IN ('pending','running','waiting','succeeded','failed','cancelled')),
    attempt integer NOT NULL DEFAULT 1 CHECK (attempt > 0),
    input jsonb NOT NULL DEFAULT '{}',
    output jsonb NOT NULL DEFAULT '{}',
    error_code text,
    started_at timestamptz,
    ended_at timestamptz,
    UNIQUE (run_id, step_key, attempt)
);

ALTER TABLE outbox DROP CONSTRAINT IF EXISTS outbox_kind_check;
ALTER TABLE outbox ADD CONSTRAINT outbox_kind_check CHECK
    (kind IN ('start','wake','workflow_start','workflow_signal','agent_step_ready'));

INSERT INTO schema_migrations(version) VALUES (15);
