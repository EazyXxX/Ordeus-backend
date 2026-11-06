-- +goose Up
-- New defaults. A color someone picked by hand stays as it is.
UPDATE kanban_columns SET color = 'gray' WHERE title = 'Бэклог' AND color = 'purple';
UPDATE kanban_columns SET color = 'blue' WHERE title = 'Нужно сделать' AND color = 'gray';
UPDATE kanban_columns SET color = 'orange' WHERE title = 'В работе' AND color = 'blue';

-- +goose Down
UPDATE kanban_columns SET color = 'blue' WHERE title = 'В работе' AND color = 'orange';
UPDATE kanban_columns SET color = 'gray' WHERE title = 'Нужно сделать' AND color = 'blue';
UPDATE kanban_columns SET color = 'purple' WHERE title = 'Бэклог' AND color = 'gray';
