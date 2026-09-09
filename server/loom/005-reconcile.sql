ALTER TABLE github_bindings ADD COLUMN reconciled_at timestamptz;
INSERT INTO schema_migrations(version) VALUES (5);
