package fs

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuthenticationUsesCaseInsensitiveAccountsAndFreshGoSessions(t *testing.T) {
	authentication := newTestAuthentication(t)
	grant, err := authentication.SignIn("ALICE", "password:with-colon")
	if err != nil || grant == nil || grant.Account.Username != "Alice" || len(grant.Token) != 43 {
		t.Fatalf("SignIn() = %#v, %v", grant, err)
	}
	resumed, err := authentication.Resume(grant.Token)
	if err != nil || resumed == nil || resumed.Account.Username != "Alice" {
		t.Fatalf("Resume() = %#v, %v", resumed, err)
	}
	for _, input := range []struct{ username, password string }{{"alice", "wrong-password"}, {"missing", "password:with-colon"}} {
		invalid, err := authentication.SignIn(input.username, input.password)
		if err != nil || invalid != nil {
			t.Fatalf("SignIn(%q) = %#v, %v", input.username, invalid, err)
		}
	}
	if !strings.Contains(authentication.SessionDirectory(), "go-v1") {
		t.Fatalf("session directory = %q, want Go-owned child", authentication.SessionDirectory())
	}
	if err := authentication.SignOut(grant.Token); err != nil {
		t.Fatalf("SignOut() error = %v", err)
	}
	resumed, err = authentication.Resume(grant.Token)
	if err != nil || resumed != nil {
		t.Fatalf("Resume(revoked) = %#v, %v", resumed, err)
	}
}

func TestAuthenticationObserveNotifiesOnSessionRevocation(t *testing.T) {
	authentication := newTestAuthentication(t)
	grant, err := authentication.SignIn("Alice", "password:with-colon")
	if err != nil {
		t.Fatal(err)
	}
	revoked := make(chan struct{}, 1)
	stop := authentication.Observe(grant.Token, func() { revoked <- struct{}{} })
	defer stop()
	if err := authentication.SignOut(grant.Token); err != nil {
		t.Fatal(err)
	}
	select {
	case <-revoked:
	case <-time.After(time.Second):
		t.Fatal("session observer was not notified")
	}
}

func TestAuthenticationRejectsInvalidAccountSpecifications(t *testing.T) {
	for _, specifications := range [][]string{
		{"alice-password"},
		{"bad name:password"},
		{"alice:tiny"},
		{"Alice:password", "alice:replacement"},
	} {
		if authentication, err := NewAuthentication(specifications, AuthenticationOptions{SessionDirectory: t.TempDir()}); err == nil || authentication != nil {
			t.Fatalf("NewAuthentication(%#v) = %#v, %v", specifications, authentication, err)
		}
	}
}

func TestReadOnlyHandlerOwnsSessionProtocolAndProtectsReadRoutes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	authentication := newTestAuthentication(t)
	handler := NewReadOnlyHandler(openReadOnlyWorkspace(t, root), ReadOnlyServerOptions{Authentication: authentication, BindingAddress: "example.com"})
	unauthorized := readOnlyResponse(handler, http.MethodGet, "/api/directory", nil)
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), `"code":"AUTHENTICATION_REQUIRED"`) {
		t.Fatalf("unauthorized response = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	login := sessionResponse(handler, http.MethodPost, `{"username":"alice","password":"password:with-colon"}`, map[string]string{
		"Content-Type": "application/json",
		"Origin":       "http://example.com",
	})
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), `"authenticated":true`) || !strings.Contains(login.Header().Get("Set-Cookie"), "HttpOnly; SameSite=Strict; Path=/; Max-Age=") {
		t.Fatalf("login response = %d %#v %s", login.Code, login.Header(), login.Body.String())
	}
	cookie := strings.Split(login.Header().Get("Set-Cookie"), ";")[0]
	protected := readOnlyResponse(handler, http.MethodGet, "/files/hello.txt", map[string]string{"Cookie": cookie})
	if protected.Code != http.StatusOK || protected.Header().Get("Access-Control-Allow-Origin") != "" || protected.Body.String() != "hello" {
		t.Fatalf("protected file response = %d %#v %q", protected.Code, protected.Header(), protected.Body.String())
	}
	state := readOnlyResponse(handler, http.MethodGet, "/api/session", map[string]string{"Cookie": cookie})
	if state.Code != http.StatusOK || !strings.Contains(state.Body.String(), `"username":"Alice"`) {
		t.Fatalf("session state = %d %s", state.Code, state.Body.String())
	}
	logout := sessionResponse(handler, http.MethodDelete, "", map[string]string{"Origin": "http://example.com", "Cookie": cookie})
	if logout.Code != http.StatusNoContent || !strings.Contains(logout.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout response = %d %#v", logout.Code, logout.Header())
	}
	forbidden := sessionResponse(handler, http.MethodPost, `{"username":"alice","password":"password:with-colon"}`, map[string]string{"Content-Type": "application/json", "Origin": "https://attacker.example"})
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login = %d", forbidden.Code)
	}
}

func newTestAuthentication(t *testing.T) *Authentication {
	t.Helper()
	authentication, err := NewAuthentication([]string{"Alice:password:with-colon"}, AuthenticationOptions{SessionDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewAuthentication() error = %v", err)
	}
	t.Cleanup(func() { _ = authentication.Close() })
	return authentication
}

func sessionResponse(handler http.Handler, method, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://example.com/api/session", bytes.NewBufferString(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
