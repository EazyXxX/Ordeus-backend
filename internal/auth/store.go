package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	uniqueViolationCode = "23505"
	// Serializes registration, so exactly one account can be the first.
	registrationLockKey = 8_170_001
)

var (
	errEmailTaken = errors.New("email is already registered")
	errNotFound   = errors.New("not found")
)

// ownedTables hold rows that belong to one account.
var ownedTables = []string{
	"kanban_columns", "kanban_cards", "calendar_notes",
	"diary_folders", "diary_tags", "diary_captures", "diary_notes",
}

var defaultColumns = []struct{ title, color string }{
	{"Бэклог", "gray"},
	{"Нужно сделать", "blue"},
	{"В работе", "orange"},
	{"Готово", "green"},
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type Stats struct {
	Notes         int `json:"notes"`
	Tasks         int `json:"tasks"`
	CalendarNotes int `json:"calendarNotes"`
	Folders       int `json:"folders"`
}

type session struct {
	token string
	user  User
}

type store struct {
	pool *pgxpool.Pool
}

// createUser adds an account. The first account also receives every row from
// before accounts existed, so the existing diary and board are not lost.
func (s *store) createUser(ctx context.Context, email, name, passwordHash string) (User, error) {
	user := User{Email: email, Name: name}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, registrationLockKey); err != nil {
			return err
		}
		var first bool
		if err := tx.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM users)`).Scan(&first); err != nil {
			return err
		}

		err := tx.QueryRow(ctx, `
			INSERT INTO users (email, name, password_hash) VALUES ($1, $2, $3)
			RETURNING id::text, color`, email, name, passwordHash).Scan(&user.ID, &user.Color)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			return errEmailTaken
		}
		if err != nil {
			return err
		}

		if first {
			if err := claimExistingData(ctx, tx, user.ID); err != nil {
				return err
			}
		}
		return seedColumns(ctx, tx, user.ID)
	})
	return user, err
}

func claimExistingData(ctx context.Context, tx pgx.Tx, userID string) error {
	for _, table := range ownedTables {
		// Table names come from the fixed list above, never from a request.
		query := fmt.Sprintf(`UPDATE %s SET user_id = $1 WHERE user_id IS NULL`, table)
		if _, err := tx.Exec(ctx, query, userID); err != nil {
			return fmt.Errorf("claim %s: %w", table, err)
		}
	}
	return nil
}

func seedColumns(ctx context.Context, tx pgx.Tx, userID string) error {
	var hasColumns bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM kanban_columns WHERE user_id = $1)`, userID).Scan(&hasColumns)
	if err != nil || hasColumns {
		return err
	}
	for position, column := range defaultColumns {
		if _, err := tx.Exec(ctx, `
			INSERT INTO kanban_columns (user_id, title, color, position) VALUES ($1, $2, $3, $4)`,
			userID, column.title, column.color, position); err != nil {
			return err
		}
	}
	return nil
}

// credentials returns the stored hash for an email.
func (s *store) credentials(ctx context.Context, email string) (User, string, error) {
	var user User
	var hash string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, email, name, color, password_hash FROM users WHERE lower(email) = lower($1)`, email,
	).Scan(&user.ID, &user.Email, &user.Name, &user.Color, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", errNotFound
	}
	return user, hash, err
}

func (s *store) passwordHash(ctx context.Context, userID string) (string, error) {
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash)
	return hash, err
}

func (s *store) createSession(ctx context.Context, userID, token string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hashToken(token), userID, time.Now().Add(sessionLifetime))
	return err
}

// sessions resolves tokens to accounts, keeping the order of tokens and leaving
// out the ones that expired or were revoked.
func (s *store) sessions(ctx context.Context, tokens []string) ([]session, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	hashes := make([][]byte, len(tokens))
	for i, token := range tokens {
		hashes[i] = hashToken(token)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT s.token_hash, u.id::text, u.email, u.name, u.color
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ANY($1) AND s.expires_at > now()`, hashes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	found := make(map[string]User, len(tokens))
	for rows.Next() {
		var hash []byte
		var user User
		if err := rows.Scan(&hash, &user.ID, &user.Email, &user.Name, &user.Color); err != nil {
			return nil, err
		}
		found[string(hash)] = user
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]session, 0, len(tokens))
	for i, token := range tokens {
		if user, ok := found[string(hashes[i])]; ok {
			result = append(result, session{token: token, user: user})
		}
	}
	return result, nil
}

func (s *store) deleteSessions(ctx context.Context, tokens []string) error {
	hashes := make([][]byte, len(tokens))
	for i, token := range tokens {
		hashes[i] = hashToken(token)
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = ANY($1)`, hashes)
	return err
}

func (s *store) deleteExpiredSessions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	return err
}

func (s *store) updateProfile(ctx context.Context, userID, name, color string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET name = $2, color = $3 WHERE id = $1`, userID, name, color)
	return err
}

// changePassword also signs the account out everywhere except this session.
func (s *store) changePassword(ctx context.Context, userID, hash, keepToken string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, hash); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2`, userID, hashToken(keepToken))
		return err
	})
}

func (s *store) stats(ctx context.Context, userID string) (Stats, error) {
	var stats Stats
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM diary_notes WHERE user_id = $1)::int,
			(SELECT count(*) FROM kanban_cards WHERE user_id = $1)::int,
			(SELECT count(*) FROM calendar_notes WHERE user_id = $1)::int,
			(SELECT count(*) FROM diary_folders WHERE user_id = $1)::int`, userID,
	).Scan(&stats.Notes, &stats.Tasks, &stats.CalendarNotes, &stats.Folders)
	return stats, err
}
