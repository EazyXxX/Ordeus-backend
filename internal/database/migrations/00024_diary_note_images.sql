-- +goose Up
-- Pictures attached to notes. The files lie on disk, one folder per account;
-- this table says whose they are and what type they have.
CREATE TABLE diary_note_images (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    note_id      uuid NOT NULL REFERENCES diary_notes (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    content_type text NOT NULL,
    size_bytes   integer NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX diary_note_images_note_idx ON diary_note_images (note_id, id);

-- +goose Down
DROP TABLE diary_note_images;
