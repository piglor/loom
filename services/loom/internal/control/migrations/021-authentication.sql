CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY,
    organization text NOT NULL,
    email text NOT NULL,
    display_name text NOT NULL DEFAULT '',
    role text NOT NULL DEFAULT 'member' CHECK (role IN ('admin','member')),
    password_hash text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    disabled_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS users_organization_email
    ON users (organization, email);

CREATE TABLE IF NOT EXISTS auth_identities (
    id uuid PRIMARY KEY,
    organization text NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL,
    subject text NOT NULL,
    email text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (organization, provider, subject)
);
CREATE INDEX IF NOT EXISTS auth_identities_user ON auth_identities(user_id);

CREATE TABLE IF NOT EXISTS auth_sessions (
    id uuid PRIMARY KEY,
    organization text NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    csrf_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_seen_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE INDEX IF NOT EXISTS auth_sessions_active
    ON auth_sessions(organization, user_id, expires_at)
    WHERE revoked_at IS NULL;

INSERT INTO schema_migrations(version) VALUES (21) ON CONFLICT DO NOTHING;
