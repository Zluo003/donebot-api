BEGIN READ ONLY;

SELECT u.id AS user_id, u.concurrency,
       COUNT(*) FILTER (WHERE p.state = 'prepaid') AS old_media_slots,
       COUNT(*) FILTER (WHERE p.state = 'prepaid' AND p.settlement_input IS NULL
         AND o.status NOT IN ('completed','failed','cancelled','refunded')
         AND (d.receipt_id IS NULL OR d.state NOT IN ('retrieving','settling','refunding','completed','failed'))) AS execution_slots
FROM users u
LEFT JOIN donebot_media_precharges p ON p.user_id = u.id
LEFT JOIN donebot_media_operations o USING (receipt_id)
LEFT JOIN donebot_media_dispatches d USING (receipt_id)
WHERE u.id = :'user_id'::bigint
GROUP BY u.id, u.concurrency;

SELECT p.receipt_id, p.model, p.created_at, p.updated_at,
       o.status AS operation_status, p.state AS payment_state,
       p.settlement_input IS NOT NULL AS settlement_ready,
       d.state AS dispatch_state, d.due_at, d.updated_at AS dispatch_updated_at,
       d.payload->>'protocol' AS protocol,
       d.payload->>'account_id' AS account_id,
       d.payload->>'provider_task_id' AS provider_task_id,
       d.payload->'diagnostic'->>'code' AS diagnostic_code,
       d.payload->'result' IS NOT NULL AND d.payload->'result' <> 'null'::jsonb AS has_result,
       CASE
         WHEN p.settlement_input IS NOT NULL OR d.state = 'settling' THEN 'resume_settlement'
         WHEN d.state = 'refunding' OR o.status IN ('failed','cancelled') THEN 'resume_refund'
         WHEN d.state = 'retrieving' THEN 'resume_retrieval'
         WHEN d.state IN ('dispatching','uncertain') AND jsonb_typeof(d.payload->'result') = 'object'
           AND (NULLIF(d.payload->>'response','') IS NOT NULL
             OR (d.payload->>'donebot_intent' = 'true' AND jsonb_array_length(
               CASE WHEN jsonb_typeof(d.payload->'artifacts') = 'array' THEN d.payload->'artifacts' ELSE '[]'::jsonb END) > 0))
           THEN 'resume_result'
         WHEN d.state IN ('polling','dispatching','uncertain') AND NULLIF(d.payload->>'provider_task_id','') IS NOT NULL
           AND d.payload->>'protocol' IN ('apimart_images','apimart_music','seedance') THEN 'query_original_task'
         WHEN d.state = 'uncertain' OR o.status = 'uncertain' THEN 'reconciliation_required'
         ELSE 'scheduled'
       END AS recovery_action,
       k.status AS api_key_status, k.deleted_at IS NOT NULL AS api_key_deleted
FROM donebot_media_precharges p
JOIN donebot_media_operations o USING (receipt_id)
LEFT JOIN donebot_media_dispatches d USING (receipt_id)
LEFT JOIN api_keys k ON k.id = p.api_key_id
WHERE p.user_id = :'user_id'::bigint AND p.state = 'prepaid'
ORDER BY p.created_at;

ROLLBACK;
