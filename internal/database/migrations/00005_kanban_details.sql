-- +goose Up
ALTER TABLE kanban_columns
    ADD COLUMN color text NOT NULL DEFAULT 'gray';

ALTER TABLE kanban_cards
    ADD COLUMN color     text NOT NULL DEFAULT '',
    ADD COLUMN priority  text NOT NULL DEFAULT 'none'
        CHECK (priority IN ('none', 'low', 'medium', 'high', 'urgent')),
    ADD COLUMN due_date  date,
    ADD COLUMN checklist jsonb NOT NULL DEFAULT '[]';

UPDATE kanban_columns SET color = 'blue' WHERE title = 'В работе';
UPDATE kanban_columns SET color = 'green' WHERE title = 'Готово';

-- +goose Down
ALTER TABLE kanban_cards
    DROP COLUMN color,
    DROP COLUMN priority,
    DROP COLUMN due_date,
    DROP COLUMN checklist;
ALTER TABLE kanban_columns DROP COLUMN color;
