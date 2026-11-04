-- +goose Up
ALTER TABLE diary_folders ADD COLUMN color text NOT NULL DEFAULT 'gray';
ALTER TABLE diary_tags ADD COLUMN color text NOT NULL DEFAULT 'gray';

-- Same rotation as new folders get, so existing folders stay easy to tell apart.
WITH ordered AS (
    SELECT id, row_number() OVER (ORDER BY lower(name)) - 1 AS position
    FROM diary_folders
)
UPDATE diary_folders f
SET color = (ARRAY['red', 'orange', 'yellow', 'green', 'blue', 'purple', 'pink'])[o.position % 7 + 1]
FROM ordered o
WHERE f.id = o.id;

-- +goose Down
ALTER TABLE diary_tags DROP COLUMN color;
ALTER TABLE diary_folders DROP COLUMN color;
