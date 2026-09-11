ALTER TABLE attempts ADD COLUMN run_id uuid REFERENCES runs(id);
UPDATE attempts a
SET run_id = s.run_id
FROM sessions s
WHERE s.id = a.session_id;
ALTER TABLE attempts ALTER COLUMN run_id SET NOT NULL;
ALTER TABLE attempts DROP CONSTRAINT IF EXISTS attempts_goal_id_phase_key;
CREATE UNIQUE INDEX attempts_run_phase ON attempts(run_id, phase);

INSERT INTO schema_migrations(version) VALUES (17);
