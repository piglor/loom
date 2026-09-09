-- Existing v1 Goals keep their two-attempt policy. The new core policy is opt-in.
ALTER TABLE goals DROP CONSTRAINT goals_phase_check;
ALTER TABLE goals ADD CONSTRAINT goals_phase_check CHECK (phase >= 0);
ALTER TABLE attempts DROP CONSTRAINT attempts_phase_check;
ALTER TABLE attempts ADD CONSTRAINT attempts_phase_check CHECK (phase >= 0);
ALTER TABLE waits ADD COLUMN prepared_by_attempt uuid REFERENCES attempts(id);
CREATE TABLE wait_history (LIKE waits INCLUDING DEFAULTS);
ALTER TABLE wait_history ADD PRIMARY KEY (id);
ALTER TABLE wait_history ADD FOREIGN KEY (goal_id) REFERENCES goals(id);
ALTER TABLE wait_history ADD FOREIGN KEY (event_id) REFERENCES events(id);
ALTER TABLE wait_history ADD FOREIGN KEY (prepared_by_attempt) REFERENCES attempts(id);
ALTER TABLE wait_history ADD UNIQUE (goal_id, generation);
INSERT INTO schema_migrations(version) VALUES (7);
