-- +goose Up
-- The column whose cards count as done. Like the backlog, it follows a column by
-- id. When it is empty, the board's last column stands in.
ALTER TABLE users ADD COLUMN done_column_id uuid REFERENCES kanban_columns (id) ON DELETE SET NULL;

UPDATE users u
SET done_column_id = COALESCE(
    (SELECT c.id FROM kanban_columns c
     WHERE c.user_id = u.id AND lower(c.title) IN ('готово', 'done')
     ORDER BY c.position, c.id LIMIT 1),
    (SELECT c.id FROM kanban_columns c
     WHERE c.user_id = u.id
     ORDER BY c.position DESC, c.id DESC LIMIT 1)
);

-- When a card reached the done column. Cards already there get the time of
-- their last change, the closest guess there is.
ALTER TABLE kanban_cards ADD COLUMN completed_at timestamptz;

UPDATE kanban_cards k SET completed_at = k.updated_at
FROM users u
WHERE u.id = k.user_id AND k.column_id = u.done_column_id;

CREATE INDEX kanban_cards_user_completed_idx ON kanban_cards (user_id, completed_at);

-- +goose StatementBegin
CREATE FUNCTION kanban_done_column(account uuid) RETURNS uuid AS $$
    SELECT COALESCE(
        (SELECT done_column_id FROM users WHERE id = account),
        (SELECT id FROM kanban_columns WHERE user_id = account ORDER BY position DESC, id DESC LIMIT 1)
    )
$$ LANGUAGE sql STABLE;
-- +goose StatementEnd

-- A trigger, so that every way a card moves keeps the time: dragging, the card
-- dialog, a new card in the done column.
-- +goose StatementBegin
CREATE FUNCTION kanban_cards_set_completed_at() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.column_id = OLD.column_id THEN
        RETURN NEW;
    END IF;
    IF NEW.column_id = kanban_done_column(NEW.user_id) THEN
        NEW.completed_at = now();
    ELSE
        NEW.completed_at = NULL;
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER kanban_cards_set_completed_at BEFORE INSERT OR UPDATE OF column_id ON kanban_cards
    FOR EACH ROW EXECUTE FUNCTION kanban_cards_set_completed_at();

-- A week's statistics as they were when the author asked for them.
CREATE TABLE task_reports (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    week_start date NOT NULL,
    stats      jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, week_start)
);

-- +goose Down
DROP TABLE task_reports;
DROP TRIGGER kanban_cards_set_completed_at ON kanban_cards;
DROP FUNCTION kanban_cards_set_completed_at();
DROP FUNCTION kanban_done_column(uuid);
ALTER TABLE kanban_cards DROP COLUMN completed_at;
ALTER TABLE users DROP COLUMN done_column_id;
