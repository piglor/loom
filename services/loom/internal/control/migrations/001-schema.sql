CREATE TABLE IF NOT EXISTS schema_migrations (
    version integer PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS goals (
    id uuid PRIMARY KEY,
    organization text NOT NULL,
    title text NOT NULL,
    objective text NOT NULL,
    state text NOT NULL CHECK (state IN
      ('READY','RUNNING','WAITING','BLOCKED','COMPLETED','FAILED','CANCELLED')),
    phase integer NOT NULL DEFAULT 0 CHECK (phase BETWEEN 0 AND 2),
    completion_criteria jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ended_at timestamptz,
    UNIQUE (organization, id)
);
CREATE TABLE IF NOT EXISTS runs (
    id uuid PRIMARY KEY,
    goal_id uuid NOT NULL UNIQUE REFERENCES goals(id),
    policy jsonb NOT NULL,
    workflow_id text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS workers (
    id text PRIMARY KEY,
    runtime text NOT NULL,
    capabilities jsonb NOT NULL
);
INSERT INTO workers VALUES ('demo-local', 'demo', '["finite-demo"]')
ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS sessions (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL UNIQUE REFERENCES runs(id),
    worker_id text NOT NULL REFERENCES workers(id),
    runtime text NOT NULL CHECK (runtime = 'demo')
);
CREATE TABLE IF NOT EXISTS waits (
    id uuid PRIMARY KEY,
    goal_id uuid NOT NULL UNIQUE REFERENCES goals(id),
    generation integer NOT NULL DEFAULT 1,
    condition jsonb NOT NULL,
    armed_at timestamptz,
    satisfied_at timestamptz,
    event_id uuid,
    closed_at timestamptz
);
CREATE TABLE IF NOT EXISTS attempts (
    id uuid PRIMARY KEY,
    goal_id uuid NOT NULL REFERENCES goals(id),
    session_id uuid NOT NULL REFERENCES sessions(id),
    worker_id text NOT NULL REFERENCES workers(id),
    phase integer NOT NULL CHECK (phase IN (0, 1)),
    state text NOT NULL CHECK (state IN ('RUNNING','STOPPED','UNKNOWN')),
    outcome text,
    started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    stopped_at timestamptz,
    duration_ms double precision CHECK (duration_ms >= 0),
    UNIQUE (goal_id, phase)
);
CREATE TABLE IF NOT EXISTS events (
    id uuid PRIMARY KEY,
    organization text NOT NULL,
    source text NOT NULL,
    delivery_id text NOT NULL,
    digest text NOT NULL,
    body jsonb NOT NULL,
    disposition text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (organization, source, delivery_id)
);
ALTER TABLE waits DROP CONSTRAINT IF EXISTS waits_event_id_fkey;
ALTER TABLE waits ADD CONSTRAINT waits_event_id_fkey
    FOREIGN KEY (event_id) REFERENCES events(id);
CREATE TABLE IF NOT EXISTS audit (
    sequence bigserial PRIMARY KEY,
    goal_id uuid NOT NULL REFERENCES goals(id),
    action text NOT NULL,
    details jsonb NOT NULL DEFAULT '{}',
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS outbox (
    id uuid PRIMARY KEY,
    goal_id uuid NOT NULL REFERENCES goals(id),
    kind text NOT NULL CHECK (kind IN ('start','wake')),
    available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    delivered_at timestamptz,
    failures integer NOT NULL DEFAULT 0,
    UNIQUE (goal_id, kind)
);
INSERT INTO schema_migrations(version) VALUES (1) ON CONFLICT DO NOTHING;
