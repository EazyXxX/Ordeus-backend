-- +goose Up
CREATE TABLE diary_folders (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX diary_folders_name_idx ON diary_folders (lower(name));

CREATE TABLE diary_tags (
    id   uuid PRIMARY KEY DEFAULT uuidv7(),
    name text NOT NULL UNIQUE
);

-- Raw zen-mode text. It is stored before any AI work, so a failed or slow
-- model never loses a thought.
CREATE TABLE diary_captures (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    raw_text     text NOT NULL,
    status       text NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'processing', 'done', 'failed')),
    error        text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz
);
CREATE INDEX diary_captures_status_idx ON diary_captures (status, id);

CREATE TABLE diary_notes (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    folder_id  uuid REFERENCES diary_folders (id) ON DELETE SET NULL,
    capture_id uuid REFERENCES diary_captures (id) ON DELETE SET NULL,
    title      text NOT NULL,
    body       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX diary_notes_folder_idx ON diary_notes (folder_id);

CREATE TRIGGER diary_notes_set_updated_at BEFORE UPDATE ON diary_notes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE diary_note_tags (
    note_id uuid NOT NULL REFERENCES diary_notes (id) ON DELETE CASCADE,
    tag_id  uuid NOT NULL REFERENCES diary_tags (id) ON DELETE CASCADE,
    PRIMARY KEY (note_id, tag_id)
);
CREATE INDEX diary_note_tags_tag_idx ON diary_note_tags (tag_id);

-- Links are undirected, so each pair is stored once with the smaller id first.
CREATE TABLE diary_note_links (
    note_a uuid NOT NULL REFERENCES diary_notes (id) ON DELETE CASCADE,
    note_b uuid NOT NULL REFERENCES diary_notes (id) ON DELETE CASCADE,
    PRIMARY KEY (note_a, note_b),
    CHECK (note_a < note_b)
);
CREATE INDEX diary_note_links_b_idx ON diary_note_links (note_b);

-- +goose Down
DROP TABLE diary_note_links;
DROP TABLE diary_note_tags;
DROP TABLE diary_notes;
DROP TABLE diary_captures;
DROP TABLE diary_tags;
DROP TABLE diary_folders;
