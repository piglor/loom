ALTER TABLE workers ADD COLUMN IF NOT EXISTS organization text;
ALTER TABLE workers ADD COLUMN IF NOT EXISTS token_hash text UNIQUE;
ALTER TABLE workers ADD COLUMN IF NOT EXISTS revoked_at timestamptz;
ALTER TABLE workers ADD COLUMN IF NOT EXISTS last_seen_at timestamptz;
ALTER TABLE workers ADD COLUMN IF NOT EXISTS workspace_ref text;
ALTER TABLE workers ADD COLUMN IF NOT EXISTS labels jsonb NOT NULL DEFAULT '{}';
ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_runtime_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_runtime_check
    CHECK (runtime IN ('demo','remote-demo'));
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS provider_session_id text;
ALTER TABLE attempts DROP CONSTRAINT IF EXISTS attempts_state_check;
ALTER TABLE attempts ADD CONSTRAINT attempts_state_check
    CHECK (state IN ('QUEUED','RUNNING','STOPPED','UNKNOWN'));
ALTER TABLE goals ADD COLUMN IF NOT EXISTS waiting_reason text;
CREATE TABLE IF NOT EXISTS commands (
    id uuid PRIMARY KEY,
    goal_id uuid NOT NULL REFERENCES goals(id),
    attempt_id uuid NOT NULL UNIQUE REFERENCES attempts(id),
    worker_id text NOT NULL REFERENCES workers(id),
    state text NOT NULL CHECK (state IN ('QUEUED','CLAIMED','STOPPED','UNKNOWN')),
    claim_id uuid,
    report_digest text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    claimed_at timestamptz,
    stopped_at timestamptz
);
CREATE INDEX IF NOT EXISTS commands_pending ON commands(worker_id,created_at)
    WHERE state IN ('QUEUED','CLAIMED');
INSERT INTO schema_migrations(version) VALUES (2) ON CONFLICT DO NOTHING;
