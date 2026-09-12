CREATE TABLE IF NOT EXISTS auth_oauth_states (
    id uuid PRIMARY KEY,
    organization text NOT NULL,
    state_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);
CREATE INDEX IF NOT EXISTS auth_oauth_states_active
    ON auth_oauth_states(organization, expires_at)
    WHERE consumed_at IS NULL;

INSERT INTO schema_migrations(version) VALUES (22) ON CONFLICT DO NOTHING;
