-- name: Ping :one
-- Trivial round-trip used to verify the pool can execute a query end to end.
-- Feature queries (accounts, messages, ...) are added by their own stories.
SELECT 1 AS ok;
