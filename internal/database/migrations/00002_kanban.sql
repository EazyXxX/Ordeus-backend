-- +goose Up
CREATE TABLE kanban_columns (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    title      text NOT NULL,
    position   integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE kanban_cards (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    column_id   uuid NOT NULL REFERENCES kanban_columns (id) ON DELETE CASCADE,
    title       text NOT NULL,
    description text NOT NULL DEFAULT '',
    position    integer NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX kanban_cards_column_position_idx ON kanban_cards (column_id, position);

CREATE TRIGGER kanban_columns_set_updated_at BEFORE UPDATE ON kanban_columns
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER kanban_cards_set_updated_at BEFORE UPDATE ON kanban_cards
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

INSERT INTO kanban_columns (title, position) VALUES
    ('Нужно сделать', 0),
    ('В работе', 1),
    ('Готово', 2);

-- +goose Down
DROP TABLE kanban_cards;
DROP TABLE kanban_columns;
