-- +goose Up
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    email         text NOT NULL,
    name          text NOT NULL,
    color         text NOT NULL DEFAULT 'blue',
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_idx ON users (lower(email));

CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Only a hash is stored, so a leaked database does not expose live sessions.
CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- Rows from before accounts existed keep a NULL owner until the first
-- registered account claims them. Queries always filter by owner, so they stay
-- hidden until then.
ALTER TABLE kanban_columns ADD COLUMN user_id uuid REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE kanban_cards ADD COLUMN user_id uuid REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE calendar_notes ADD COLUMN user_id uuid REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE diary_folders ADD COLUMN user_id uuid REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE diary_tags ADD COLUMN user_id uuid REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE diary_captures ADD COLUMN user_id uuid REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE diary_notes ADD COLUMN user_id uuid REFERENCES users (id) ON DELETE CASCADE;

CREATE INDEX kanban_columns_user_idx ON kanban_columns (user_id);
CREATE INDEX kanban_cards_user_idx ON kanban_cards (user_id);
CREATE INDEX calendar_notes_user_date_idx ON calendar_notes (user_id, date);
CREATE INDEX diary_captures_user_idx ON diary_captures (user_id);
CREATE INDEX diary_notes_user_idx ON diary_notes (user_id);

-- Names are unique within one account, not across the whole app.
DROP INDEX diary_folders_name_idx;
CREATE UNIQUE INDEX diary_folders_user_name_idx ON diary_folders (user_id, lower(name));
ALTER TABLE diary_tags DROP CONSTRAINT diary_tags_name_key;
CREATE UNIQUE INDEX diary_tags_user_name_idx ON diary_tags (user_id, name);

-- +goose Down
DROP INDEX diary_tags_user_name_idx;
ALTER TABLE diary_tags ADD CONSTRAINT diary_tags_name_key UNIQUE (name);
DROP INDEX diary_folders_user_name_idx;
CREATE UNIQUE INDEX diary_folders_name_idx ON diary_folders (lower(name));

ALTER TABLE diary_notes DROP COLUMN user_id;
ALTER TABLE diary_captures DROP COLUMN user_id;
ALTER TABLE diary_tags DROP COLUMN user_id;
ALTER TABLE diary_folders DROP COLUMN user_id;
ALTER TABLE calendar_notes DROP COLUMN user_id;
ALTER TABLE kanban_cards DROP COLUMN user_id;
ALTER TABLE kanban_columns DROP COLUMN user_id;

DROP TABLE sessions;
DROP TABLE users;
