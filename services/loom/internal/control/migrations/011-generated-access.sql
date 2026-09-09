-- Preserve existing compound keys while exposing a stable ORM row identity.
ALTER TABLE integration_bindings ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE integration_bindings ADD CONSTRAINT integration_bindings_row_id UNIQUE(id);
ALTER TABLE integration_deliveries ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE integration_deliveries ADD CONSTRAINT integration_deliveries_row_id UNIQUE(id);
ALTER TABLE integration_bindings ADD COLUMN attributes jsonb NOT NULL DEFAULT '{}';
ALTER TABLE integration_deliveries ADD COLUMN raw_body bytea;
INSERT INTO schema_migrations(version) VALUES(11);
