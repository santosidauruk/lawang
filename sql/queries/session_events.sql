-- name: AppendSessionEvent :one
INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
VALUES ($1, $2, $3, $4)
RETURNING id, session_id, event_type, metadata, occurred_at;

-- name: ListSessionEvents :many
SELECT id, session_id, event_type, metadata, occurred_at
FROM session_events
WHERE session_id = $1
ORDER BY occurred_at, id;
