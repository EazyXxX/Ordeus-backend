-- +goose Up
-- A zen capture can carry pictures. They belong to the capture until the model
-- turns it into a note, and then they move to that note.
ALTER TABLE diary_note_images
    ALTER COLUMN note_id DROP NOT NULL,
    ADD COLUMN capture_id uuid REFERENCES diary_captures (id) ON DELETE CASCADE,
    ADD CONSTRAINT diary_note_images_owner_check CHECK (num_nonnulls(note_id, capture_id) = 1);

CREATE INDEX diary_note_images_capture_idx ON diary_note_images (capture_id) WHERE capture_id IS NOT NULL;

-- +goose Down
DELETE FROM diary_note_images WHERE note_id IS NULL;
DROP INDEX diary_note_images_capture_idx;
ALTER TABLE diary_note_images
    DROP CONSTRAINT diary_note_images_owner_check,
    DROP COLUMN capture_id,
    ALTER COLUMN note_id SET NOT NULL;
