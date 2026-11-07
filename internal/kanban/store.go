package kanban

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNotFound = errors.New("not found")

type ChecklistItem struct {
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

type Card struct {
	ID          string          `json:"id"`
	ColumnID    string          `json:"columnId"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Color       string          `json:"color"`
	Priority    string          `json:"priority"`
	DueDate     *string         `json:"dueDate"`
	Checklist   []ChecklistItem `json:"checklist"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

// CardInput holds the fields a person edits on a card.
type CardInput struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Color       string          `json:"color"`
	Priority    string          `json:"priority"`
	DueDate     *string         `json:"dueDate"`
	Checklist   []ChecklistItem `json:"checklist"`
}

type Column struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Color string `json:"color"`
	Cards []Card `json:"cards"`
}

type Board struct {
	Columns []Column `json:"columns"`
}

type store struct {
	pool *pgxpool.Pool
}

func (s *store) board(ctx context.Context, userID string) (Board, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, title, color FROM kanban_columns WHERE user_id = $1 ORDER BY position, id`, userID)
	if err != nil {
		return Board{}, err
	}
	columns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Column, error) {
		column := Column{Cards: []Card{}}
		return column, row.Scan(&column.ID, &column.Title, &column.Color)
	})
	if err != nil {
		return Board{}, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT id::text, column_id::text, title, description, category, color, priority,
			to_char(due_date, 'YYYY-MM-DD'), checklist, created_at, updated_at
		FROM kanban_cards
		WHERE user_id = $1
		ORDER BY position, id`, userID)
	if err != nil {
		return Board{}, err
	}
	cards, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Card])
	if err != nil {
		return Board{}, err
	}

	indexByID := make(map[string]int, len(columns))
	for i, column := range columns {
		indexByID[column.ID] = i
	}
	for _, card := range cards {
		i := indexByID[card.ColumnID]
		columns[i].Cards = append(columns[i].Cards, card)
	}
	return Board{Columns: columns}, nil
}

func (s *store) createColumn(ctx context.Context, userID, title string) (Column, error) {
	column := Column{Title: title, Cards: []Card{}}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO kanban_columns (user_id, title, position)
		SELECT $1, $2, COALESCE(MAX(position) + 1, 0) FROM kanban_columns WHERE user_id = $1
		RETURNING id::text, color`, userID, title).Scan(&column.ID, &column.Color)
	return column, err
}

// updateColumn changes only the fields that are not nil.
func (s *store) updateColumn(ctx context.Context, userID, id string, title, color *string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE kanban_columns
		SET title = COALESCE($3, title), color = COALESCE($4, color)
		WHERE id = $1 AND user_id = $2`, id, userID, title, color)
	return requireRow(tag.RowsAffected(), err)
}

func (s *store) deleteColumn(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM kanban_columns WHERE id = $1 AND user_id = $2`, id, userID)
	return requireRow(tag.RowsAffected(), err)
}

func (s *store) moveColumn(ctx context.Context, userID, id string, index int) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id::text FROM kanban_columns WHERE user_id = $1 ORDER BY position, id FOR UPDATE`, userID)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		if !slices.Contains(ids, id) {
			return errNotFound
		}
		_, err = tx.Exec(ctx, `
			UPDATE kanban_columns c SET position = o.ordinality
			FROM unnest($1::uuid[]) WITH ORDINALITY o (id, ordinality)
			WHERE c.id = o.id`, moveTo(ids, id, index))
		return err
	})
}

func (s *store) createCard(ctx context.Context, userID, columnID, title string) (Card, error) {
	card := Card{ColumnID: columnID, Title: title, Priority: "none", Checklist: []ChecklistItem{}}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockColumn(ctx, tx, userID, columnID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO kanban_cards (user_id, column_id, title, position)
			SELECT $1, $2, $3, COALESCE(MAX(position) + 1, 0) FROM kanban_cards WHERE column_id = $2
			RETURNING id::text, color, created_at, updated_at`, userID, columnID, title,
		).Scan(&card.ID, &card.Color, &card.CreatedAt, &card.UpdatedAt)
	})
	return card, err
}

// updateCard takes the spelling of a category the account already has, so that
// «дом» and «Дом» do not become two groups.
func (s *store) updateCard(ctx context.Context, userID, id string, input CardInput) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE kanban_cards
		SET title = $3, description = $4, color = $5, priority = $6, due_date = $7::date, checklist = $8,
			category = COALESCE((
				SELECT other.category FROM kanban_cards other
				WHERE other.user_id = $2 AND other.id <> $1 AND lower(other.category) = lower($9)
				LIMIT 1
			), $9)
		WHERE id = $1 AND user_id = $2`,
		id, userID, input.Title, input.Description, input.Color, input.Priority, input.DueDate, input.Checklist,
		input.Category)
	return requireRow(tag.RowsAffected(), err)
}

// categories lists the ones in use, for the model to reuse.
func (s *store) categories(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT category FROM kanban_cards WHERE user_id = $1 AND category <> '' ORDER BY category`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (s *store) deleteCard(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM kanban_cards WHERE id = $1 AND user_id = $2`, id, userID)
	return requireRow(tag.RowsAffected(), err)
}

func (s *store) moveCard(ctx context.Context, userID, id, columnID string, index int) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockColumn(ctx, tx, userID, columnID); err != nil {
			return err
		}
		var exists bool
		err := tx.QueryRow(ctx,
			`SELECT true FROM kanban_cards WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}

		rows, err := tx.Query(ctx, `
			SELECT id::text FROM kanban_cards
			WHERE column_id = $1 ORDER BY position, id`, columnID)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE kanban_cards c SET column_id = $2, position = o.ordinality
			FROM unnest($1::uuid[]) WITH ORDINALITY o (id, ordinality)
			WHERE c.id = o.id`, moveTo(ids, id, index), columnID)
		return err
	})
}

// lockColumn serializes writes to one column's card order and confirms that the
// column belongs to the account.
func lockColumn(ctx context.Context, tx pgx.Tx, userID, id string) error {
	var exists bool
	err := tx.QueryRow(ctx,
		`SELECT true FROM kanban_columns WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("lock column: %w", err)
	}
	return nil
}

// moveTo returns ids with id placed at index, clamped to the list bounds.
// id does not have to be in ids yet: a card can arrive from another column.
func moveTo(ids []string, id string, index int) []string {
	rest := slices.DeleteFunc(slices.Clone(ids), func(other string) bool { return other == id })
	return slices.Insert(rest, min(max(index, 0), len(rest)), id)
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
