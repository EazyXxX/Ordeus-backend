-- +goose Up
-- A place for rough task drafts, on the left of every board that lacks one.
UPDATE kanban_columns c
SET position = position + 1
WHERE NOT EXISTS (
    SELECT 1 FROM kanban_columns b
    WHERE b.user_id = c.user_id AND lower(b.title) IN ('бэклог', 'backlog')
);

INSERT INTO kanban_columns (user_id, title, color, position)
SELECT u.id, 'Бэклог', 'purple', 0
FROM users u
WHERE NOT EXISTS (
    SELECT 1 FROM kanban_columns b
    WHERE b.user_id = u.id AND lower(b.title) IN ('бэклог', 'backlog')
);

-- +goose Down
DELETE FROM kanban_columns c
WHERE c.title = 'Бэклог' AND NOT EXISTS (SELECT 1 FROM kanban_cards k WHERE k.column_id = c.id);
