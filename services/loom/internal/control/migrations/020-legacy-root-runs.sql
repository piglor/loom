-- Complete the compatibility backfill for databases that already applied the
-- workflow migrations before a legacy Goal was imported.
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

INSERT INTO schema_migrations(version) VALUES (20);
