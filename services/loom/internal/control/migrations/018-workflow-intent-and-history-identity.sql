ALTER TABLE outbox ADD COLUMN step_key text NOT NULL DEFAULT '';
DROP INDEX IF EXISTS outbox_goal_run_kind;
CREATE UNIQUE INDEX outbox_goal_run_step_kind ON outbox(goal_id, run_id, step_key, kind);

INSERT INTO schema_migrations(version) VALUES (18);
