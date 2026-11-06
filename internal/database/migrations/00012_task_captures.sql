-- +goose Up
-- Raw zen-mode text for the task board. As with the diary, it is stored before
-- any AI work, so a failed or slow model never loses what was written.
CREATE TABLE task_captures (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    raw_text     text NOT NULL,
    -- The author's date when writing, which anchors words like "tomorrow".
    written_on   date NOT NULL,
    status       text NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'processing', 'done', 'failed')),
    error        text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz
);
CREATE INDEX task_captures_status_idx ON task_captures (status, id);
CREATE INDEX task_captures_user_idx ON task_captures (user_id);

-- +goose Down
DROP TABLE task_captures;
