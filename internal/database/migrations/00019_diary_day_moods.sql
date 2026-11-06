-- +goose Up
-- A day can hold several feelings at once.
ALTER TABLE diary_days ADD COLUMN moods text[] NOT NULL DEFAULT '{}';
UPDATE diary_days SET moods = ARRAY[mood] WHERE mood <> '';
ALTER TABLE diary_days DROP COLUMN mood;

-- +goose Down
ALTER TABLE diary_days ADD COLUMN mood text NOT NULL DEFAULT '';
UPDATE diary_days SET mood = moods[1] WHERE cardinality(moods) > 0;
ALTER TABLE diary_days DROP COLUMN moods;
