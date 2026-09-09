ALTER TABLE sessions DROP CONSTRAINT sessions_runtime_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_runtime_check
    CHECK (runtime IN ('demo','remote-demo','codex-container'));
INSERT INTO schema_migrations(version) VALUES (10);
