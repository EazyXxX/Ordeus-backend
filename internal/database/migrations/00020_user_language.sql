-- +goose Up
-- The interface language. Accounts from before this choice existed were in Russian.
ALTER TABLE users ADD COLUMN language text NOT NULL DEFAULT 'ru' CHECK (language IN ('ru', 'en'));

-- +goose Down
ALTER TABLE users DROP COLUMN language;
