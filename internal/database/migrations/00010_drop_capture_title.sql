-- +goose Up
-- The title now comes from the first line of the entry itself.
ALTER TABLE diary_captures DROP COLUMN title;

-- +goose Down
ALTER TABLE diary_captures ADD COLUMN title text NOT NULL DEFAULT '';
