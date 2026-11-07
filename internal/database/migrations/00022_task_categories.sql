-- +goose Up
ALTER TABLE kanban_cards ADD COLUMN category text NOT NULL DEFAULT '';

-- backlog_column_id follows a column by id, so renaming it changes nothing. When
-- it is empty, because the column was deleted, the board's first column stands in.
ALTER TABLE users
    ADD COLUMN task_categories text NOT NULL DEFAULT 'all' CHECK (task_categories IN ('off', 'all', 'backlog')),
    ADD COLUMN backlog_column_id uuid REFERENCES kanban_columns (id) ON DELETE SET NULL;

UPDATE users u
SET backlog_column_id = (
    SELECT c.id FROM kanban_columns c
    WHERE c.user_id = u.id AND lower(c.title) IN ('бэклог', 'backlog')
    ORDER BY c.position, c.id
    LIMIT 1
);

-- +goose Down
ALTER TABLE users DROP COLUMN backlog_column_id, DROP COLUMN task_categories;
ALTER TABLE kanban_cards DROP COLUMN category;
