CREATE TABLE integration_bindings (
    goal_id uuid NOT NULL REFERENCES goals(id),
    generation integer NOT NULL CHECK (generation > 0),
    organization text NOT NULL,
    source text NOT NULL,
    instance text NOT NULL,
    event_type text NOT NULL,
    resource text NOT NULL,
    version text NOT NULL,
    reconciled_at timestamptz,
    PRIMARY KEY (goal_id,generation),
    UNIQUE (organization,source,instance,event_type,resource,version)
);
CREATE TABLE integration_deliveries (
    organization text NOT NULL,
    source text NOT NULL,
    instance text NOT NULL,
    delivery_id text NOT NULL,
    digest text NOT NULL,
    condition jsonb,
    details jsonb NOT NULL,
    disposition text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (organization,source,instance,delivery_id)
);
CREATE INDEX integration_unmatched ON integration_deliveries
    (organization,source,instance) WHERE disposition='ignored';
INSERT INTO schema_migrations(version) VALUES (8);
