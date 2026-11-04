package calendar

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNotFound = errors.New("not found")

type Note struct {
	ID    string  `json:"id"`
	Date  string  `json:"date"`
	Time  *string `json:"time"`
	Title string  `json:"title"`
	Body  string  `json:"body"`
	Color string  `json:"color"`
}

type NoteInput struct {
	Date  string  `json:"date"`
	Time  *string `json:"time"`
	Title string  `json:"title"`
	Body  string  `json:"body"`
	Color string  `json:"color"`
}

// Task is a kanban card with a due date. The calendar only reads these.
type Task struct {
	ID          string `json:"id"`
	Date        string `json:"date"`
	Title       string `json:"title"`
	Color       string `json:"color"`
	Priority    string `json:"priority"`
	ColumnTitle string `json:"columnTitle"`
	ColumnColor string `json:"columnColor"`
}

type Range struct {
	Notes []Note `json:"notes"`
	Tasks []Task `json:"tasks"`
}

type store struct {
	pool *pgxpool.Pool
}

func (s *store) inRange(ctx context.Context, from, to string) (Range, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, to_char(date, 'YYYY-MM-DD'), to_char(start_time, 'HH24:MI'), title, body, color
		FROM calendar_notes
		WHERE date BETWEEN $1::date AND $2::date
		ORDER BY date, start_time NULLS LAST, id`, from, to)
	if err != nil {
		return Range{}, err
	}
	notes, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Note])
	if err != nil {
		return Range{}, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT c.id::text, to_char(c.due_date, 'YYYY-MM-DD'), c.title, c.color, c.priority, k.title, k.color
		FROM kanban_cards c
		JOIN kanban_columns k ON k.id = c.column_id
		WHERE c.due_date BETWEEN $1::date AND $2::date
		ORDER BY c.due_date, k.position, c.position, c.id`, from, to)
	if err != nil {
		return Range{}, err
	}
	tasks, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Task])
	if err != nil {
		return Range{}, err
	}
	return Range{Notes: notes, Tasks: tasks}, nil
}

func (s *store) createNote(ctx context.Context, input NoteInput) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO calendar_notes (date, start_time, title, body, color)
		VALUES ($1::date, $2::time, $3, $4, $5)
		RETURNING id::text`, input.Date, input.Time, input.Title, input.Body, input.Color).Scan(&id)
	return id, err
}

func (s *store) updateNote(ctx context.Context, id string, input NoteInput) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE calendar_notes
		SET date = $2::date, start_time = $3::time, title = $4, body = $5, color = $6
		WHERE id = $1`, id, input.Date, input.Time, input.Title, input.Body, input.Color)
	return requireRow(tag.RowsAffected(), err)
}

func (s *store) deleteNote(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM calendar_notes WHERE id = $1`, id)
	return requireRow(tag.RowsAffected(), err)
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
