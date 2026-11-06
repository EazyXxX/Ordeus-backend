-- +goose Up
-- A weekly digest of the diary. Like captures, it waits in this table until the
-- background worker gets to it, so a slow or failed model blocks nothing.
CREATE TABLE diary_summaries (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    week_start date NOT NULL,
    status     text NOT NULL DEFAULT 'pending',
    error      text NOT NULL DEFAULT '',
    headline   text NOT NULL DEFAULT '',
    body       text NOT NULL DEFAULT '',
    highlights text[] NOT NULL DEFAULT '{}',
    themes     text[] NOT NULL DEFAULT '{}',
    focus      text NOT NULL DEFAULT '',
    note_count int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, week_start)
);

CREATE INDEX diary_summaries_pending_idx ON diary_summaries (id) WHERE status = 'pending';

-- +goose Down
DROP TABLE diary_summaries;
