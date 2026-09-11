ALTER TABLE runs
    ADD COLUMN context jsonb NOT NULL DEFAULT '{}';

ALTER TABLE waits
    ADD COLUMN run_id uuid REFERENCES runs(id);

-- Legacy databases may have Goals without a generic root Run. Create the
-- durable compatibility rows before tightening outbox ownership below.
INSERT INTO runs (id, goal_id, policy, state, created_at, updated_at)
SELECT gen_random_uuid(), g.id,
       jsonb_build_object('runtime','demo','max_attempts',2,'lifecycle','legacy'),
       'legacy', clock_timestamp(), clock_timestamp()
FROM goals g
LEFT JOIN runs r ON r.goal_id = g.id AND r.parent_run_id IS NULL
WHERE r.id IS NULL;

INSERT INTO sessions (id, run_id, worker_id, runtime)
SELECT gen_random_uuid(), r.id, 'demo-local', 'demo'
FROM runs r
LEFT JOIN sessions s ON s.run_id = r.id
WHERE r.state = 'legacy' AND s.id IS NULL;

ALTER TABLE outbox
    ADD COLUMN run_id uuid REFERENCES runs(id);
UPDATE outbox o
SET run_id = r.id
FROM runs r
WHERE r.goal_id = o.goal_id AND r.parent_run_id IS NULL;
ALTER TABLE outbox ALTER COLUMN run_id SET NOT NULL;
ALTER TABLE outbox DROP CONSTRAINT IF EXISTS outbox_goal_id_kind_key;
CREATE UNIQUE INDEX outbox_goal_run_kind ON outbox(goal_id, run_id, kind);

INSERT INTO schema_migrations(version) VALUES (16);
