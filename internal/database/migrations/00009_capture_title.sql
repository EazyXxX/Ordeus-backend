-- +goose Up
-- Empty means the model picks the title.
ALTER TABLE diary_captures ADD COLUMN title text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE diary_captures DROP COLUMN title;
