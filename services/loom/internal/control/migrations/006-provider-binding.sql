-- A runtime context cannot silently service two logical Sessions on one worker.
CREATE UNIQUE INDEX sessions_provider_binding
    ON sessions(worker_id, runtime, provider_session_id)
    WHERE provider_session_id IS NOT NULL;
INSERT INTO schema_migrations(version) VALUES (6);
