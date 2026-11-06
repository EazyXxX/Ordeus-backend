-- +goose Up
-- A daily note documents one day of the author's life. The date is the day it
-- describes, which can differ from the day it was written.
ALTER TABLE diary_notes ADD COLUMN daily_date date;
CREATE INDEX diary_notes_daily_idx ON diary_notes (user_id, daily_date DESC) WHERE daily_date IS NOT NULL;

-- The author's date when writing, which anchors words like "yesterday".
ALTER TABLE diary_captures ADD COLUMN written_on date NOT NULL DEFAULT CURRENT_DATE;
ALTER TABLE diary_captures ALTER COLUMN written_on DROP DEFAULT;

-- +goose Down
ALTER TABLE diary_captures DROP COLUMN written_on;
DROP INDEX diary_notes_daily_idx;
ALTER TABLE diary_notes DROP COLUMN daily_date;
