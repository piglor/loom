ALTER TABLE github_bindings ADD COLUMN generation integer NOT NULL DEFAULT 1
    CHECK (generation > 0);
ALTER TABLE github_bindings DROP CONSTRAINT github_bindings_pkey;
ALTER TABLE github_bindings ADD PRIMARY KEY (goal_id,generation);
INSERT INTO schema_migrations(version) VALUES (9);
