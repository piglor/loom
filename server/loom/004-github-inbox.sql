ALTER TABLE github_deliveries ADD COLUMN event_type text;
ALTER TABLE github_deliveries ADD COLUMN raw_body bytea;
ALTER TABLE github_deliveries ADD COLUMN payload jsonb;
CREATE INDEX github_unmatched_payload ON github_deliveries USING gin(payload)
    WHERE disposition='ignored';
INSERT INTO schema_migrations(version) VALUES (4);
