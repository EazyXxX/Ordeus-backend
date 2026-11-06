-- +goose Up
-- Every card has a color now, so cards without one become gray.
ALTER TABLE kanban_cards ALTER COLUMN color SET DEFAULT 'gray';
UPDATE kanban_cards SET color = 'gray' WHERE color = '';

-- +goose Down
-- Cards that were colorless stay gray: they cannot be told from hand-picked ones.
ALTER TABLE kanban_cards ALTER COLUMN color SET DEFAULT '';
