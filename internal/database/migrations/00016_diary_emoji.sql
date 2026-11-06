-- +goose Up
-- NULL means no one has chosen yet, so the organizer may pick one. An empty
-- string means the author removed it, and the organizer leaves it alone.
ALTER TABLE diary_folders ADD COLUMN emoji text;
ALTER TABLE diary_tags ADD COLUMN emoji text;

-- +goose Down
ALTER TABLE diary_tags DROP COLUMN emoji;
ALTER TABLE diary_folders DROP COLUMN emoji;
