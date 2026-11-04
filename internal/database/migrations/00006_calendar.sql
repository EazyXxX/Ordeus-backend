-- +goose Up
CREATE TABLE calendar_notes (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    date       date NOT NULL,
    start_time time,
    title      text NOT NULL,
    body       text NOT NULL DEFAULT '',
    color      text NOT NULL DEFAULT 'blue',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX calendar_notes_date_idx ON calendar_notes (date);

CREATE TRIGGER calendar_notes_set_updated_at BEFORE UPDATE ON calendar_notes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX kanban_cards_due_date_idx ON kanban_cards (due_date) WHERE due_date IS NOT NULL;

-- +goose Down
DROP INDEX kanban_cards_due_date_idx;
DROP TABLE calendar_notes;
