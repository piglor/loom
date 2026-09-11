CREATE TABLE integration_credentials (
    id uuid PRIMARY KEY,
    organization text NOT NULL,
    plugin_id text NOT NULL,
    label text NOT NULL,
    secret_reference text NOT NULL UNIQUE,
    secret_version integer NOT NULL CHECK (secret_version > 0),
    state text NOT NULL CHECK (state IN ('setup_incomplete','active','needs_attention','disabled')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE integration_instances (
    id uuid PRIMARY KEY,
    organization text NOT NULL,
    credential_id uuid NOT NULL REFERENCES integration_credentials(id),
    plugin_id text NOT NULL,
    external_instance_id text NOT NULL,
    account_id text NOT NULL,
    account_label text NOT NULL,
    repository_selection text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}',
    state text NOT NULL CHECK (state IN ('active','needs_attention','disabled')),
    last_verified_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (organization, plugin_id, external_instance_id)
);

CREATE TABLE integration_setup_sessions (
    id uuid PRIMARY KEY,
    organization text NOT NULL,
    plugin_id text NOT NULL,
    mode text NOT NULL CHECK (mode IN ('manifest','manual')),
    state_hash text NOT NULL UNIQUE,
    stage text NOT NULL CHECK (stage IN ('created','app_created','installation_pending','complete','failed')),
    credential_id uuid REFERENCES integration_credentials(id),
    pending_installation_id text,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX integration_credentials_organization_plugin
    ON integration_credentials(organization, plugin_id);
CREATE INDEX integration_instances_credential
    ON integration_instances(credential_id);
CREATE INDEX integration_setup_sessions_expiry
    ON integration_setup_sessions(expires_at) WHERE consumed_at IS NULL;

INSERT INTO schema_migrations(version) VALUES (14);
