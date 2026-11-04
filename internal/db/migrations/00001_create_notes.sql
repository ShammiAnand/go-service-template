-- +goose Up
CREATE TABLE notes (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id   text        NOT NULL,
    title      text        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    body       text        NOT NULL DEFAULT '' CHECK (char_length(body) <= 10000),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX notes_owner_created_idx ON notes (owner_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE notes;
