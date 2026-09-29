CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY,
  checksum TEXT NOT NULL,
  applied_at TEXT NOT NULL
);

ALTER TABLE remote_nodes
  ADD COLUMN desired_policy TEXT NOT NULL DEFAULT '{"version":1,"fields":{"custom404Page":{"mode":"inherit"}}}';

UPDATE remote_nodes
SET desired_policy = json_object(
  'version', 1,
  'fields', json_object(
    'custom404Page', json_object(
      'mode', 'custom',
      'value', json_extract(desired_snapshot, '$.custom404Page')
    )
  )
)
WHERE desired_snapshot IS NOT NULL
  AND json_valid(desired_snapshot)
  AND COALESCE(json_extract(desired_snapshot, '$.custom404Page'), '') <> '';
