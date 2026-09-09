-- One-time cutover after old writers have stopped. Do not drop the source
-- tables: they are retained for audit and explicit recovery. Conflicting target
-- bindings deliberately abort the transaction rather than choosing an owner.
--
-- The generic GitHub adapter deliberately strengthened both trust-domain and
-- workflow identity. Reconcile the still-active durable wait before importing
-- its binding so an upgrade cannot strand an already suspended Goal.
UPDATE waits w
SET condition = jsonb_build_object(
        'source','github',
        'type','workflow.completed',
        'resource',b.repository_id::text || '/' || b.pull_request::text || '/' ||
            b.workflow_id::text || '/' || b.run_id::text || '/' || b.run_attempt::text,
        'version',b.head_sha)
FROM github_bindings b
WHERE w.goal_id=b.goal_id AND w.generation=b.generation
  AND w.condition=jsonb_build_object(
        'source','github',
        'type','workflow.completed',
        'resource',b.repository_id::text || '/' || b.pull_request::text || '/' ||
            b.run_id::text || '/' || b.run_attempt::text,
        'version',b.head_sha);

UPDATE wait_history w
SET condition = jsonb_build_object(
        'source','github',
        'type','workflow.completed',
        'resource',b.repository_id::text || '/' || b.pull_request::text || '/' ||
            b.workflow_id::text || '/' || b.run_id::text || '/' || b.run_attempt::text,
        'version',b.head_sha)
FROM github_bindings b
WHERE w.goal_id=b.goal_id AND w.generation=b.generation
  AND w.condition=jsonb_build_object(
        'source','github',
        'type','workflow.completed',
        'resource',b.repository_id::text || '/' || b.pull_request::text || '/' ||
            b.run_id::text || '/' || b.run_attempt::text,
        'version',b.head_sha);

UPDATE goals g
SET completion_criteria = jsonb_set(
        g.completion_criteria,'{event_matches}',
        jsonb_build_object(
            'source','github',
            'type','workflow.completed',
            'resource',b.repository_id::text || '/' || b.pull_request::text || '/' ||
                b.workflow_id::text || '/' || b.run_id::text || '/' || b.run_attempt::text,
            'version',b.head_sha))
FROM github_bindings b
WHERE g.id=b.goal_id
  AND g.completion_criteria->'event_matches'=jsonb_build_object(
        'source','github',
        'type','workflow.completed',
        'resource',b.repository_id::text || '/' || b.pull_request::text || '/' ||
            b.run_id::text || '/' || b.run_attempt::text,
        'version',b.head_sha);

INSERT INTO integration_bindings
    (goal_id,generation,organization,source,instance,event_type,resource,version,
     reconciled_at,attributes)
SELECT goal_id,generation,organization,'github','installation:' || installation_id::text,
       'workflow.completed',
       repository_id::text || '/' || pull_request::text || '/' ||
           workflow_id::text || '/' || run_id::text || '/' || run_attempt::text,
       head_sha,reconciled_at,
       to_jsonb(b) - ARRAY['goal_id','generation','organization','reconciled_at']
FROM github_bindings b;

-- Raw receipts are preserved, not newly trusted. A plugin must revalidate
-- ignored legacy payloads before populating a normalized condition/replaying.
INSERT INTO integration_deliveries
    (organization,source,instance,delivery_id,digest,condition,details,
     disposition,received_at,raw_body)
SELECT organization,'github',
       CASE WHEN payload #>> '{installation,id}' IS NULL
            THEN 'legacy-unresolved'
            ELSE 'installation:' || (payload #>> '{installation,id}') END,
       delivery_id::text,digest,NULL,
       jsonb_build_object('legacy_event_type',event_type,'legacy_payload',payload),
       disposition,received_at,raw_body
FROM github_deliveries;

INSERT INTO schema_migrations(version) VALUES (12);
