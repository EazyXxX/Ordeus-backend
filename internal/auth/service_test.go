package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTokensFromIgnoresForeignValues(t *testing.T) {
	token, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newToken()
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: strings.Join([]string{token, "bad", token, second, "zz"}, tokenSeparator),
	})

	got := tokensFrom(request)
	if len(got) != 2 || got[0] != token || got[1] != second {
		t.Errorf("tokens = %v", got)
	}
}

func TestHashTokenIsNotTheToken(t *testing.T) {
	if string(hashToken("abc")) == "abc" || len(hashToken("abc")) != 32 {
		t.Error("unexpected hash")
	}
}

func TestRegistrationProblem(t *testing.T) {
	tests := []struct {
		name, email, userName, password, want string
	}{
		{"valid", "a@example.com", "Анна", "длинный пароль", ""},
		{"bad email", "nope", "Анна", "password1", "invalid email"},
		{"email with display name", "Анна <a@example.com>", "Анна", "password1", "invalid email"},
		{"blank name", "a@example.com", "", "password1", "name must be 1 to 100 characters long"},
		{"short password", "a@example.com", "Анна", "1234567", "password is too short"},
		{"long password", "a@example.com", "Анна", strings.Repeat("x", 73), "password is too long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := registrationProblem(tt.email, tt.userName, tt.password); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSessionCookieIsProtected(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Service{secureCookies: true}).writeTokens(recorder, []string{"token"})

	cookie := recorder.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie = %+v", cookie)
	}
}
