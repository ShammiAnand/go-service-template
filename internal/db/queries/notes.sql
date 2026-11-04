-- name: CreateNote :one
INSERT INTO notes (owner_id, title, body)
VALUES (sqlc.arg(owner_id), sqlc.arg(title), sqlc.arg(body))
RETURNING *;

-- name: GetNote :one
SELECT * FROM notes
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: ListNotes :many
SELECT * FROM notes
WHERE owner_id = sqlc.arg(owner_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: UpdateNote :one
UPDATE notes
SET title = sqlc.arg(title), body = sqlc.arg(body), updated_at = now()
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id)
RETURNING *;

-- name: DeleteNote :execrows
DELETE FROM notes
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);
