package diary

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	listLimit           = 500
	contextNotesLimit   = 50
	uniqueViolationCode = "23505"
)

var (
	errNotFound = errors.New("not found")
	errConflict = errors.New("conflict")
)

type NoteRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type Note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Folder    string    `json:"folder"`
	Tags      []string  `json:"tags"`
	Links     []NoteRef `json:"links"`
	FromAI    bool      `json:"fromAi"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Folder struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Notes int    `json:"notes"`
}

type Tag struct {
	Name  string `json:"name"`
	Notes int    `json:"notes"`
}

type Capture struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"createdAt"`
}

type NoteFilter struct {
	FolderID string
	Tag      string
	Query    string
}

type NoteInput struct {
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	Folder  string   `json:"folder"`
	Tags    []string `json:"tags"`
	LinkIDs []string `json:"linkIds"`
}

type store struct {
	pool *pgxpool.Pool
}

func (s *store) notes(ctx context.Context, filter NoteFilter) ([]Note, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT n.id::text, n.title, n.body, COALESCE(f.name, ''),
			COALESCE((
				SELECT array_agg(t.name ORDER BY t.name)
				FROM diary_note_tags nt JOIN diary_tags t ON t.id = nt.tag_id
				WHERE nt.note_id = n.id
			), '{}'::text[]),
			COALESCE((
				SELECT jsonb_agg(jsonb_build_object('id', other.id, 'title', other.title) ORDER BY other.title)
				FROM diary_note_links l
				JOIN diary_notes other
					ON other.id = CASE WHEN l.note_a = n.id THEN l.note_b ELSE l.note_a END
				WHERE n.id IN (l.note_a, l.note_b)
			), '[]'::jsonb),
			n.capture_id IS NOT NULL, n.created_at, n.updated_at
		FROM diary_notes n
		LEFT JOIN diary_folders f ON f.id = n.folder_id
		WHERE (NULLIF($1, '') IS NULL OR n.folder_id = NULLIF($1, '')::uuid)
			AND ($2 = '' OR EXISTS (
				SELECT 1 FROM diary_note_tags nt JOIN diary_tags t ON t.id = nt.tag_id
				WHERE nt.note_id = n.id AND t.name = $2
			))
			AND ($3 = '' OR strpos(lower(n.title || ' ' || n.body), lower($3)) > 0)
		ORDER BY n.id DESC
		LIMIT $4`, filter.FolderID, filter.Tag, filter.Query, listLimit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Note, error) {
		var note Note
		err := row.Scan(&note.ID, &note.Title, &note.Body, &note.Folder, &note.Tags, &note.Links,
			&note.FromAI, &note.CreatedAt, &note.UpdatedAt)
		return note, err
	})
}

func (s *store) folders(ctx context.Context) ([]Folder, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.id::text, f.name, count(n.id)::int
		FROM diary_folders f
		LEFT JOIN diary_notes n ON n.folder_id = f.id
		GROUP BY f.id
		ORDER BY lower(f.name)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Folder])
}

func (s *store) tags(ctx context.Context) ([]Tag, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.name, count(*)::int
		FROM diary_tags t
		JOIN diary_note_tags nt ON nt.tag_id = t.id
		GROUP BY t.id
		ORDER BY count(*) DESC, t.name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Tag])
}

func (s *store) createNote(ctx context.Context, input NoteInput) (string, error) {
	var id string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		folderID, err := ensureFolder(ctx, tx, input.Folder)
		if err != nil {
			return err
		}
		id, err = insertNote(ctx, tx, input.Title, input.Body, folderID, nil)
		if err != nil {
			return err
		}
		if err := addTags(ctx, tx, id, input.Tags); err != nil {
			return err
		}
		return addLinks(ctx, tx, id, input.LinkIDs)
	})
	return id, err
}

func (s *store) updateNote(ctx context.Context, id string, input NoteInput) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		folderID, err := ensureFolder(ctx, tx, input.Folder)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE diary_notes SET title = $2, body = $3, folder_id = $4 WHERE id = $1`,
			id, input.Title, input.Body, folderID)
		if err := requireRow(tag.RowsAffected(), err); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM diary_note_tags WHERE note_id = $1`, id); err != nil {
			return err
		}
		if err := addTags(ctx, tx, id, input.Tags); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM diary_note_links WHERE note_a = $1 OR note_b = $1`, id); err != nil {
			return err
		}
		return addLinks(ctx, tx, id, input.LinkIDs)
	})
}

func (s *store) deleteNote(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM diary_notes WHERE id = $1`, id)
	return requireRow(tag.RowsAffected(), err)
}

func (s *store) renameFolder(ctx context.Context, id, name string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE diary_folders SET name = $2 WHERE id = $1`, id, name)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return errConflict
	}
	return requireRow(tag.RowsAffected(), err)
}

// deleteFolder keeps the notes: they simply lose their folder.
func (s *store) deleteFolder(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM diary_folders WHERE id = $1`, id)
	return requireRow(tag.RowsAffected(), err)
}

func (s *store) createCapture(ctx context.Context, text string) (Capture, error) {
	capture := Capture{Text: text, Status: statusPending}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO diary_captures (raw_text) VALUES ($1) RETURNING id::text, created_at`, text,
	).Scan(&capture.ID, &capture.CreatedAt)
	return capture, err
}

// unfinishedCaptures returns everything not yet turned into notes.
func (s *store) unfinishedCaptures(ctx context.Context) ([]Capture, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, raw_text, status, error, created_at
		FROM diary_captures
		WHERE status <> 'done'
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Capture])
}

func (s *store) retryCapture(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE diary_captures SET status = 'pending', error = ''
		WHERE id = $1 AND status = 'failed'`, id)
	return requireRow(tag.RowsAffected(), err)
}

func (s *store) deleteFailedCapture(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM diary_captures WHERE id = $1 AND status = 'failed'`, id)
	return requireRow(tag.RowsAffected(), err)
}

// requeueInterrupted returns captures left mid-flight by a stopped server.
func (s *store) requeueInterrupted(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE diary_captures SET status = 'pending' WHERE status = 'processing'`)
	return err
}

func (s *store) claimCapture(ctx context.Context) (Capture, error) {
	var capture Capture
	err := s.pool.QueryRow(ctx, `
		UPDATE diary_captures SET status = 'processing'
		WHERE id = (
			SELECT id FROM diary_captures WHERE status = 'pending'
			ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		RETURNING id::text, raw_text`).Scan(&capture.ID, &capture.Text)
	if errors.Is(err, pgx.ErrNoRows) {
		return Capture{}, errNotFound
	}
	return capture, err
}

func (s *store) failCapture(ctx context.Context, id, message string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE diary_captures SET status = 'failed', error = $2 WHERE id = $1`, id, message)
	return err
}

func (s *store) organizerContext(ctx context.Context) (organizerContext, error) {
	var known organizerContext
	rows, err := s.pool.Query(ctx, `SELECT name FROM diary_folders ORDER BY lower(name)`)
	if err != nil {
		return known, err
	}
	if known.Folders, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return known, err
	}
	rows, err = s.pool.Query(ctx, `
		SELECT DISTINCT t.name
		FROM diary_tags t JOIN diary_note_tags nt ON nt.tag_id = t.id
		ORDER BY t.name`)
	if err != nil {
		return known, err
	}
	if known.Tags, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return known, err
	}
	rows, err = s.pool.Query(ctx,
		`SELECT id::text, title FROM diary_notes ORDER BY id DESC LIMIT $1`, contextNotesLimit)
	if err != nil {
		return known, err
	}
	known.Notes, err = pgx.CollectRows(rows, pgx.RowToStructByPos[NoteRef])
	return known, err
}

// applyPlan stores a whole plan atomically and finishes the capture.
func (s *store) applyPlan(ctx context.Context, captureID string, result plan, known []NoteRef) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		ids := make([]string, len(result.Notes))
		for i, note := range result.Notes {
			folderID, err := ensureFolder(ctx, tx, note.Folder)
			if err != nil {
				return err
			}
			if ids[i], err = insertNote(ctx, tx, note.Title, note.Body, folderID, &captureID); err != nil {
				return err
			}
			if err := addTags(ctx, tx, ids[i], note.Tags); err != nil {
				return err
			}
		}
		for i, note := range result.Notes {
			links := make([]string, 0, len(note.RelatedExisting)+len(note.RelatedNew))
			for _, number := range note.RelatedExisting {
				links = append(links, known[number-1].ID)
			}
			for _, index := range note.RelatedNew {
				links = append(links, ids[index])
			}
			if err := addLinks(ctx, tx, ids[i], links); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx,
			`UPDATE diary_captures SET status = 'done', error = '', processed_at = now() WHERE id = $1`, captureID)
		return err
	})
}

func ensureFolder(ctx context.Context, tx pgx.Tx, name string) (*string, error) {
	if name == "" {
		return nil, nil
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO diary_folders (name) VALUES ($1)
		ON CONFLICT (lower(name)) DO UPDATE SET name = diary_folders.name
		RETURNING id::text`, name).Scan(&id)
	return &id, err
}

func insertNote(ctx context.Context, tx pgx.Tx, title, body string, folderID, captureID *string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO diary_notes (title, body, folder_id, capture_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text`, title, body, folderID, captureID).Scan(&id)
	return id, err
}

func addTags(ctx context.Context, tx pgx.Tx, noteID string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO diary_tags (name) SELECT unnest($1::text[])
		ON CONFLICT (name) DO NOTHING`, tags); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO diary_note_tags (note_id, tag_id)
		SELECT $1, id FROM diary_tags WHERE name = ANY($2::text[])
		ON CONFLICT DO NOTHING`, noteID, tags)
	return err
}

// addLinks ignores ids of notes that no longer exist and self-links.
func addLinks(ctx context.Context, tx pgx.Tx, noteID string, others []string) error {
	if len(others) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO diary_note_links (note_a, note_b)
		SELECT LEAST($1::uuid, n.id), GREATEST($1::uuid, n.id)
		FROM diary_notes n
		WHERE n.id = ANY($2::uuid[]) AND n.id <> $1::uuid
		ON CONFLICT DO NOTHING`, noteID, others)
	return err
}

func requireRow(affected int64, err error) error {
	if err != nil {
		return err
	}
	if affected == 0 {
		return errNotFound
	}
	return nil
}
