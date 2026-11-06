package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRemoteURL(t *testing.T) {
	tests := []struct {
		name, raw, want string
		allowPrivate    bool
		wantErr         bool
	}{
		{name: "https", raw: " https://api.openai.com/v1/ ", want: "https://api.openai.com/v1"},
		{name: "plain http", raw: "http://api.example.com/v1", wantErr: true},
		{name: "http allowed for private setups", raw: "http://192.168.1.5:1234/v1", want: "http://192.168.1.5:1234/v1", allowPrivate: true},
		{name: "file scheme", raw: "file:///etc/passwd", wantErr: true},
		{name: "credentials in url", raw: "https://user:pass@example.com/v1", wantErr: true},
		{name: "query", raw: "https://example.com/v1?x=1", wantErr: true},
		{name: "no host", raw: "https:///v1", wantErr: true},
		{name: "empty", raw: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeRemoteURL(tt.raw, tt.allowPrivate)
			if tt.wantErr != (err != nil) || got != tt.want {
				t.Errorf("got %q, %v", got, err)
			}
		})
	}
}

func TestIsPublicAddress(t *testing.T) {
	for address, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.0.0.1": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "::1": false,
		"fe80::1": false, "fc00::1": false, "::ffff:127.0.0.1": false, "224.0.0.1": false,
	} {
		if got := isPublicAddress(netip.MustParseAddr(address)); got != want {
			t.Errorf("%s = %v, want %v", address, got, want)
		}
	}
}

func TestRemoteClientRefusesInternalAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the request reached an internal address")
	}))
	defer server.Close()

	_, err := newRemoteHTTPClient(false).Get(server.URL)
	if !errors.Is(err, errBlockedAddress) {
		t.Errorf("err = %v", err)
	}
}

func TestRemoteClientDoesNotFollowRedirects(t *testing.T) {
	var followed bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer target.Close()
	server := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer server.Close()

	response, err := newRemoteHTTPClient(true).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if followed || response.StatusCode != http.StatusFound {
		t.Errorf("followed = %v, status = %d", followed, response.StatusCode)
	}
}

func TestSecretBox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	box, err := loadSecretBox(path)
	if err != nil {
		t.Fatal(err)
	}

	sealed, err := box.seal("sk-test", "user-a")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := box.open(sealed, "user-a"); err != nil || got != "sk-test" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := box.open(sealed, "user-b"); err == nil {
		t.Error("a key opened for another account")
	}

	reloaded, err := loadSecretBox(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reloaded.open(sealed, "user-a"); err != nil || got != "sk-test" {
		t.Errorf("after reload: %q, %v", got, err)
	}
}

func TestErrorsDoNotRevealTheKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key "+r.Header.Get("Authorization"), http.StatusUnauthorized)
	}))
	defer server.Close()

	var result struct{}
	err := New(server.URL, "sk-secret-value", "m", "").Structured(context.Background(), "s", "u", "n", map[string]any{}, &result)
	if err == nil || strings.Contains(err.Error(), "sk-secret-value") {
		t.Errorf("err = %v", err)
	}
}
