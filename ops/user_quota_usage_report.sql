-- Read-only report for the last 14 complete Beijing dates. Costs use the
-- site's internal USD-equivalent quota, including metered-only requests.
-- A sample is one active user's full-day usage across all channels.
-- Trim exactly one largest and one smallest sample, with deterministic ties.
\set ON_ERROR_STOP on
BEGIN READ ONLY;
SET LOCAL statement_timeout = '20s';
WITH daily AS (
    SELECT l.user_id,
           (to_timestamp(l.created_at) AT TIME ZONE 'Asia/Shanghai')::date AS day,
           SUM(l.quota) / 500000.0 AS usd,
           SUM(CASE WHEN EXTRACT(HOUR FROM to_timestamp(l.created_at) AT TIME ZONE 'Asia/Shanghai') < 17
                    THEN l.quota ELSE 0 END) / 500000.0 AS restricted_usd
    FROM logs l
    JOIN users u ON u.id = l.user_id
    WHERE l.type = 2 AND l.quota > 0
      AND u.role < 10 AND u.status = 1 AND u.deleted_at IS NULL
      AND u."group" IN ('asxs', 'cmsg') AND u.id <> 1
      AND l.created_at >= EXTRACT(EPOCH FROM ((NOW() AT TIME ZONE 'Asia/Shanghai')::date - 14)::timestamp AT TIME ZONE 'Asia/Shanghai')::bigint
      AND l.created_at < EXTRACT(EPOCH FROM (NOW() AT TIME ZONE 'Asia/Shanghai')::date::timestamp AT TIME ZONE 'Asia/Shanghai')::bigint
    GROUP BY l.user_id, day
), ranked AS (
    SELECT *,
           ROW_NUMBER() OVER (ORDER BY usd, day, user_id) AS rank_asc,
           ROW_NUMBER() OVER (ORDER BY usd DESC, day DESC, user_id DESC) AS rank_desc
    FROM daily
)
SELECT MIN(day) AS first_day, MAX(day) AS last_day,
       COUNT(DISTINCT user_id) AS active_users, COUNT(*) AS user_days,
       ROUND(AVG(usd), 4) AS mean_usd,
       ROUND(AVG(usd) FILTER (WHERE rank_asc > 1 AND rank_desc > 1), 4) AS trimmed_mean_usd,
       ROUND(MIN(usd), 4) AS lowest_usd, ROUND(MAX(usd), 4) AS highest_usd,
       ROUND((percentile_cont(0.5) WITHIN GROUP (ORDER BY usd))::numeric, 4) AS median_usd,
       COUNT(*) FILTER (WHERE restricted_usd > 25) AS restricted_days_above_25,
       COUNT(*) FILTER (WHERE restricted_usd > 0) AS restricted_active_days
FROM ranked;
COMMIT;
