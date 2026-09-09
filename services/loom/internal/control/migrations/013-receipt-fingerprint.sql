-- Raw digest remains compatible with historical signed deliveries. The optional
-- fingerprint additionally binds normalized Go adapter output on new receipts.
ALTER TABLE integration_deliveries ADD COLUMN fingerprint text;
CREATE INDEX integration_pending_condition ON integration_deliveries
    (organization,source,instance,(condition->>'type'),(condition->>'resource'),
     (condition->>'version')) WHERE disposition='ignored';
INSERT INTO schema_migrations(version) VALUES (13);
