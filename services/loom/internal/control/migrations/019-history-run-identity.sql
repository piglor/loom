ALTER TABLE wait_history ADD COLUMN run_id uuid REFERENCES runs(id);

INSERT INTO schema_migrations(version) VALUES (19);
