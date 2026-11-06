-- +goose Up
-- What the author says about a whole day: its mood, its score out of ten and the
-- model's summary. Notes of the day stay in diary_notes.
CREATE TABLE diary_days (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    day            date NOT NULL,
    mood           text NOT NULL DEFAULT '',
    score          int CHECK (score BETWEEN 1 AND 10),
    summary_status text NOT NULL DEFAULT 'none',
    summary_error  text NOT NULL DEFAULT '',
    summary        text NOT NULL DEFAULT '',
    focus          text NOT NULL DEFAULT '',
    summarized_at  timestamptz,
    UNIQUE (user_id, day)
);

CREATE INDEX diary_days_pending_idx ON diary_days (id) WHERE summary_status = 'pending';

-- +goose Down
DROP TABLE diary_days;
