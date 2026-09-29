-- CMSG model plaza: retire four obsolete GPT names and publish Moonshot's four live models.
-- Run once against the production PostgreSQL database before restarting new-api.
BEGIN;

DO $$
BEGIN
  IF (SELECT count(*) FROM models) <> 0 THEN
    RAISE EXCEPTION 'Model metadata changed; review before applying';
  END IF;
  IF (SELECT count(*) FROM channels WHERE id = 33 AND status = 1
      AND models = 'gpt-5.5,gpt-5.6-sol,gpt-5.6-terra,gpt-5.6-luna') <> 1 THEN
    RAISE EXCEPTION 'Kimi channel 33 changed; review before applying';
  END IF;
  IF (SELECT count(*) FROM channels WHERE id IN (1,9,12,26,27,28,30,33)) <> 8 THEN
    RAISE EXCEPTION 'Expected channel IDs changed; review before applying';
  END IF;
END $$;

UPDATE channels AS c
SET models = (
  SELECT string_agg(m.model, ',' ORDER BY m.ordinality)
  FROM unnest(string_to_array(c.models, ',')) WITH ORDINALITY AS m(model, ordinality)
  WHERE m.model <> ALL (ARRAY['gpt-5.3-codex','gpt-5.3-codex-spark','gpt-5.4','gpt-5.4-mini'])
    AND NOT (c.id = 26 AND m.model IN (
      'gpt-5.3-codex-spark-openai-compact','gpt-5.4-openai-compact',
      'gpt-5.4-mini-openai-compact','gpt-5.2-openai-compact'
    ))
    AND NOT (c.id = 28 AND m.model IN ('gpt-5.6-sol','gpt-5.6-terra','gpt-5.6-luna'))
)
WHERE id IN (1,9,12,26,27,28,30);

UPDATE channels AS c
SET model_mapping = (
  SELECT COALESCE(jsonb_object_agg(e.key, e.value), '{}'::jsonb)::text
  FROM jsonb_each_text(COALESCE(NULLIF(c.model_mapping, ''), '{}')::jsonb) AS e(key, value)
  WHERE e.key <> ALL (ARRAY['gpt-5.3-codex','gpt-5.3-codex-spark','gpt-5.4','gpt-5.4-mini'])
    AND e.value <> ALL (ARRAY['gpt-5.3-codex','gpt-5.3-codex-spark','gpt-5.4','gpt-5.4-mini'])
    AND NOT (c.id = 26 AND e.key IN ('gpt-5.2','gpt-5.2-openai-compact'))
)
WHERE id IN (26,28,30);

DELETE FROM abilities
WHERE model = ANY (ARRAY[
  'gpt-5.3-codex','gpt-5.3-codex-spark','gpt-5.4','gpt-5.4-mini',
  'gpt-5.3-codex-spark-openai-compact','gpt-5.4-openai-compact',
  'gpt-5.4-mini-openai-compact'
])
   OR (channel_id = 26 AND model = 'gpt-5.2-openai-compact')
   OR (channel_id = 28 AND model IN ('gpt-5.6-sol','gpt-5.6-terra','gpt-5.6-luna'));

UPDATE channels
SET models = models || ',kimi-k3,kimi-k2.6,kimi-k2.7-code,kimi-k2.7-code-highspeed'
WHERE id = 33;

INSERT INTO abilities ("group", model, channel_id, enabled, priority, weight, tag)
SELECT a."group", m.model, a.channel_id, a.enabled, a.priority, a.weight, a.tag
FROM abilities AS a
CROSS JOIN (
  VALUES ('kimi-k3'),('kimi-k2.6'),('kimi-k2.7-code'),('kimi-k2.7-code-highspeed')
) AS m(model)
WHERE a.channel_id = 33 AND a.model = 'gpt-5.5'
ON CONFLICT DO NOTHING;

INSERT INTO models
  (model_name, status, sync_official, name_rule, created_time, updated_time)
SELECT name, 0, 0, 0, extract(epoch FROM now())::bigint, extract(epoch FROM now())::bigint
FROM unnest(ARRAY['gpt-5.3-codex','gpt-5.3-codex-spark','gpt-5.4','gpt-5.4-mini']) AS name;

INSERT INTO models
  (model_name, vendor_id, endpoints, status, sync_official, name_rule, created_time, updated_time)
SELECT name, 6,
       '{"openai":"/v1/chat/completions","openai-response":"/v1/responses"}',
       1, 0, 0, extract(epoch FROM now())::bigint, extract(epoch FROM now())::bigint
FROM unnest(ARRAY['kimi-k3','kimi-k2.6','kimi-k2.7-code','kimi-k2.7-code-highspeed']) AS name;

DO $$
BEGIN
  IF (SELECT count(*) FROM abilities WHERE channel_id = 33 AND model LIKE 'kimi-%') <> 8 THEN
    RAISE EXCEPTION 'Kimi abilities were not created';
  END IF;
  IF EXISTS (SELECT 1 FROM abilities WHERE model = ANY (ARRAY['gpt-5.3-codex','gpt-5.3-codex-spark','gpt-5.4','gpt-5.4-mini'])) THEN
    RAISE EXCEPTION 'Retired model ability remains';
  END IF;
  IF (SELECT count(*) FROM models WHERE status = 0) <> 4
      OR (SELECT count(*) FROM models WHERE status = 1 AND model_name LIKE 'kimi-%') <> 4 THEN
    RAISE EXCEPTION 'Model metadata count is wrong';
  END IF;
END $$;

COMMIT;
