-- name: GetUserByID :one
SELECT id, created_at, updated_at, email
FROM users
WHERE id = $1;
