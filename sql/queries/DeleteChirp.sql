-- name: DeleteChirp :one
DELETE FROM chirps
WHERE id = $1
RETURNING *;
