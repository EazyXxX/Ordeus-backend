// Package auth owns accounts, sessions and the personal cabinet. Requests carry
// the account in their context, and every module reads it with UserID.
package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"ordeus/backend/internal/httpjson"
)

const (
	minPasswordLength = 8
	// bcrypt only looks at the first 72 bytes.
	maxPasswordBytes = 72
	maxNameLength    = 100
	maxEmailLength   = 254
)

var colors = []string{"gray", "red", "orange", "yellow", "green", "blue", "purple", "pink"}

type contextKey struct{}

type requestAccount struct {
	user  User
	token string
}

// UserID returns the signed-in account. Require guarantees it is set.
func UserID(ctx context.Context) string {
	account, _ := ctx.Value(contextKey{}).(requestAccount)
	return account.user.ID
}

// dummyHash lets a login for an unknown email take as long as a real one, so
// response time does not reveal which emails are registered.
var dummyHash = sync.OnceValue(func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("ordeus"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
})

type Service struct {
	store         *store
	secureCookies bool
}

func New(pool *pgxpool.Pool, secureCookies bool) *Service {
	return &Service{store: &store{pool: pool}, secureCookies: secureCookies}
}

// CleanExpired removes sessions that can no longer be used.
func (s *Service) CleanExpired(ctx context.Context) error {
	return s.store.deleteExpiredSessions(ctx)
}

// RegisterPublic adds the routes that work without a session.
func (s *Service) RegisterPublic(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("GET /api/auth/session", s.session)
	mux.HandleFunc("POST /api/auth/switch", s.switchAccount)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
}

// RegisterProtected adds the personal cabinet routes.
func (s *Service) RegisterProtected(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /api/account", s.updateProfile)
	mux.HandleFunc("POST /api/account/password", s.changePassword)
	mux.HandleFunc("GET /api/account/stats", s.stats)
}

// Require rejects requests without a valid session.
func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions, err := s.store.sessions(r.Context(), tokensFrom(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		if len(sessions) == 0 {
			httpjson.Error(w, http.StatusUnauthorized, "not signed in")
			return
		}
		active := sessions[0]
		account := requestAccount{user: active.user, token: active.token}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, account)))
	})
}

type accountsResponse struct {
	User     User   `json:"user"`
	Accounts []User `json:"accounts"`
}

func toResponse(sessions []session) accountsResponse {
	accounts := make([]User, len(sessions))
	for i, item := range sessions {
		accounts[i] = item.user
	}
	return accountsResponse{User: accounts[0], Accounts: accounts}
}

func (s *Service) session(w http.ResponseWriter, r *http.Request) {
	tokens := tokensFrom(r)
	sessions, err := s.store.sessions(r.Context(), tokens)
	if err != nil {
		fail(w, r, err)
		return
	}
	if len(sessions) != len(tokens) {
		s.writeTokens(w, tokensOf(sessions))
	}
	if len(sessions) == 0 {
		httpjson.Error(w, http.StatusUnauthorized, "not signed in")
		return
	}
	httpjson.Write(w, http.StatusOK, toResponse(sessions))
}

func tokensOf(sessions []session) []string {
	tokens := make([]string, len(sessions))
	for i, item := range sessions {
		tokens[i] = item.token
	}
	return tokens
}

func (s *Service) register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !httpjson.Bind(w, r, &input) {
		return
	}
	input.Email = normalizeEmail(input.Email)
	input.Name = strings.TrimSpace(input.Name)

	if problem := registrationProblem(input.Email, input.Name, input.Password); problem != "" {
		httpjson.Error(w, http.StatusBadRequest, problem)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(w, r, err)
		return
	}

	user, err := s.store.createUser(r.Context(), input.Email, input.Name, string(hash))
	if errors.Is(err, errEmailTaken) {
		httpjson.Error(w, http.StatusConflict, "email is already registered")
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	s.signIn(w, r, user)
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !httpjson.Bind(w, r, &input) {
		return
	}

	user, hash, err := s.store.credentials(r.Context(), normalizeEmail(input.Email))
	if errors.Is(err, errNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(input.Password))
		httpjson.Error(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) != nil {
		httpjson.Error(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	s.signIn(w, r, user)
}

// signIn makes the account active on this device, next to any account that is
// already signed in here.
func (s *Service) signIn(w http.ResponseWriter, r *http.Request, user User) {
	existing, err := s.store.sessions(r.Context(), tokensFrom(r))
	if err != nil {
		fail(w, r, err)
		return
	}

	// Signing in again replaces this account's old session on the device.
	var replaced []string
	kept := make([]session, 0, len(existing))
	for _, item := range existing {
		if item.user.ID == user.ID {
			replaced = append(replaced, item.token)
		} else {
			kept = append(kept, item)
		}
	}
	if len(kept) >= maxDeviceAccounts {
		httpjson.Error(w, http.StatusConflict, "too many accounts on this device")
		return
	}

	token, err := newToken()
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.store.createSession(r.Context(), user.ID, token); err != nil {
		fail(w, r, err)
		return
	}
	if len(replaced) > 0 {
		if err := s.store.deleteSessions(r.Context(), replaced); err != nil {
			fail(w, r, err)
			return
		}
	}

	sessions := append([]session{{token: token, user: user}}, kept...)
	s.writeTokens(w, tokensOf(sessions))
	httpjson.Write(w, http.StatusOK, toResponse(sessions))
}

func (s *Service) switchAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID string `json:"userId"`
	}
	if !httpjson.Bind(w, r, &input) {
		return
	}
	sessions, err := s.store.sessions(r.Context(), tokensFrom(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	// Only accounts already signed in on this device can be switched to.
	index := slices.IndexFunc(sessions, func(item session) bool { return item.user.ID == input.UserID })
	if index < 0 {
		httpjson.Error(w, http.StatusNotFound, "account is not signed in on this device")
		return
	}

	target := sessions[index]
	sessions = append([]session{target}, slices.Delete(sessions, index, index+1)...)
	s.writeTokens(w, tokensOf(sessions))
	httpjson.Write(w, http.StatusOK, toResponse(sessions))
}

// logout signs out of the active account, and the next one becomes active.
func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.sessions(r.Context(), tokensFrom(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	if len(sessions) == 0 {
		s.writeTokens(w, nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.deleteSessions(r.Context(), []string{sessions[0].token}); err != nil {
		fail(w, r, err)
		return
	}

	remaining := sessions[1:]
	s.writeTokens(w, tokensOf(remaining))
	if len(remaining) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpjson.Write(w, http.StatusOK, toResponse(remaining))
}

func (s *Service) updateProfile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !httpjson.Bind(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if problem := nameProblem(input.Name); problem != "" {
		httpjson.Error(w, http.StatusBadRequest, problem)
		return
	}
	if !slices.Contains(colors, input.Color) {
		httpjson.Error(w, http.StatusBadRequest, "unknown color")
		return
	}
	if err := s.store.updateProfile(r.Context(), UserID(r.Context()), input.Name, input.Color); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if !httpjson.Bind(w, r, &input) {
		return
	}
	if problem := passwordProblem(input.Next); problem != "" {
		httpjson.Error(w, http.StatusBadRequest, problem)
		return
	}

	account, _ := r.Context().Value(contextKey{}).(requestAccount)
	stored, err := s.store.passwordHash(r.Context(), account.user.ID)
	if err != nil {
		fail(w, r, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(stored), []byte(input.Current)) != nil {
		httpjson.Error(w, http.StatusForbidden, "wrong current password")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Next), bcrypt.DefaultCost)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.store.changePassword(r.Context(), account.user.ID, string(hash), account.token); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) stats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.stats(r.Context(), UserID(r.Context()))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusOK, stats)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func registrationProblem(email, name, password string) string {
	if len(email) > maxEmailLength {
		return "invalid email"
	}
	if address, err := mail.ParseAddress(email); err != nil || address.Address != email {
		return "invalid email"
	}
	if problem := nameProblem(name); problem != "" {
		return problem
	}
	return passwordProblem(password)
}

func nameProblem(name string) string {
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return "name must be 1 to 100 characters long"
	}
	return ""
}

func passwordProblem(password string) string {
	if utf8.RuneCountInString(password) < minPasswordLength {
		return "password is too short"
	}
	if len(password) > maxPasswordBytes {
		return "password is too long"
	}
	return ""
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("auth request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	httpjson.Error(w, http.StatusInternalServerError, "internal error")
}
