package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestServerHTTPHandlerServesHealthAndRefreshesAuthenticatedSession(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	grant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "http://tunnel.example.test/healthz", nil))
	if health.Code != http.StatusOK || health.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("GET /healthz = (%d, %s)", health.Code, health.Body.String())
	}
	assertServerHTTPHeaders(t, health.Result())

	request := httptest.NewRequest(http.MethodGet, "http://tunnel.example.test/api/session", nil)
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: grant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/session status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	var body struct {
		Version       int                   `json:"version"`
		Authenticated bool                  `json:"authenticated"`
		Account       serverHTTPAccountView `json:"account"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	if body.Version != 1 || !body.Authenticated || body.Account.ID != environmentAdministratorID || body.Account.Username != "admin" || body.Account.Role != AccountRoleAdmin || !body.Account.ManagedByEnvironment {
		t.Fatalf("session response = %#v", body)
	}
	if strings.Contains(response.Body.String(), "environment-secret") {
		t.Fatalf("session response leaked password: %s", response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != grant.Token || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" || cookies[0].MaxAge < 1 {
		t.Fatalf("session refresh cookie = %#v", cookies)
	}
}

func TestServerHTTPHandlerRejectsUnauthenticatedUnknownAndUnsupportedRequests(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: openServerSessions(t, accounts, state)})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	for _, test := range []struct {
		method string
		path   string
		status int
		code   string
	}{
		{method: http.MethodGet, path: "/api/session", status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
		{method: http.MethodPut, path: "/api/session/password", status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
		{method: http.MethodPost, path: "/healthz", status: http.StatusMethodNotAllowed, code: "METHOD_NOT_ALLOWED"},
		{method: http.MethodGet, path: "/api/not-found", status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
		{method: http.MethodGet, path: "/not-found", status: http.StatusNotFound, code: "NOT_FOUND"},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, "http://tunnel.example.test"+test.path, nil))
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertServerHTTPHeaders(t, response.Result())
			assertServerHTTPError(t, response.Body.Bytes(), test.code)
		})
	}
}

func TestServerHTTPHandlerRejectsUnknownAPIOnlyAfterAuthenticatingIt(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	grant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://tunnel.example.test/api/not-found", nil)
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: grant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /api/not-found status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	assertServerHTTPError(t, response.Body.Bytes(), "NOT_FOUND")
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != grant.Token || cookies[0].MaxAge < 1 {
		t.Fatalf("unknown API session refresh cookie = %#v", cookies)
	}

	methodRequest := httptest.NewRequest(http.MethodPut, "http://tunnel.example.test/api/session", nil)
	methodRequest.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: grant.Token})
	methodResponse := httptest.NewRecorder()
	handler.ServeHTTP(methodResponse, methodRequest)
	if methodResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /api/session status = %d, body = %s", methodResponse.Code, methodResponse.Body.String())
	}
	assertServerHTTPError(t, methodResponse.Body.Bytes(), "METHOD_NOT_ALLOWED")
}

func TestServerHTTPHandlerSignsInFromAValidOrigin(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	if _, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: openServerSessions(t, accounts, state)})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/session", strings.NewReader(`{"username":"ALICE","password":"alice-secret"}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Origin", "https://tunnel.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/session status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	var body struct {
		Version       int                   `json:"version"`
		Authenticated bool                  `json:"authenticated"`
		Account       serverHTTPAccountView `json:"account"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if body.Version != 1 || !body.Authenticated || body.Account.Username != "alice" || body.Account.Role != AccountRoleUser || body.Account.ManagedByEnvironment {
		t.Fatalf("login response = %#v", body)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value == "" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge < 1 {
		t.Fatalf("login cookie = %#v", cookies)
	}
}

func TestServerHTTPHandlerRejectsInvalidLoginRequests(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: openServerSessions(t, accounts, state)})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	for _, test := range []struct {
		name        string
		contentType string
		origin      string
		body        string
		status      int
		code        string
	}{
		{name: "foreign origin", contentType: "application/json", origin: "https://other.example.test", body: `{"username":"admin","password":"environment-secret"}`, status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "wrong media", contentType: "text/plain", body: `{"username":"admin","password":"environment-secret"}`, status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "malformed JSON", contentType: "application/json", body: `{`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "unknown field", contentType: "application/json", body: `{"username":"admin","password":"environment-secret","extra":true}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "missing password", contentType: "application/json", body: `{"username":"admin"}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "null password", contentType: "application/json", body: `{"username":"admin","password":null}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "multiple JSON values", contentType: "application/json", body: `{"username":"admin","password":"environment-secret"} {}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "bad credentials", contentType: "application/json", body: `{"username":"admin","password":"wrong-secret"}`, status: http.StatusUnauthorized, code: "AUTHENTICATION_FAILED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/session", strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertServerHTTPHeaders(t, response.Result())
			assertServerHTTPError(t, response.Body.Bytes(), test.code)
		})
	}
}

func TestServerHTTPHandlerSignsOutAndClearsTheSessionCookie(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	grant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodDelete, "http://tunnel.example.test/api/session", nil)
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: grant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("DELETE /api/session = (%d, %q)", response.Code, response.Body.String())
	}
	assertServerHTTPNoContentHeaders(t, response.Result())
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].MaxAge >= 0 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatalf("expired session cookie = %#v", cookies)
	}
	if resumed, err := sessions.Resume(context.Background(), grant.Token); err != nil || resumed != nil {
		t.Fatalf("Resume(signed-out token) = (%#v, %v), want nil", resumed, err)
	}

	withoutCookie := httptest.NewRecorder()
	handler.ServeHTTP(withoutCookie, httptest.NewRequest(http.MethodDelete, "http://tunnel.example.test/api/session", nil))
	if withoutCookie.Code != http.StatusUnauthorized {
		t.Fatalf("DELETE /api/session without cookie status = %d, body = %q", withoutCookie.Code, withoutCookie.Body.String())
	}
	assertServerHTTPHeaders(t, withoutCookie.Result())
	assertServerHTTPError(t, withoutCookie.Body.Bytes(), "AUTHENTICATION_REQUIRED")
}

func TestServerHTTPHandlerRejectsForeignOriginBeforeSignOut(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	grant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodDelete, "http://tunnel.example.test/api/session", nil)
	request.Header.Set("Origin", "https://other.example.test")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: grant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign DELETE /api/session status = %d", response.Code)
	}
	assertServerHTTPError(t, response.Body.Bytes(), "ORIGIN_FORBIDDEN")
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != grant.Token || cookies[0].MaxAge < 1 {
		t.Fatalf("foreign-origin session refresh cookie = %#v", cookies)
	}
	if resumed, err := sessions.Resume(context.Background(), grant.Token); err != nil || resumed == nil {
		t.Fatalf("Resume(foreign-origin rejected token) = (%#v, %v)", resumed, err)
	}
}

func TestServerHTTPHandlerChangesOwnLocalPasswordAndRevokesAllSessions(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	if _, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount() error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	current, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(current) error = %v", err)
	}
	other, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(other) error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, "http://tunnel.example.test/api/session/password", strings.NewReader(`{"currentPassword":"alice-secret","newPassword":"replacement-secret"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://tunnel.example.test")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: current.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("PUT /api/session/password status = %d, body = %q", response.Code, response.Body.String())
	}
	assertServerHTTPNoContentHeaders(t, response.Result())
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].MaxAge >= 0 {
		t.Fatalf("password-change cookie = %#v", cookies)
	}
	for _, token := range []string{current.Token, other.Token} {
		if resumed, err := sessions.Resume(context.Background(), token); err != nil || resumed != nil {
			t.Fatalf("Resume(revoked token) = (%#v, %v), want nil", resumed, err)
		}
	}
	if _, err := sessions.SignIn(context.Background(), "alice", "alice-secret"); err == nil {
		t.Fatal("SignIn(old password) error = nil")
	}
	if _, err := sessions.SignIn(context.Background(), "alice", "replacement-secret"); err != nil {
		t.Fatalf("SignIn(replacement password) error = %v", err)
	}
}

func TestServerHTTPHandlerRejectsInvalidOwnPasswordChangesWithoutRevokingSession(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	if _, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount() error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	grant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	for _, test := range []struct {
		name   string
		origin string
		body   string
		status int
		code   string
	}{
		{name: "foreign origin", origin: "https://other.example.test", status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "wrong current password", origin: "https://tunnel.example.test", body: `{"currentPassword":"wrong-secret","newPassword":"replacement-secret"}`, status: http.StatusBadRequest, code: "INVALID_CURRENT_PASSWORD"},
		{name: "missing password", origin: "https://tunnel.example.test", body: `{"currentPassword":"alice-secret"}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "null password", origin: "https://tunnel.example.test", body: `{"currentPassword":"alice-secret","newPassword":null}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "unknown field", origin: "https://tunnel.example.test", body: `{"currentPassword":"alice-secret","newPassword":"replacement-secret","extra":true}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "wrong media", origin: "https://tunnel.example.test", body: `{"currentPassword":"alice-secret","newPassword":"replacement-secret"}`, status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, "http://tunnel.example.test/api/session/password", strings.NewReader(test.body))
			request.Header.Set("Origin", test.origin)
			if test.name != "wrong media" {
				request.Header.Set("Content-Type", "application/json")
			} else {
				request.Header.Set("Content-Type", "text/plain")
			}
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: grant.Token})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertServerHTTPHeaders(t, response.Result())
			assertServerHTTPError(t, response.Body.Bytes(), test.code)
			if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != grant.Token || cookies[0].MaxAge < 1 {
				t.Fatalf("error session refresh cookie = %#v", cookies)
			}
			if resumed, err := sessions.Resume(context.Background(), grant.Token); err != nil || resumed == nil {
				t.Fatalf("Resume(failed-change token) = (%#v, %v)", resumed, err)
			}
		})
	}
}

func TestServerHTTPHandlerRejectsEnvironmentPasswordChange(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	grant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, "http://tunnel.example.test/api/session/password", strings.NewReader(`{"currentPassword":"environment-secret","newPassword":"replacement-secret"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: grant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("environment password change status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPError(t, response.Body.Bytes(), "MANAGED_ACCOUNT")
}

func TestServerHTTPHandlerReadsScopedClientAndTunnelViewsWithoutCredentials(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := accounts.CreateLocalAccount(context.Background(), "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), alice.ID, "Alice laptop")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if _, err := plane.CreateTunnel(context.Background(), client.ID, TunnelMutationInput{
		Protocol:      tunnelruntime.TunnelProtocolHTTP,
		CustomDomains: []string{"alice.example.test"},
		LocalPort:     3000,
		Options: &TunnelOptionsInput{HTTP: &TunnelHTTPOptionsInput{
			BasicAuth: &tunnelruntime.TunnelBasicAuth{Username: "operator", Password: "secret-value"},
		}},
	}); err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	bobGrant, err := sessions.SignIn(context.Background(), "bob", "bob-secret")
	if err != nil {
		t.Fatalf("SignIn(bob) error = %v", err)
	}
	adminGrant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}

	list := serverHTTPReadRequest(handler, http.MethodGet, "/api/clients", aliceGrant.Token)
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/clients status = %d, body = %s", list.Code, list.Body.String())
	}
	assertServerHTTPHeaders(t, list.Result())
	var listBody struct {
		Version int                    `json:"version"`
		Clients []serverHTTPClientView `json:"clients"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode client list: %v", err)
	}
	if listBody.Version != 1 || len(listBody.Clients) != 1 {
		t.Fatalf("client list = %#v", listBody)
	}
	view := listBody.Clients[0]
	if view.ID != client.ID || view.Owner != (serverHTTPClientOwner{ID: alice.ID, Username: "alice"}) || view.Runtime.ConnectionState != ServerClientDisconnected || view.Runtime.ProcessState != tunnelruntime.FRPProcessStopped || view.TunnelCounts != (serverHTTPTunnelCounts{Total: 1, Enabled: 1, Pending: 1}) {
		t.Fatalf("client view = %#v", view)
	}
	if strings.Contains(list.Body.String(), "ownerAccountId") || strings.Contains(list.Body.String(), "secret-value") {
		t.Fatalf("client list leaked internal field or password: %s", list.Body.String())
	}

	detail := serverHTTPReadRequest(handler, http.MethodGet, "/api/clients/"+client.ID, aliceGrant.Token)
	if detail.Code != http.StatusOK {
		t.Fatalf("GET /api/clients/:id status = %d, body = %s", detail.Code, detail.Body.String())
	}
	var detailBody struct {
		Version int                  `json:"version"`
		Client  serverHTTPClientView `json:"client"`
		Tunnels []map[string]any     `json:"tunnels"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &detailBody); err != nil {
		t.Fatalf("decode client detail: %v", err)
	}
	if detailBody.Version != 1 || detailBody.Client.ID != client.ID || len(detailBody.Tunnels) != 1 {
		t.Fatalf("client detail = %#v", detailBody)
	}
	tunnel := detailBody.Tunnels[0]
	if _, found := tunnel["location"]; !found || tunnel["location"] != nil || tunnel["state"] != string(ServerTunnelPending) {
		t.Fatalf("public HTTP tunnel = %#v", tunnel)
	}
	options, ok := tunnel["options"].(map[string]any)
	if !ok {
		t.Fatalf("public tunnel options = %#v", tunnel["options"])
	}
	httpOptions, ok := options["http"].(map[string]any)
	if !ok {
		t.Fatalf("public HTTP options = %#v", options["http"])
	}
	basicAuth, ok := httpOptions["basicAuth"].(map[string]any)
	if !ok || basicAuth["username"] != "operator" || basicAuth["passwordConfigured"] != true || strings.Contains(detail.Body.String(), "secret-value") {
		t.Fatalf("public Basic Auth projection = %#v, body = %s", basicAuth, detail.Body.String())
	}

	tunnels := serverHTTPReadRequest(handler, http.MethodGet, "/api/clients/"+client.ID+"/tunnels", aliceGrant.Token)
	if tunnels.Code != http.StatusOK {
		t.Fatalf("GET /api/clients/:id/tunnels status = %d, body = %s", tunnels.Code, tunnels.Body.String())
	}
	if strings.Contains(tunnels.Body.String(), "secret-value") {
		t.Fatalf("tunnel list leaked Basic Auth password: %s", tunnels.Body.String())
	}

	if response := serverHTTPReadRequest(handler, http.MethodGet, "/api/clients", bobGrant.Token); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"clients":[]`) {
		t.Fatalf("bob client list = (%d, %s)", response.Code, response.Body.String())
	}
	for _, path := range []string{"/api/clients/" + client.ID, "/api/clients/" + client.ID + "/tunnels"} {
		response := serverHTTPReadRequest(handler, http.MethodGet, path, bobGrant.Token)
		if response.Code != http.StatusNotFound {
			t.Fatalf("bob GET %s status = %d, body = %s", path, response.Code, response.Body.String())
		}
		assertServerHTTPError(t, response.Body.Bytes(), "NOT_FOUND")
	}
	adminDetail := serverHTTPReadRequest(handler, http.MethodGet, "/api/clients/"+client.ID, adminGrant.Token)
	if adminDetail.Code != http.StatusOK || !strings.Contains(adminDetail.Body.String(), `"username":"alice"`) {
		t.Fatalf("admin client detail = (%d, %s)", adminDetail.Code, adminDetail.Body.String())
	}
}

func TestServerTunnelPresentationStateForReadViews(t *testing.T) {
	revision := int64(4)
	client := TrustedTunnelClient{DesiredRevision: revision, LastAppliedRevision: revision}
	running := ServerClientRuntimeState{ConnectionState: ServerClientConnected, ProcessState: tunnelruntime.FRPProcessRunning}
	for _, test := range []struct {
		name    string
		tunnel  tunnelruntime.TunnelDefinition
		client  TrustedTunnelClient
		runtime ServerClientRuntimeState
		want    ServerTunnelPresentationState
	}{
		{name: "disabled", tunnel: tunnelruntime.TunnelDefinition{Enabled: false}, client: client, runtime: running, want: ServerTunnelDisabled},
		{name: "current error", tunnel: tunnelruntime.TunnelDefinition{Enabled: true}, client: client, runtime: ServerClientRuntimeState{ProcessState: tunnelruntime.FRPProcessRunning, LastError: &tunnelruntime.StructuredRuntimeError{Code: "CONFIGURATION_FAILED", Revision: &revision}}, want: ServerTunnelError},
		{name: "unapplied revision", tunnel: tunnelruntime.TunnelDefinition{Enabled: true}, client: TrustedTunnelClient{DesiredRevision: revision, LastAppliedRevision: revision - 1}, runtime: running, want: ServerTunnelPending},
		{name: "stopped process", tunnel: tunnelruntime.TunnelDefinition{Enabled: true}, client: client, runtime: ServerClientRuntimeState{ProcessState: tunnelruntime.FRPProcessStopped}, want: ServerTunnelPending},
		{name: "applied", tunnel: tunnelruntime.TunnelDefinition{Enabled: true}, client: client, runtime: running, want: ServerTunnelApplied},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := serverTunnelPresentationStateFor(test.tunnel, test.client, test.runtime); got != test.want {
				t.Fatalf("serverTunnelPresentationStateFor() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestServerHTTPHandlerCreatesScopedClients(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount() error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	grant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/clients", strings.NewReader(`{"remark":"  Work laptop  "}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://tunnel.example.test")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: grant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/clients status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	var body struct {
		Version int                  `json:"version"`
		Client  serverHTTPClientView `json:"client"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode created client: %v", err)
	}
	if body.Version != 1 || body.Client.Remark != "Work laptop" || body.Client.Owner != (serverHTTPClientOwner{ID: alice.ID, Username: "alice"}) || !strings.HasPrefix(body.Client.Token, "ycy_") || body.Client.DesiredRevision != 0 || body.Client.TunnelCounts != (serverHTTPTunnelCounts{}) {
		t.Fatalf("created client = %#v", body.Client)
	}
	if strings.Contains(response.Body.String(), "ownerAccountId") {
		t.Fatalf("created client leaked owner account ID: %s", response.Body.String())
	}
	stored, err := plane.GetClientForOwner(context.Background(), body.Client.ID, alice.ID)
	if err != nil || stored.Token != body.Client.Token {
		t.Fatalf("stored created client = (%#v, %v)", stored, err)
	}
	defaultRequest := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/clients", strings.NewReader(`{}`))
	defaultRequest.Header.Set("Content-Type", "application/json")
	defaultRequest.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: grant.Token})
	defaultResponse := httptest.NewRecorder()
	handler.ServeHTTP(defaultResponse, defaultRequest)
	if defaultResponse.Code != http.StatusCreated {
		t.Fatalf("default POST /api/clients status = %d, body = %s", defaultResponse.Code, defaultResponse.Body.String())
	}
	var defaultBody struct {
		Client serverHTTPClientView `json:"client"`
	}
	if err := json.Unmarshal(defaultResponse.Body.Bytes(), &defaultBody); err != nil || defaultBody.Client.Remark != "" {
		t.Fatalf("default created client = (%#v, %v)", defaultBody.Client, err)
	}

	for _, test := range []struct {
		name        string
		origin      string
		contentType string
		body        string
		status      int
		code        string
	}{
		{name: "foreign origin", origin: "https://other.example.test", contentType: "application/json", body: `{}`, status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "wrong media", origin: "https://tunnel.example.test", contentType: "text/plain", body: `{}`, status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "null remark", origin: "https://tunnel.example.test", contentType: "application/json", body: `{"remark":null}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "unknown field", origin: "https://tunnel.example.test", contentType: "application/json", body: `{"unknown":true}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "long remark", origin: "https://tunnel.example.test", contentType: "application/json", body: `{"remark":"` + strings.Repeat("x", 101) + `"}`, status: http.StatusBadRequest, code: "INVALID_CLIENT_REMARK"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/clients", strings.NewReader(test.body))
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Content-Type", test.contentType)
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: grant.Token})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertServerHTTPError(t, response.Body.Bytes(), test.code)
		})
	}
	clients, err := plane.ListClientsForOwner(context.Background(), alice.ID)
	if err != nil || len(clients) != 2 {
		t.Fatalf("owner clients after rejected creates = (%#v, %v)", clients, err)
	}
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/clients", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST /api/clients status = %d", unauthenticated.Code)
	}
	assertServerHTTPError(t, unauthenticated.Body.Bytes(), "AUTHENTICATION_REQUIRED")
}

func TestServerHTTPHandlerPatchesOwnedClientRemarkWithoutChangingTokenOrRevision(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := accounts.CreateLocalAccount(context.Background(), "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), alice.ID, "Original")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	bobGrant, err := sessions.SignIn(context.Background(), "bob", "bob-secret")
	if err != nil {
		t.Fatalf("SignIn(bob) error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPatch, "http://tunnel.example.test/api/clients/"+client.ID, strings.NewReader(`{"remark":"  Updated laptop  "}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://tunnel.example.test")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: aliceGrant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH /api/clients/:id status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Client serverHTTPClientView `json:"client"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode patched client: %v", err)
	}
	if body.Client.Remark != "Updated laptop" || body.Client.Token != client.Token || body.Client.DesiredRevision != client.DesiredRevision || body.Client.LastAppliedRevision != client.LastAppliedRevision {
		t.Fatalf("patched client = %#v", body.Client)
	}
	stored, err := plane.GetClientForOwner(context.Background(), client.ID, alice.ID)
	if err != nil || stored.Remark != "Updated laptop" || stored.Token != client.Token || stored.DesiredRevision != client.DesiredRevision {
		t.Fatalf("stored patched client = (%#v, %v)", stored, err)
	}

	for _, test := range []struct {
		name        string
		token       string
		origin      string
		contentType string
		body        string
		status      int
		code        string
	}{
		{name: "other owner", token: bobGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"remark":"stolen"}`, status: http.StatusNotFound, code: "NOT_FOUND"},
		{name: "foreign origin", token: aliceGrant.Token, origin: "https://other.example.test", contentType: "application/json", body: `{"remark":"blocked"}`, status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "missing remark", token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "null remark", token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"remark":null}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "wrong media", token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "text/plain", body: `{"remark":"blocked"}`, status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "long remark", token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"remark":"` + strings.Repeat("x", 101) + `"}`, status: http.StatusBadRequest, code: "INVALID_CLIENT_REMARK"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPatch, "http://tunnel.example.test/api/clients/"+client.ID, strings.NewReader(test.body))
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Content-Type", test.contentType)
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: test.token})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertServerHTTPError(t, response.Body.Bytes(), test.code)
		})
	}
	stored, err = plane.GetClientForOwner(context.Background(), client.ID, alice.ID)
	if err != nil || stored.Remark != "Updated laptop" || stored.Token != client.Token || stored.DesiredRevision != client.DesiredRevision {
		t.Fatalf("stored client after rejected patches = (%#v, %v)", stored, err)
	}
}

func TestServerHTTPHandlerRotatesOwnedClientTokenWithoutChangingRevisions(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := accounts.CreateLocalAccount(context.Background(), "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), alice.ID, "Laptop")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	bobGrant, err := sessions.SignIn(context.Background(), "bob", "bob-secret")
	if err != nil {
		t.Fatalf("SignIn(bob) error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/clients/"+client.ID+"/rotate", nil)
	request.Header.Set("Origin", "https://tunnel.example.test")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: aliceGrant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/clients/:id/rotate status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Client serverHTTPClientView `json:"client"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode rotated client: %v", err)
	}
	if body.Client.Token == client.Token || !strings.HasPrefix(body.Client.Token, "ycy_") || !body.Client.RevocationPending || body.Client.RotatedAt == nil || body.Client.DesiredRevision != client.DesiredRevision || body.Client.LastAppliedRevision != client.LastAppliedRevision {
		t.Fatalf("rotated client = %#v", body.Client)
	}
	if old, err := plane.FindClientByToken(context.Background(), client.Token); err != nil || old != nil {
		t.Fatalf("FindClientByToken(old) = (%#v, %v)", old, err)
	}
	if replacement, err := plane.FindClientByToken(context.Background(), body.Client.Token); err != nil || replacement == nil || !replacement.RevocationPending {
		t.Fatalf("FindClientByToken(replacement) = (%#v, %v)", replacement, err)
	}

	for _, test := range []struct {
		name   string
		token  string
		origin string
		status int
		code   string
	}{
		{name: "other owner", token: bobGrant.Token, origin: "https://tunnel.example.test", status: http.StatusNotFound, code: "NOT_FOUND"},
		{name: "foreign origin", token: aliceGrant.Token, origin: "https://other.example.test", status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/clients/"+client.ID+"/rotate", nil)
			request.Header.Set("Origin", test.origin)
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: test.token})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertServerHTTPError(t, response.Body.Bytes(), test.code)
		})
	}
	stored, err := plane.GetClientForOwner(context.Background(), client.ID, alice.ID)
	if err != nil || stored.Token != body.Client.Token || !stored.RevocationPending || stored.DesiredRevision != client.DesiredRevision || stored.LastAppliedRevision != client.LastAppliedRevision {
		t.Fatalf("stored client after rejected rotations = (%#v, %v)", stored, err)
	}
}

func TestServerHTTPHandlerDeletesOwnedClientAndReleasesTunnelReservations(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := accounts.CreateLocalAccount(context.Background(), "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), alice.ID, "Laptop")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if _, err := plane.CreateTunnel(context.Background(), client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"released.example.test"}, LocalPort: 3000}); err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	bobGrant, err := sessions.SignIn(context.Background(), "bob", "bob-secret")
	if err != nil {
		t.Fatalf("SignIn(bob) error = %v", err)
	}
	for _, test := range []struct {
		name   string
		token  string
		origin string
		status int
		code   string
	}{
		{name: "other owner", token: bobGrant.Token, origin: "https://tunnel.example.test", status: http.StatusNotFound, code: "NOT_FOUND"},
		{name: "foreign origin", token: aliceGrant.Token, origin: "https://other.example.test", status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodDelete, "http://tunnel.example.test/api/clients/"+client.ID, nil)
			request.Header.Set("Origin", test.origin)
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: test.token})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertServerHTTPError(t, response.Body.Bytes(), test.code)
		})
	}

	request := httptest.NewRequest(http.MethodDelete, "http://tunnel.example.test/api/clients/"+client.ID, nil)
	request.Header.Set("Origin", "https://tunnel.example.test")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: aliceGrant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("DELETE /api/clients/:id = (%d, %q)", response.Code, response.Body.String())
	}
	assertServerHTTPNoContentHeaders(t, response.Result())
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != aliceGrant.Token || cookies[0].MaxAge < 1 {
		t.Fatalf("client delete session refresh cookie = %#v", cookies)
	}
	assertServerDomainCode(t, func() error {
		_, err := plane.GetClient(context.Background(), client.ID)
		return err
	}(), "NOT_FOUND")
	replacement, err := plane.CreateClient(context.Background(), alice.ID, "Replacement")
	if err != nil {
		t.Fatalf("CreateClient(replacement) error = %v", err)
	}
	if _, err := plane.CreateTunnel(context.Background(), replacement.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"released.example.test"}, LocalPort: 3000}); err != nil {
		t.Fatalf("CreateTunnel(released reservation) error = %v", err)
	}
}

func TestServerHTTPHandlerCreatesOwnedTypedTunnels(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := accounts.CreateLocalAccount(context.Background(), "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), alice.ID, "Laptop")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	bobGrant, err := sessions.SignIn(context.Background(), "bob", "bob-secret")
	if err != nil {
		t.Fatalf("SignIn(bob) error = %v", err)
	}

	create := func(token, origin, contentType, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/clients/"+client.ID+"/tunnels", strings.NewReader(body))
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	type createdTunnelResponse struct {
		Version int `json:"version"`
		Tunnel  struct {
			ID            string                        `json:"id"`
			Label         string                        `json:"label"`
			Protocol      tunnelruntime.TunnelProtocol  `json:"protocol"`
			CustomDomains []string                      `json:"customDomains"`
			Location      *string                       `json:"location"`
			ServerPort    *int64                        `json:"serverPort"`
			LocalHost     string                        `json:"localHost"`
			LocalPort     int64                         `json:"localPort"`
			Enabled       bool                          `json:"enabled"`
			State         ServerTunnelPresentationState `json:"state"`
			Options       struct {
				HTTP *struct {
					BasicAuth *serverHTTPPublicTunnelBasicAuth `json:"basicAuth"`
				} `json:"http"`
			} `json:"options"`
		} `json:"tunnel"`
	}

	httpPayload := `{"label":"Public app","protocol":"http","customDomains":["Routes.Example.Test.","routes.example.test"],"location":"/service","serverPort":null,"localHost":" 127.0.0.1 ","localPort":3000,"enabled":true,"options":{"transport":{"useEncryption":true,"useCompression":false,"bandwidthLimit":{"value":2.5,"unit":"MB","mode":"server"},"proxyProtocolVersion":"v2"},"healthCheck":{"type":"http","path":"/ready","intervalSeconds":20,"timeoutSeconds":5,"maxFailed":2,"headers":[{"name":"X-Health","value":"yes"}]},"http":{"basicAuth":{"username":"operator","password":"secret-value"},"hostHeaderRewrite":"origin.internal","requestHeaders":[{"name":"X-Request","value":"1"}],"responseHeaders":[{"name":"X-Response","value":"1"}]}}}`
	httpResponse := create(aliceGrant.Token, "https://tunnel.example.test", "application/json", httpPayload)
	if httpResponse.Code != http.StatusCreated {
		t.Fatalf("POST HTTP tunnel status = %d, body = %s", httpResponse.Code, httpResponse.Body.String())
	}
	assertServerHTTPHeaders(t, httpResponse.Result())
	if strings.Contains(httpResponse.Body.String(), "secret-value") {
		t.Fatalf("HTTP tunnel response leaked Basic Auth password: %s", httpResponse.Body.String())
	}
	var httpBody createdTunnelResponse
	if err := json.Unmarshal(httpResponse.Body.Bytes(), &httpBody); err != nil {
		t.Fatalf("decode HTTP tunnel response: %v", err)
	}
	if httpBody.Version != 1 || httpBody.Tunnel.ID == "" || httpBody.Tunnel.Label != "Public app" || httpBody.Tunnel.Protocol != tunnelruntime.TunnelProtocolHTTP || len(httpBody.Tunnel.CustomDomains) != 1 || httpBody.Tunnel.CustomDomains[0] != "routes.example.test" || httpBody.Tunnel.Location == nil || *httpBody.Tunnel.Location != "/service" || httpBody.Tunnel.ServerPort != nil || httpBody.Tunnel.LocalHost != "127.0.0.1" || httpBody.Tunnel.LocalPort != 3000 || !httpBody.Tunnel.Enabled || httpBody.Tunnel.State != ServerTunnelPending {
		t.Fatalf("HTTP tunnel response = %#v", httpBody)
	}
	if httpBody.Tunnel.Options.HTTP == nil || httpBody.Tunnel.Options.HTTP.BasicAuth == nil || httpBody.Tunnel.Options.HTTP.BasicAuth.Username != "operator" || !httpBody.Tunnel.Options.HTTP.BasicAuth.PasswordConfigured {
		t.Fatalf("HTTP tunnel Basic Auth response = %#v", httpBody.Tunnel.Options.HTTP)
	}
	storedHTTP, err := plane.GetTunnel(context.Background(), httpBody.Tunnel.ID)
	if err != nil || storedHTTP.Options.HTTP == nil || storedHTTP.Options.HTTP.BasicAuth == nil || storedHTTP.Options.HTTP.BasicAuth.Password != "secret-value" {
		t.Fatalf("stored HTTP tunnel = (%#v, %v)", storedHTTP, err)
	}

	tcpResponse := create(aliceGrant.Token, "", "application/json", `{"label":"TCP service","protocol":"tcp","location":null,"serverPort":20002,"localHost":"127.0.0.1","localPort":4000,"enabled":false,"options":{"transport":{"useEncryption":false,"useCompression":true,"bandwidthLimit":null,"proxyProtocolVersion":null},"healthCheck":null,"http":null}}`)
	if tcpResponse.Code != http.StatusCreated {
		t.Fatalf("POST TCP tunnel status = %d, body = %s", tcpResponse.Code, tcpResponse.Body.String())
	}
	var tcpBody createdTunnelResponse
	if err := json.Unmarshal(tcpResponse.Body.Bytes(), &tcpBody); err != nil {
		t.Fatalf("decode TCP tunnel response: %v", err)
	}
	if tcpBody.Tunnel.Protocol != tunnelruntime.TunnelProtocolTCP || tcpBody.Tunnel.ServerPort == nil || *tcpBody.Tunnel.ServerPort != 20002 || tcpBody.Tunnel.Enabled || tcpBody.Tunnel.State != ServerTunnelDisabled || strings.Contains(tcpResponse.Body.String(), "customDomains") {
		t.Fatalf("TCP tunnel response = %#v", tcpBody)
	}

	udpResponse := create(aliceGrant.Token, "", "application/json", `{"label":"UDP service","protocol":"udp","location":null,"serverPort":null,"localHost":"127.0.0.1","localPort":5000,"enabled":true,"options":{"transport":{"useEncryption":false,"useCompression":false,"bandwidthLimit":null,"proxyProtocolVersion":null},"healthCheck":null,"http":null}}`)
	if udpResponse.Code != http.StatusCreated {
		t.Fatalf("POST UDP tunnel status = %d, body = %s", udpResponse.Code, udpResponse.Body.String())
	}
	var udpBody createdTunnelResponse
	if err := json.Unmarshal(udpResponse.Body.Bytes(), &udpBody); err != nil {
		t.Fatalf("decode UDP tunnel response: %v", err)
	}
	if udpBody.Tunnel.Protocol != tunnelruntime.TunnelProtocolUDP || udpBody.Tunnel.ServerPort == nil || *udpBody.Tunnel.ServerPort != 20000 || !udpBody.Tunnel.Enabled || udpBody.Tunnel.State != ServerTunnelPending || strings.Contains(udpResponse.Body.String(), "customDomains") {
		t.Fatalf("UDP tunnel response = %#v", udpBody)
	}

	for _, test := range []struct {
		name        string
		token       string
		origin      string
		contentType string
		body        string
		status      int
		code        string
	}{
		{name: "other owner", token: bobGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: httpPayload, status: http.StatusNotFound, code: "NOT_FOUND"},
		{name: "foreign origin", token: aliceGrant.Token, origin: "https://other.example.test", contentType: "application/json", body: httpPayload, status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "wrong media", token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "text/plain", body: httpPayload, status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "unknown nested field", token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"protocol":"http","customDomains":["other.example.test"],"localPort":3000,"options":{"transport":{"unexpected":true}}}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "reserved route", token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: httpPayload, status: http.StatusConflict, code: "RESOURCE_RESERVED"},
		{name: "unauthenticated", token: "", origin: "https://tunnel.example.test", contentType: "application/json", body: httpPayload, status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := create(test.token, test.origin, test.contentType, test.body)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertServerHTTPError(t, response.Body.Bytes(), test.code)
		})
	}
	storedClient, err := plane.GetClientForOwner(context.Background(), client.ID, alice.ID)
	if err != nil || storedClient.DesiredRevision != 3 || storedClient.LastAppliedRevision != 0 {
		t.Fatalf("client revisions after rejected creates = (%#v, %v)", storedClient, err)
	}
	storedTunnels, err := plane.ListTunnels(context.Background(), client.ID)
	if err != nil || len(storedTunnels) != 3 {
		t.Fatalf("stored tunnels after rejected creates = (%#v, %v)", storedTunnels, err)
	}
}

func TestServerHTTPHandlerPatchesOwnedTypedTunnels(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := accounts.CreateLocalAccount(context.Background(), "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	password := "secret-value"
	hostRewrite := "internal.example.test"
	proxyProtocol := "v2"
	location := "/current"
	client, err := plane.CreateClient(context.Background(), alice.ID, "Laptop")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	tunnel, err := plane.CreateTunnel(context.Background(), client.ID, TunnelMutationInput{
		Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"patch.example.test"}, Location: &location, LocalPort: 3000,
		Options: &TunnelOptionsInput{
			Transport:   &TunnelTransportOptionsInput{UseEncryption: boolPointer(true), BandwidthLimit: &tunnelruntime.TunnelBandwidthLimit{Value: 2, Unit: "MB", Mode: "server"}, ProxyProtocolVersion: &proxyProtocol},
			HealthCheck: &TunnelHealthCheckInput{Type: "http", Path: stringPointer("/health"), IntervalSeconds: 10, TimeoutSeconds: 3, MaxFailed: 2, Headers: []tunnelruntime.TunnelHeader{{Name: "X-Probe", Value: "initial"}}},
			HTTP:        &TunnelHTTPOptionsInput{BasicAuth: &tunnelruntime.TunnelBasicAuth{Username: "operator", Password: password}, HostHeaderRewrite: &hostRewrite, RequestHeaders: []tunnelruntime.TunnelHeader{{Name: "X-Request", Value: "initial"}}, ResponseHeaders: []tunnelruntime.TunnelHeader{{Name: "X-Response", Value: "initial"}}},
		},
	})
	if err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	bobGrant, err := sessions.SignIn(context.Background(), "bob", "bob-secret")
	if err != nil {
		t.Fatalf("SignIn(bob) error = %v", err)
	}

	patchTunnel := func(token, origin, contentType, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPatch, "http://tunnel.example.test/api/tunnels/"+tunnel.ID, strings.NewReader(body))
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	type patchedTunnelResponse struct {
		Version int `json:"version"`
		Tunnel  struct {
			Protocol      tunnelruntime.TunnelProtocol `json:"protocol"`
			Label         string                       `json:"label"`
			Location      *string                      `json:"location"`
			ServerPort    *int64                       `json:"serverPort"`
			LocalPort     int64                        `json:"localPort"`
			Enabled       bool                         `json:"enabled"`
			CustomDomains []string                     `json:"customDomains"`
			Options       struct {
				HTTP *struct {
					BasicAuth *serverHTTPPublicTunnelBasicAuth `json:"basicAuth"`
				} `json:"http"`
			} `json:"options"`
		} `json:"tunnel"`
	}

	patchPayload := `{"label":" Updated tunnel ","location":null,"localPort":3001,"enabled":false,"options":{"transport":{"useCompression":true,"bandwidthLimit":null,"proxyProtocolVersion":null},"healthCheck":null,"http":{"basicAuth":{"username":"renamed"},"hostHeaderRewrite":null,"requestHeaders":[],"responseHeaders":[{"name":"X-Response","value":"fresh"}]}}}`
	response := patchTunnel(aliceGrant.Token, "https://tunnel.example.test", "application/json", patchPayload)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH /api/tunnels/:id status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	if strings.Contains(response.Body.String(), password) {
		t.Fatalf("patched tunnel response leaked Basic Auth password: %s", response.Body.String())
	}
	var body patchedTunnelResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode patched tunnel response: %v", err)
	}
	if body.Version != 1 || body.Tunnel.Protocol != tunnelruntime.TunnelProtocolHTTP || body.Tunnel.Label != "Updated tunnel" || body.Tunnel.Location != nil || body.Tunnel.LocalPort != 3001 || body.Tunnel.Enabled || len(body.Tunnel.CustomDomains) != 1 || body.Tunnel.CustomDomains[0] != "patch.example.test" || body.Tunnel.Options.HTTP == nil || body.Tunnel.Options.HTTP.BasicAuth == nil || body.Tunnel.Options.HTTP.BasicAuth.Username != "renamed" || !body.Tunnel.Options.HTTP.BasicAuth.PasswordConfigured {
		t.Fatalf("patched HTTP tunnel response = %#v", body)
	}
	stored, err := plane.GetTunnel(context.Background(), tunnel.ID)
	if err != nil || stored.Options.HTTP == nil || stored.Options.HTTP.BasicAuth == nil || stored.Options.HTTP.BasicAuth.Password != password || stored.Options.Transport.BandwidthLimit != nil || stored.Options.Transport.ProxyProtocolVersion != nil || !stored.Options.Transport.UseEncryption || !stored.Options.Transport.UseCompression || stored.Options.HealthCheck != nil || stored.Options.HTTP.HostHeaderRewrite != nil || len(stored.Options.HTTP.RequestHeaders) != 0 || len(stored.Options.HTTP.ResponseHeaders) != 1 || stored.Options.HTTP.ResponseHeaders[0] != (tunnelruntime.TunnelHeader{Name: "X-Response", Value: "fresh"}) {
		t.Fatalf("stored patched tunnel = (%#v, %v)", stored, err)
	}

	reservedClient, err := plane.CreateClient(context.Background(), alice.ID, "Reserved route")
	if err != nil {
		t.Fatalf("CreateClient(reserved) error = %v", err)
	}
	reservedLocation := "/reserved"
	if _, err := plane.CreateTunnel(context.Background(), reservedClient.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"patch.example.test"}, Location: &reservedLocation, LocalPort: 3002}); err != nil {
		t.Fatalf("CreateTunnel(reserved) error = %v", err)
	}
	for _, test := range []struct {
		name        string
		token       string
		origin      string
		contentType string
		body        string
		status      int
		code        string
	}{
		{name: "other owner", token: bobGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"enabled":true}`, status: http.StatusNotFound, code: "NOT_FOUND"},
		{name: "foreign origin", token: aliceGrant.Token, origin: "https://other.example.test", contentType: "application/json", body: `{"enabled":true}`, status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "wrong media", token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "text/plain", body: `{"enabled":true}`, status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "unknown nested field", token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"options":{"http":{"unexpected":true}}}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "reserved route", token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"location":"/reserved"}`, status: http.StatusConflict, code: "RESOURCE_RESERVED"},
		{name: "unauthenticated", token: "", origin: "https://tunnel.example.test", contentType: "application/json", body: `{"enabled":true}`, status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := patchTunnel(test.token, test.origin, test.contentType, test.body)
			if result.Code != test.status {
				t.Fatalf("status = %d, body = %s", result.Code, result.Body.String())
			}
			assertServerHTTPError(t, result.Body.Bytes(), test.code)
		})
	}
	clientAfterRejectedPatches, err := plane.GetClientForOwner(context.Background(), client.ID, alice.ID)
	if err != nil || clientAfterRejectedPatches.DesiredRevision != 2 || clientAfterRejectedPatches.LastAppliedRevision != 0 {
		t.Fatalf("client revisions after rejected patches = (%#v, %v)", clientAfterRejectedPatches, err)
	}

	protocolResponse := patchTunnel(aliceGrant.Token, "", "application/json", `{"protocol":"tcp","serverPort":20001,"options":{"http":null}}`)
	if protocolResponse.Code != http.StatusOK {
		t.Fatalf("PATCH protocol status = %d, body = %s", protocolResponse.Code, protocolResponse.Body.String())
	}
	var protocolBody patchedTunnelResponse
	if err := json.Unmarshal(protocolResponse.Body.Bytes(), &protocolBody); err != nil {
		t.Fatalf("decode protocol patch response: %v", err)
	}
	if protocolBody.Tunnel.Protocol != tunnelruntime.TunnelProtocolTCP || protocolBody.Tunnel.ServerPort == nil || *protocolBody.Tunnel.ServerPort != 20001 || len(protocolBody.Tunnel.CustomDomains) != 0 || strings.Contains(protocolResponse.Body.String(), "customDomains") {
		t.Fatalf("protocol patch response = %#v", protocolBody)
	}
	clientAfterProtocolPatch, err := plane.GetClientForOwner(context.Background(), client.ID, alice.ID)
	if err != nil || clientAfterProtocolPatch.DesiredRevision != 3 || clientAfterProtocolPatch.LastAppliedRevision != 0 {
		t.Fatalf("client revisions after protocol patch = (%#v, %v)", clientAfterProtocolPatch, err)
	}
}

func TestServerHTTPHandlerDeletesOwnedTunnelAndReleasesReservation(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := accounts.CreateLocalAccount(context.Background(), "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), alice.ID, "Laptop")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	tunnel, err := plane.CreateTunnel(context.Background(), client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"released-tunnel.example.test"}, LocalPort: 3000})
	if err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	bobGrant, err := sessions.SignIn(context.Background(), "bob", "bob-secret")
	if err != nil {
		t.Fatalf("SignIn(bob) error = %v", err)
	}

	deleteTunnel := func(method, token, origin string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "http://tunnel.example.test/api/tunnels/"+tunnel.ID, nil)
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, test := range []struct {
		name   string
		method string
		token  string
		origin string
		status int
		code   string
	}{
		{name: "other owner", method: http.MethodDelete, token: bobGrant.Token, origin: "https://tunnel.example.test", status: http.StatusNotFound, code: "NOT_FOUND"},
		{name: "foreign origin", method: http.MethodDelete, token: aliceGrant.Token, origin: "https://other.example.test", status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "unsupported method", method: http.MethodGet, token: aliceGrant.Token, origin: "https://tunnel.example.test", status: http.StatusMethodNotAllowed, code: "METHOD_NOT_ALLOWED"},
		{name: "unauthenticated", method: http.MethodDelete, origin: "https://tunnel.example.test", status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := deleteTunnel(test.method, test.token, test.origin)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertServerHTTPError(t, response.Body.Bytes(), test.code)
		})
	}

	response := deleteTunnel(http.MethodDelete, aliceGrant.Token, "")
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("DELETE /api/tunnels/:id = (%d, %q)", response.Code, response.Body.String())
	}
	assertServerHTTPNoContentHeaders(t, response.Result())
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != aliceGrant.Token || cookies[0].MaxAge < 1 {
		t.Fatalf("tunnel delete session refresh cookie = %#v", cookies)
	}
	assertServerDomainCode(t, func() error {
		_, err := plane.GetTunnel(context.Background(), tunnel.ID)
		return err
	}(), "NOT_FOUND")
	storedClient, err := plane.GetClientForOwner(context.Background(), client.ID, alice.ID)
	if err != nil || storedClient.DesiredRevision != 2 || storedClient.LastAppliedRevision != 0 {
		t.Fatalf("client after tunnel deletion = (%#v, %v)", storedClient, err)
	}
	replacement, err := plane.CreateClient(context.Background(), alice.ID, "Replacement")
	if err != nil {
		t.Fatalf("CreateClient(replacement) error = %v", err)
	}
	if _, err := plane.CreateTunnel(context.Background(), replacement.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"released-tunnel.example.test"}, LocalPort: 3001}); err != nil {
		t.Fatalf("CreateTunnel(released reservation) error = %v", err)
	}
}

func TestServerHTTPHandlerPreviewsScopedTunnelImportWithoutPersistingState(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := accounts.CreateLocalAccount(context.Background(), "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), alice.ID, "Laptop")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	bobGrant, err := sessions.SignIn(context.Background(), "bob", "bob-secret")
	if err != nil {
		t.Fatalf("SignIn(bob) error = %v", err)
	}

	preview := func(method, token, origin, contentType, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "http://tunnel.example.test/api/clients/"+client.ID+"/tunnels/import/preview", strings.NewReader(body))
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	payload, err := json.Marshal(struct {
		Source string `json:"source"`
	}{Source: serverImportSource})
	if err != nil {
		t.Fatalf("json.Marshal(import preview) error = %v", err)
	}
	response := preview(http.MethodPost, aliceGrant.Token, "https://tunnel.example.test", "application/json", string(payload))
	if response.Code != http.StatusOK {
		t.Fatalf("POST import preview status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	if strings.Contains(response.Body.String(), "secret-value") {
		t.Fatalf("import preview leaked Basic Auth password: %s", response.Body.String())
	}
	var body struct {
		Version    int                  `json:"version"`
		Candidates []map[string]any     `json:"candidates"`
		Ignored    []TunnelImportNotice `json:"ignored"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode import preview: %v", err)
	}
	if body.Version != 1 || len(body.Candidates) != 4 || len(body.Ignored) == 0 {
		t.Fatalf("import preview = %#v", body)
	}
	first := body.Candidates[0]
	basicAuth, ok := first["basicAuth"].(map[string]any)
	if !ok || first["id"] != "proxy-0-location-0" || first["protocol"] != string(tunnelruntime.TunnelProtocolHTTP) || first["location"] != "/api" || basicAuth["username"] != "operator" || basicAuth["passwordConfigured"] != true || basicAuth["password"] != nil {
		t.Fatalf("first import candidate = %#v", first)
	}
	if !hasTunnelImportNotice(body.Ignored, "", "Client connection settings are not imported") {
		t.Fatalf("import preview notices = %#v", body.Ignored)
	}
	storedClient, err := plane.GetClientForOwner(context.Background(), client.ID, alice.ID)
	if err != nil || storedClient.DesiredRevision != 0 || storedClient.LastAppliedRevision != 0 {
		t.Fatalf("client after preview = (%#v, %v)", storedClient, err)
	}
	storedTunnels, err := plane.ListTunnels(context.Background(), client.ID)
	if err != nil || len(storedTunnels) != 0 {
		t.Fatalf("tunnels after preview = (%#v, %v)", storedTunnels, err)
	}

	for _, test := range []struct {
		name        string
		method      string
		token       string
		origin      string
		contentType string
		body        string
		status      int
		code        string
	}{
		{name: "other owner", method: http.MethodPost, token: bobGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: string(payload), status: http.StatusNotFound, code: "NOT_FOUND"},
		{name: "foreign origin", method: http.MethodPost, token: aliceGrant.Token, origin: "https://other.example.test", contentType: "application/json", body: string(payload), status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "wrong media", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "text/plain", body: string(payload), status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "empty source", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"source":""}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "oversize source", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"source":"` + strings.Repeat("x", serverTunnelImportSourceLimit+1) + `"}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "unknown field", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"source":"[[proxies]]","extra":true}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "invalid TOML", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"source":"proxies = ["}`, status: http.StatusBadRequest, code: "INVALID_CONFIG"},
		{name: "unsupported method", method: http.MethodGet, token: aliceGrant.Token, origin: "https://tunnel.example.test", status: http.StatusMethodNotAllowed, code: "METHOD_NOT_ALLOWED"},
		{name: "unauthenticated", method: http.MethodPost, origin: "https://tunnel.example.test", contentType: "application/json", body: string(payload), status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := preview(test.method, test.token, test.origin, test.contentType, test.body)
			if result.Code != test.status {
				t.Fatalf("status = %d, body = %s", result.Code, result.Body.String())
			}
			assertServerHTTPError(t, result.Body.Bytes(), test.code)
		})
	}
	storedClient, err = plane.GetClientForOwner(context.Background(), client.ID, alice.ID)
	if err != nil || storedClient.DesiredRevision != 0 || storedClient.LastAppliedRevision != 0 {
		t.Fatalf("client after rejected previews = (%#v, %v)", storedClient, err)
	}
}

func TestServerHTTPHandlerImportsSelectedScopedTunnelsAtomically(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := accounts.CreateLocalAccount(context.Background(), "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), alice.ID, "Laptop")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	bobGrant, err := sessions.SignIn(context.Background(), "bob", "bob-secret")
	if err != nil {
		t.Fatalf("SignIn(bob) error = %v", err)
	}

	encodeImport := func(source string, candidateIDs []string) string {
		t.Helper()
		payload, err := json.Marshal(struct {
			Source       string   `json:"source"`
			CandidateIDs []string `json:"candidateIds"`
		}{Source: source, CandidateIDs: candidateIDs})
		if err != nil {
			t.Fatalf("json.Marshal(import) error = %v", err)
		}
		return string(payload)
	}
	importTunnels := func(clientID, method, token, origin, contentType, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "http://tunnel.example.test/api/clients/"+clientID+"/tunnels/import", strings.NewReader(body))
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	payload := encodeImport(serverImportSource, []string{"proxy-0-location-0", "proxy-1", "proxy-2"})
	for _, test := range []struct {
		name        string
		method      string
		token       string
		origin      string
		contentType string
		body        string
		status      int
		code        string
	}{
		{name: "other owner", method: http.MethodPost, token: bobGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: payload, status: http.StatusNotFound, code: "NOT_FOUND"},
		{name: "foreign origin", method: http.MethodPost, token: aliceGrant.Token, origin: "https://other.example.test", contentType: "application/json", body: payload, status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "wrong media", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "text/plain", body: payload, status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "missing candidates", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"source":"[[proxies]]"}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "empty candidates", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: encodeImport(serverImportSource, []string{}), status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "unknown field", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"source":"[[proxies]]","candidateIds":["proxy-0"],"extra":true}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "invalid TOML", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: encodeImport("proxies = [", []string{"proxy-0"}), status: http.StatusBadRequest, code: "INVALID_CONFIG"},
		{name: "stale candidate", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: encodeImport(serverImportSource, []string{"stale"}), status: http.StatusBadRequest, code: "INVALID_TUNNEL"},
		{name: "duplicate candidate", method: http.MethodPost, token: aliceGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: encodeImport(serverImportSource, []string{"proxy-1", "proxy-1"}), status: http.StatusBadRequest, code: "INVALID_TUNNEL"},
		{name: "unsupported method", method: http.MethodGet, token: aliceGrant.Token, origin: "https://tunnel.example.test", status: http.StatusMethodNotAllowed, code: "METHOD_NOT_ALLOWED"},
		{name: "unauthenticated", method: http.MethodPost, origin: "https://tunnel.example.test", contentType: "application/json", body: payload, status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := importTunnels(client.ID, test.method, test.token, test.origin, test.contentType, test.body)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertServerHTTPError(t, response.Body.Bytes(), test.code)
		})
	}
	storedClient, err := plane.GetClientForOwner(context.Background(), client.ID, alice.ID)
	if err != nil || storedClient.DesiredRevision != 0 || storedClient.LastAppliedRevision != 0 {
		t.Fatalf("client after rejected imports = (%#v, %v)", storedClient, err)
	}

	conflictingClient, err := plane.CreateClient(context.Background(), alice.ID, "Conflict")
	if err != nil {
		t.Fatalf("CreateClient(conflict) error = %v", err)
	}
	conflictLocation := "/api"
	if _, err := plane.CreateTunnel(context.Background(), conflictingClient.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"conflict.example.test"}, Location: &conflictLocation, LocalPort: 3001}); err != nil {
		t.Fatalf("CreateTunnel(conflict) error = %v", err)
	}
	conflictSource := strings.Replace(serverImportSource, "app.example.com", "conflict.example.test", 1)
	conflictResponse := importTunnels(conflictingClient.ID, http.MethodPost, aliceGrant.Token, "", "application/json", encodeImport(conflictSource, []string{"proxy-1", "proxy-0-location-0"}))
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("POST conflicting import = (%d, %s)", conflictResponse.Code, conflictResponse.Body.String())
	}
	assertServerHTTPError(t, conflictResponse.Body.Bytes(), "RESOURCE_RESERVED")
	conflictTunnels, err := plane.ListTunnels(context.Background(), conflictingClient.ID)
	if err != nil || len(conflictTunnels) != 1 {
		t.Fatalf("conflict tunnels after rollback = (%#v, %v)", conflictTunnels, err)
	}
	conflictStoredClient, err := plane.GetClientForOwner(context.Background(), conflictingClient.ID, alice.ID)
	if err != nil || conflictStoredClient.DesiredRevision != 1 {
		t.Fatalf("conflict client after rollback = (%#v, %v)", conflictStoredClient, err)
	}

	response := importTunnels(client.ID, http.MethodPost, aliceGrant.Token, "", "application/json", payload)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST selected import status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	if strings.Contains(response.Body.String(), "secret-value") {
		t.Fatalf("selected import leaked Basic Auth password: %s", response.Body.String())
	}
	var body struct {
		Version int              `json:"version"`
		Tunnels []map[string]any `json:"tunnels"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode selected import: %v", err)
	}
	if body.Version != 1 || len(body.Tunnels) != 3 {
		t.Fatalf("selected import body = %#v", body)
	}
	first := body.Tunnels[0]
	options, ok := first["options"].(map[string]any)
	if !ok {
		t.Fatalf("imported HTTP options = %#v", first["options"])
	}
	httpOptions, ok := options["http"].(map[string]any)
	if !ok {
		t.Fatalf("imported HTTP options = %#v", options["http"])
	}
	basicAuth, ok := httpOptions["basicAuth"].(map[string]any)
	if !ok || first["protocol"] != string(tunnelruntime.TunnelProtocolHTTP) || first["enabled"] != false || first["state"] != string(ServerTunnelDisabled) || basicAuth["username"] != "operator" || basicAuth["passwordConfigured"] != true || basicAuth["password"] != nil {
		t.Fatalf("imported HTTP tunnel = %#v", first)
	}
	storedClient, err = plane.GetClientForOwner(context.Background(), client.ID, alice.ID)
	if err != nil || storedClient.DesiredRevision != 1 || storedClient.LastAppliedRevision != 0 {
		t.Fatalf("client after selected import = (%#v, %v)", storedClient, err)
	}
	storedTunnels, err := plane.ListTunnels(context.Background(), client.ID)
	var storedHTTP *tunnelruntime.TunnelDefinition
	for index := range storedTunnels {
		if storedTunnels[index].Protocol == tunnelruntime.TunnelProtocolHTTP {
			storedHTTP = &storedTunnels[index]
			break
		}
	}
	if err != nil || len(storedTunnels) != 3 || storedHTTP == nil || storedHTTP.Enabled || storedHTTP.Options.HTTP == nil || storedHTTP.Options.HTTP.BasicAuth == nil || storedHTTP.Options.HTTP.BasicAuth.Password != "secret-value" {
		t.Fatalf("stored selected tunnels = (%#v, %v)", storedTunnels, err)
	}
}

func TestServerHTTPHandlerListsAccountsForAdministrators(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	plane := openServerControlPlane(t, state)
	if _, err := plane.CreateClient(context.Background(), alice.ID, "Alice laptop"); err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	adminGrant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}

	requestAccounts := func(method, token string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "http://tunnel.example.test/api/accounts", nil)
		if token != "" {
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	response := requestAccounts(http.MethodGet, adminGrant.Token)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/accounts status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	if strings.Contains(response.Body.String(), "environment-secret") || strings.Contains(response.Body.String(), "alice-secret") || strings.Contains(response.Body.String(), "passwordHash") {
		t.Fatalf("account list leaked credentials: %s", response.Body.String())
	}
	var body struct {
		Version  int                         `json:"version"`
		Accounts []serverHTTPAccountListView `json:"accounts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode account list: %v", err)
	}
	var environment, aliceView *serverHTTPAccountListView
	for index := range body.Accounts {
		account := &body.Accounts[index]
		switch account.ID {
		case environmentAdministratorID:
			environment = account
		case alice.ID:
			aliceView = account
		}
	}
	if body.Version != 1 || environment == nil || environment.Kind != AccountKindEnvironment || !environment.ManagedByEnvironment || environment.ClientCount != 0 || aliceView == nil || aliceView.Kind != AccountKindLocal || aliceView.Username != "alice" || aliceView.Role != AccountRoleUser || aliceView.ManagedByEnvironment || aliceView.ClientCount != 1 {
		t.Fatalf("account list = %#v", body)
	}

	for _, test := range []struct {
		name   string
		method string
		token  string
		status int
		code   string
	}{
		{name: "non-admin", method: http.MethodGet, token: aliceGrant.Token, status: http.StatusForbidden, code: "FORBIDDEN"},
		{name: "unsupported method", method: http.MethodPut, token: adminGrant.Token, status: http.StatusMethodNotAllowed, code: "METHOD_NOT_ALLOWED"},
		{name: "unauthenticated", method: http.MethodGet, status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := requestAccounts(test.method, test.token)
			if result.Code != test.status {
				t.Fatalf("status = %d, body = %s", result.Code, result.Body.String())
			}
			assertServerHTTPError(t, result.Body.Bytes(), test.code)
		})
	}
}

func TestServerHTTPHandlerCreatesLocalAccountsForAdministrators(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	operator, err := accounts.CreateLocalAccount(context.Background(), "operator", "operator-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(operator) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	adminGrant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	operatorGrant, err := sessions.SignIn(context.Background(), "operator", "operator-secret")
	if err != nil {
		t.Fatalf("SignIn(operator) error = %v", err)
	}

	createAccount := func(token, origin, contentType, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/accounts", strings.NewReader(body))
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	response := createAccount(adminGrant.Token, "https://tunnel.example.test", "application/json", `{"username":"alice","password":"alice-secret"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/accounts status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	if strings.Contains(response.Body.String(), "alice-secret") || strings.Contains(response.Body.String(), "passwordHash") {
		t.Fatalf("account creation leaked credential: %s", response.Body.String())
	}
	var body struct {
		Version int                       `json:"version"`
		Account serverHTTPAccountListView `json:"account"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode account creation: %v", err)
	}
	if body.Version != 1 || body.Account.ID == "" || body.Account.Kind != AccountKindLocal || body.Account.Username != "alice" || body.Account.Role != AccountRoleUser || body.Account.ManagedByEnvironment || body.Account.ClientCount != 0 {
		t.Fatalf("created account = %#v", body)
	}
	if _, err := sessions.SignIn(context.Background(), "alice", "alice-secret"); err != nil {
		t.Fatalf("SignIn(created account) error = %v", err)
	}
	adminResponse := createAccount(adminGrant.Token, "", "application/json", `{"username":"second-admin","password":"second-admin-secret","role":"admin"}`)
	if adminResponse.Code != http.StatusCreated {
		t.Fatalf("POST admin account status = %d, body = %s", adminResponse.Code, adminResponse.Body.String())
	}
	var adminBody struct {
		Account serverHTTPAccountListView `json:"account"`
	}
	if err := json.Unmarshal(adminResponse.Body.Bytes(), &adminBody); err != nil || adminBody.Account.Role != AccountRoleAdmin {
		t.Fatalf("created administrator = (%#v, %v)", adminBody, err)
	}

	for _, test := range []struct {
		name        string
		token       string
		origin      string
		contentType string
		body        string
		status      int
		code        string
	}{
		{name: "non-admin", token: operatorGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"username":"blocked","password":"blocked-secret"}`, status: http.StatusForbidden, code: "FORBIDDEN"},
		{name: "foreign origin", token: adminGrant.Token, origin: "https://other.example.test", contentType: "application/json", body: `{"username":"blocked","password":"blocked-secret"}`, status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "wrong media", token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "text/plain", body: `{"username":"blocked","password":"blocked-secret"}`, status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "null role", token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"username":"blocked","password":"blocked-secret","role":null}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "invalid role", token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"username":"blocked","password":"blocked-secret","role":"owner"}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "unknown field", token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"username":"blocked","password":"blocked-secret","extra":true}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "duplicate username", token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"username":"Alice","password":"different-secret"}`, status: http.StatusConflict, code: "USERNAME_TAKEN"},
		{name: "unauthenticated", origin: "https://tunnel.example.test", contentType: "application/json", body: `{"username":"blocked","password":"blocked-secret"}`, status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := createAccount(test.token, test.origin, test.contentType, test.body)
			if result.Code != test.status {
				t.Fatalf("status = %d, body = %s", result.Code, result.Body.String())
			}
			assertServerHTTPError(t, result.Body.Bytes(), test.code)
		})
	}
	if _, err := sessions.SignIn(context.Background(), "blocked", "blocked-secret"); err == nil {
		t.Fatal("SignIn(rejected account) error = nil")
	} else {
		assertServerDomainCode(t, err, "AUTHENTICATION_FAILED")
	}
	if account, err := accounts.GetAccount(context.Background(), operator.ID); err != nil || account.Role != AccountRoleUser {
		t.Fatalf("existing operator after rejected creates = (%#v, %v)", account, err)
	}
}

func TestServerHTTPHandlerChangesLocalAccountRolesForAdministrators(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	operator, err := accounts.CreateLocalAccount(context.Background(), "operator", "operator-secret", AccountRoleAdmin)
	if err != nil {
		t.Fatalf("CreateLocalAccount(operator) error = %v", err)
	}
	_, err = accounts.CreateLocalAccount(context.Background(), "viewer", "viewer-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(viewer) error = %v", err)
	}
	plane := openServerControlPlane(t, state)
	if _, err := plane.CreateClient(context.Background(), alice.ID, "Alice client"); err != nil {
		t.Fatalf("CreateClient(alice) error = %v", err)
	}
	if _, err := plane.CreateClient(context.Background(), operator.ID, "Operator client"); err != nil {
		t.Fatalf("CreateClient(operator) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	adminGrant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	operatorGrant, err := sessions.SignIn(context.Background(), "operator", "operator-secret")
	if err != nil {
		t.Fatalf("SignIn(operator) error = %v", err)
	}
	viewerGrant, err := sessions.SignIn(context.Background(), "viewer", "viewer-secret")
	if err != nil {
		t.Fatalf("SignIn(viewer) error = %v", err)
	}

	changeRole := func(accountID, token, origin, contentType, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPatch, "http://tunnel.example.test/api/accounts/"+accountID, strings.NewReader(body))
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	response := changeRole(alice.ID, adminGrant.Token, "https://tunnel.example.test", "application/json", `{"role":"admin"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH /api/accounts/:id status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	var body struct {
		Version int                       `json:"version"`
		Account serverHTTPAccountListView `json:"account"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode role update: %v", err)
	}
	if body.Version != 1 || body.Account.ID != alice.ID || body.Account.Role != AccountRoleAdmin || body.Account.ClientCount != 1 || body.Account.ManagedByEnvironment {
		t.Fatalf("updated account = %#v", body)
	}
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != adminGrant.Token || cookies[0].MaxAge < 1 {
		t.Fatalf("administrator refresh cookie = %#v", cookies)
	}
	if session, err := sessions.Resume(context.Background(), aliceGrant.Token); err != nil || session != nil {
		t.Fatalf("changed account session = (%#v, %v)", session, err)
	}

	selfResponse := changeRole(operator.ID, operatorGrant.Token, "", "application/json", `{"role":"user"}`)
	if selfResponse.Code != http.StatusOK {
		t.Fatalf("PATCH self account status = %d, body = %s", selfResponse.Code, selfResponse.Body.String())
	}
	assertServerHTTPHeaders(t, selfResponse.Result())
	var selfBody struct {
		Account serverHTTPAccountListView `json:"account"`
	}
	if err := json.Unmarshal(selfResponse.Body.Bytes(), &selfBody); err != nil || selfBody.Account.ID != operator.ID || selfBody.Account.Role != AccountRoleUser || selfBody.Account.ClientCount != 1 {
		t.Fatalf("self role update = (%#v, %v)", selfBody, err)
	}
	if cookies := selfResponse.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != "" || cookies[0].MaxAge >= 0 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatalf("self expired cookie = %#v", cookies)
	}
	if session, err := sessions.Resume(context.Background(), operatorGrant.Token); err != nil || session != nil {
		t.Fatalf("self changed session = (%#v, %v)", session, err)
	}

	for _, test := range []struct {
		name        string
		accountID   string
		method      string
		token       string
		origin      string
		contentType string
		body        string
		status      int
		code        string
	}{
		{name: "managed account", accountID: environmentAdministratorID, method: http.MethodPatch, token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"role":"user"}`, status: http.StatusConflict, code: "MANAGED_ACCOUNT"},
		{name: "non-admin", accountID: alice.ID, method: http.MethodPatch, token: viewerGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"role":"user"}`, status: http.StatusForbidden, code: "FORBIDDEN"},
		{name: "foreign origin", accountID: alice.ID, method: http.MethodPatch, token: adminGrant.Token, origin: "https://other.example.test", contentType: "application/json", body: `{"role":"user"}`, status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "wrong media", accountID: alice.ID, method: http.MethodPatch, token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "text/plain", body: `{"role":"user"}`, status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "missing role", accountID: alice.ID, method: http.MethodPatch, token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "null role", accountID: alice.ID, method: http.MethodPatch, token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"role":null}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "invalid role", accountID: alice.ID, method: http.MethodPatch, token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"role":"owner"}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "unknown field", accountID: alice.ID, method: http.MethodPatch, token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"role":"user","extra":true}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "unsupported method", accountID: alice.ID, method: http.MethodPut, token: adminGrant.Token, status: http.StatusMethodNotAllowed, code: "METHOD_NOT_ALLOWED"},
		{name: "unauthenticated", accountID: alice.ID, method: http.MethodPatch, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"role":"user"}`, status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://tunnel.example.test/api/accounts/"+test.accountID, strings.NewReader(test.body))
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.token != "" {
				request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: test.token})
			}
			result := httptest.NewRecorder()
			handler.ServeHTTP(result, request)
			if result.Code != test.status {
				t.Fatalf("status = %d, body = %s", result.Code, result.Body.String())
			}
			assertServerHTTPError(t, result.Body.Bytes(), test.code)
		})
	}
	if account, err := accounts.GetAccount(context.Background(), alice.ID); err != nil || account.Role != AccountRoleAdmin {
		t.Fatalf("alice after rejected updates = (%#v, %v)", account, err)
	}
}

func TestServerHTTPHandlerResetsLocalAccountPasswordsForAdministrators(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	operator, err := accounts.CreateLocalAccount(context.Background(), "operator", "operator-secret", AccountRoleAdmin)
	if err != nil {
		t.Fatalf("CreateLocalAccount(operator) error = %v", err)
	}
	_, err = accounts.CreateLocalAccount(context.Background(), "viewer", "viewer-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(viewer) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	adminGrant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceCurrent, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice current) error = %v", err)
	}
	aliceOther, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice other) error = %v", err)
	}
	operatorGrant, err := sessions.SignIn(context.Background(), "operator", "operator-secret")
	if err != nil {
		t.Fatalf("SignIn(operator) error = %v", err)
	}
	viewerGrant, err := sessions.SignIn(context.Background(), "viewer", "viewer-secret")
	if err != nil {
		t.Fatalf("SignIn(viewer) error = %v", err)
	}

	resetPassword := func(method, accountID, token, origin, contentType, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "http://tunnel.example.test/api/accounts/"+accountID+"/password", strings.NewReader(body))
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	response := resetPassword(http.MethodPut, alice.ID, adminGrant.Token, "https://tunnel.example.test", "application/json", `{"password":"replacement-secret"}`)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("PUT /api/accounts/:id/password status = %d, body = %q", response.Code, response.Body.String())
	}
	assertServerHTTPNoContentHeaders(t, response.Result())
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != adminGrant.Token || cookies[0].MaxAge < 1 {
		t.Fatalf("administrator reset refresh cookie = %#v", cookies)
	}
	for _, token := range []string{aliceCurrent.Token, aliceOther.Token} {
		if session, err := sessions.Resume(context.Background(), token); err != nil || session != nil {
			t.Fatalf("Resume(revoked alice token) = (%#v, %v), want nil", session, err)
		}
	}
	if _, err := sessions.SignIn(context.Background(), "alice", "alice-secret"); err == nil {
		t.Fatal("SignIn(old alice password) error = nil")
	}
	if _, err := sessions.SignIn(context.Background(), "alice", "replacement-secret"); err != nil {
		t.Fatalf("SignIn(replacement alice password) error = %v", err)
	}

	selfResponse := resetPassword(http.MethodPut, operator.ID, operatorGrant.Token, "", "application/json", `{"password":"operator-replacement-secret"}`)
	if selfResponse.Code != http.StatusNoContent || selfResponse.Body.Len() != 0 {
		t.Fatalf("PUT self account password status = %d, body = %q", selfResponse.Code, selfResponse.Body.String())
	}
	assertServerHTTPNoContentHeaders(t, selfResponse.Result())
	if cookies := selfResponse.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != "" || cookies[0].MaxAge >= 0 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatalf("self password-reset cookie = %#v", cookies)
	}
	if session, err := sessions.Resume(context.Background(), operatorGrant.Token); err != nil || session != nil {
		t.Fatalf("Resume(self-reset token) = (%#v, %v), want nil", session, err)
	}
	if _, err := sessions.SignIn(context.Background(), "operator", "operator-secret"); err == nil {
		t.Fatal("SignIn(old operator password) error = nil")
	}
	if _, err := sessions.SignIn(context.Background(), "operator", "operator-replacement-secret"); err != nil {
		t.Fatalf("SignIn(replacement operator password) error = %v", err)
	}
	managedResponse := resetPassword(http.MethodPut, environmentAdministratorID, adminGrant.Token, "https://tunnel.example.test", "application/json", `{"password":"replacement-secret"}`)
	if managedResponse.Code != http.StatusConflict {
		t.Fatalf("PUT managed account password status = %d, body = %s", managedResponse.Code, managedResponse.Body.String())
	}
	assertServerHTTPError(t, managedResponse.Body.Bytes(), "MANAGED_ACCOUNT")
	if session, err := sessions.Resume(context.Background(), adminGrant.Token); err != nil || session == nil {
		t.Fatalf("Resume(administrator after managed reset) = (%#v, %v)", session, err)
	}

	for _, test := range []struct {
		name        string
		method      string
		accountID   string
		token       string
		origin      string
		contentType string
		body        string
		status      int
		code        string
	}{
		{name: "missing account", method: http.MethodPut, accountID: "missing", token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"password":"replacement-secret"}`, status: http.StatusNotFound, code: "NOT_FOUND"},
		{name: "non-admin", method: http.MethodPut, accountID: alice.ID, token: viewerGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"password":"replacement-secret"}`, status: http.StatusForbidden, code: "FORBIDDEN"},
		{name: "foreign origin", method: http.MethodPut, accountID: alice.ID, token: adminGrant.Token, origin: "https://other.example.test", contentType: "application/json", body: `{"password":"replacement-secret"}`, status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "wrong media", method: http.MethodPut, accountID: alice.ID, token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "text/plain", body: `{"password":"replacement-secret"}`, status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "missing password", method: http.MethodPut, accountID: alice.ID, token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "null password", method: http.MethodPut, accountID: alice.ID, token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"password":null}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "unknown field", method: http.MethodPut, accountID: alice.ID, token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"password":"replacement-secret","extra":true}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "invalid password", method: http.MethodPut, accountID: alice.ID, token: adminGrant.Token, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"password":"tiny"}`, status: http.StatusBadRequest, code: "INVALID_ACCOUNT"},
		{name: "unsupported method", method: http.MethodPatch, accountID: alice.ID, token: adminGrant.Token, status: http.StatusMethodNotAllowed, code: "METHOD_NOT_ALLOWED"},
		{name: "unauthenticated", method: http.MethodPut, accountID: alice.ID, origin: "https://tunnel.example.test", contentType: "application/json", body: `{"password":"replacement-secret"}`, status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := resetPassword(test.method, test.accountID, test.token, test.origin, test.contentType, test.body)
			if result.Code != test.status {
				t.Fatalf("status = %d, body = %s", result.Code, result.Body.String())
			}
			assertServerHTTPError(t, result.Body.Bytes(), test.code)
		})
	}
	if _, err := sessions.SignIn(context.Background(), "alice", "replacement-secret"); err != nil {
		t.Fatalf("SignIn(alice after rejected resets) error = %v", err)
	}
}

func TestServerHTTPHandlerDeletesEmptyLocalAccountsForAdministrators(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	operator, err := accounts.CreateLocalAccount(context.Background(), "operator", "operator-secret", AccountRoleAdmin)
	if err != nil {
		t.Fatalf("CreateLocalAccount(operator) error = %v", err)
	}
	owner, err := accounts.CreateLocalAccount(context.Background(), "owner", "owner-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(owner) error = %v", err)
	}
	_, err = accounts.CreateLocalAccount(context.Background(), "viewer", "viewer-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(viewer) error = %v", err)
	}
	plane := openServerControlPlane(t, state)
	ownedClient, err := plane.CreateClient(context.Background(), owner.ID, "Owned client")
	if err != nil {
		t.Fatalf("CreateClient(owner) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	adminGrant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceCurrent, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice current) error = %v", err)
	}
	aliceOther, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice other) error = %v", err)
	}
	operatorGrant, err := sessions.SignIn(context.Background(), "operator", "operator-secret")
	if err != nil {
		t.Fatalf("SignIn(operator) error = %v", err)
	}
	ownerGrant, err := sessions.SignIn(context.Background(), "owner", "owner-secret")
	if err != nil {
		t.Fatalf("SignIn(owner) error = %v", err)
	}
	viewerGrant, err := sessions.SignIn(context.Background(), "viewer", "viewer-secret")
	if err != nil {
		t.Fatalf("SignIn(viewer) error = %v", err)
	}

	deleteAccount := func(method, accountID, token, origin string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "http://tunnel.example.test/api/accounts/"+accountID, nil)
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	response := deleteAccount(http.MethodDelete, alice.ID, adminGrant.Token, "https://tunnel.example.test")
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("DELETE /api/accounts/:id status = %d, body = %q", response.Code, response.Body.String())
	}
	assertServerHTTPNoContentHeaders(t, response.Result())
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != adminGrant.Token || cookies[0].MaxAge < 1 {
		t.Fatalf("administrator delete refresh cookie = %#v", cookies)
	}
	for _, token := range []string{aliceCurrent.Token, aliceOther.Token} {
		if session, err := sessions.Resume(context.Background(), token); err != nil || session != nil {
			t.Fatalf("Resume(deleted alice token) = (%#v, %v), want nil", session, err)
		}
	}
	if _, err := sessions.SignIn(context.Background(), "alice", "alice-secret"); err == nil {
		t.Fatal("SignIn(deleted alice) error = nil")
	}

	selfResponse := deleteAccount(http.MethodDelete, operator.ID, operatorGrant.Token, "")
	if selfResponse.Code != http.StatusNoContent || selfResponse.Body.Len() != 0 {
		t.Fatalf("DELETE self account status = %d, body = %q", selfResponse.Code, selfResponse.Body.String())
	}
	assertServerHTTPNoContentHeaders(t, selfResponse.Result())
	if cookies := selfResponse.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != "" || cookies[0].MaxAge >= 0 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatalf("self account-delete cookie = %#v", cookies)
	}
	if session, err := sessions.Resume(context.Background(), operatorGrant.Token); err != nil || session != nil {
		t.Fatalf("Resume(deleted self token) = (%#v, %v), want nil", session, err)
	}
	if _, err := sessions.SignIn(context.Background(), "operator", "operator-secret"); err == nil {
		t.Fatal("SignIn(deleted operator) error = nil")
	}

	for _, test := range []struct {
		name      string
		method    string
		accountID string
		token     string
		origin    string
		status    int
		code      string
	}{
		{name: "managed account", method: http.MethodDelete, accountID: environmentAdministratorID, token: adminGrant.Token, origin: "https://tunnel.example.test", status: http.StatusConflict, code: "MANAGED_ACCOUNT"},
		{name: "missing account", method: http.MethodDelete, accountID: "missing", token: adminGrant.Token, origin: "https://tunnel.example.test", status: http.StatusNotFound, code: "NOT_FOUND"},
		{name: "owned account", method: http.MethodDelete, accountID: owner.ID, token: adminGrant.Token, origin: "https://tunnel.example.test", status: http.StatusConflict, code: "ACCOUNT_NOT_EMPTY"},
		{name: "non-admin", method: http.MethodDelete, accountID: owner.ID, token: viewerGrant.Token, origin: "https://tunnel.example.test", status: http.StatusForbidden, code: "FORBIDDEN"},
		{name: "foreign origin", method: http.MethodDelete, accountID: owner.ID, token: adminGrant.Token, origin: "https://other.example.test", status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "unsupported method", method: http.MethodPost, accountID: owner.ID, token: adminGrant.Token, status: http.StatusMethodNotAllowed, code: "METHOD_NOT_ALLOWED"},
		{name: "unauthenticated", method: http.MethodDelete, accountID: owner.ID, origin: "https://tunnel.example.test", status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := deleteAccount(test.method, test.accountID, test.token, test.origin)
			if result.Code != test.status {
				t.Fatalf("status = %d, body = %s", result.Code, result.Body.String())
			}
			assertServerHTTPError(t, result.Body.Bytes(), test.code)
		})
	}
	if session, err := sessions.Resume(context.Background(), ownerGrant.Token); err != nil || session == nil {
		t.Fatalf("Resume(owner after rejected deletion) = (%#v, %v)", session, err)
	}
	if client, err := plane.GetClientForOwner(context.Background(), ownedClient.ID, owner.ID); err != nil || client.ID != ownedClient.ID {
		t.Fatalf("GetClientForOwner(owner) = (%#v, %v)", client, err)
	}
}

type serverHTTPTestClientRuntime struct {
	states map[string]ServerClientRuntimeState
}

func (runtime serverHTTPTestClientRuntime) State(clientID string) ServerClientRuntimeState {
	return runtime.states[clientID]
}

type serverHTTPTestStateProvider struct {
	state ServerHTTPState
}

func (provider serverHTTPTestStateProvider) State() ServerHTTPState {
	return provider.state
}

type serverHTTPTestFRPSController struct {
	calls   []ServerFRPSAction
	err     error
	changes *serverHTTPTestFRPSChangeObserver
}

func (controller *serverHTTPTestFRPSController) Start(context.Context) error {
	controller.calls = append(controller.calls, ServerFRPSActionStart)
	if controller.err == nil && controller.changes != nil {
		controller.changes.notify()
	}
	return controller.err
}

func (controller *serverHTTPTestFRPSController) Stop() error {
	controller.calls = append(controller.calls, ServerFRPSActionStop)
	if controller.err == nil && controller.changes != nil {
		controller.changes.notify()
	}
	return controller.err
}

func (controller *serverHTTPTestFRPSController) Restart(context.Context) error {
	controller.calls = append(controller.calls, ServerFRPSActionRestart)
	if controller.err == nil && controller.changes != nil {
		controller.changes.notify()
	}
	return controller.err
}

type serverHTTPTestCustom404PageReader struct {
	content string
	reads   int
	err     error
}

func (reader *serverHTTPTestCustom404PageReader) ReadCustom404Page() (string, error) {
	reader.reads++
	if reader.err != nil {
		return "", reader.err
	}
	return reader.content, nil
}

type serverHTTPTestCustom404PageWriter struct {
	contents []string
	err      error
	changes  *serverHTTPTestFRPSChangeObserver
}

func (writer *serverHTTPTestCustom404PageWriter) WriteCustom404Page(content string) error {
	writer.contents = append(writer.contents, content)
	if writer.err == nil && writer.changes != nil {
		writer.changes.notify()
	}
	return writer.err
}

type serverHTTPTestFRPSChangeObserver struct {
	mu        sync.Mutex
	listeners map[uint64]func()
	next      uint64
}

func (observer *serverHTTPTestFRPSChangeObserver) ObserveFRPSChanges(listener func()) func() {
	if listener == nil {
		return func() {}
	}
	observer.mu.Lock()
	if observer.listeners == nil {
		observer.listeners = make(map[uint64]func())
	}
	id := observer.next
	observer.next++
	observer.listeners[id] = listener
	observer.mu.Unlock()
	return func() {
		observer.mu.Lock()
		delete(observer.listeners, id)
		observer.mu.Unlock()
	}
}

func (observer *serverHTTPTestFRPSChangeObserver) notify() {
	observer.mu.Lock()
	listeners := make([]func(), 0, len(observer.listeners))
	for _, listener := range observer.listeners {
		listeners = append(listeners, listener)
	}
	observer.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

func TestServerHTTPHandlerServesScopedStateAndRedactsDeploymentSecrets(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	bob, err := accounts.CreateLocalAccount(ctx, "bob", "bob-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)

	aliceCurrent, err := plane.CreateClient(ctx, alice.ID, "Alice current")
	if err != nil {
		t.Fatalf("CreateClient(alice current) error = %v", err)
	}
	alicePending, err := plane.CreateClient(ctx, alice.ID, "Alice pending")
	if err != nil {
		t.Fatalf("CreateClient(alice pending) error = %v", err)
	}
	bobFailed, err := plane.CreateClient(ctx, bob.ID, "Bob failed")
	if err != nil {
		t.Fatalf("CreateClient(bob failed) error = %v", err)
	}
	for _, input := range []struct {
		clientID string
		domain   string
	}{
		{clientID: aliceCurrent.ID, domain: "current.example.test"},
		{clientID: alicePending.ID, domain: "pending.example.test"},
		{clientID: bobFailed.ID, domain: "failed.example.test"},
	} {
		if _, err := plane.CreateTunnel(ctx, input.clientID, TunnelMutationInput{
			Protocol:      tunnelruntime.TunnelProtocolHTTP,
			CustomDomains: []string{input.domain},
			LocalPort:     3000,
		}); err != nil {
			t.Fatalf("CreateTunnel(%s) error = %v", input.domain, err)
		}
	}
	aliceCurrent, err = plane.GetClient(ctx, aliceCurrent.ID)
	if err != nil {
		t.Fatalf("GetClient(alice current) error = %v", err)
	}
	alicePending, err = plane.GetClient(ctx, alicePending.ID)
	if err != nil {
		t.Fatalf("GetClient(alice pending) error = %v", err)
	}
	bobFailed, err = plane.GetClient(ctx, bobFailed.ID)
	if err != nil {
		t.Fatalf("GetClient(bob failed) error = %v", err)
	}

	failedRevision := bobFailed.DesiredRevision
	prefix := 7123
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions:     sessions,
		Accounts:     accounts,
		ControlPlane: plane,
		Runtime: serverHTTPTestClientRuntime{states: map[string]ServerClientRuntimeState{
			aliceCurrent.ID: {ConnectionState: ServerClientConnected, ProcessState: tunnelruntime.FRPProcessRunning},
			alicePending.ID: {ConnectionState: ServerClientDisconnected, ProcessState: tunnelruntime.FRPProcessStopped},
			bobFailed.ID: {
				ConnectionState: ServerClientConnected,
				ProcessState:    tunnelruntime.FRPProcessRunning,
				LastError:       &tunnelruntime.StructuredRuntimeError{Code: "APPLY_FAILED", Message: "client configuration failed", Revision: &failedRevision},
			},
		}},
		ServerState: serverHTTPTestStateProvider{state: ServerHTTPState{
			FRPS: tunnelruntime.FRPSupervisorState{
				State: tunnelruntime.FRPProcessConfigurationFailed,
				PID:   &prefix,
				Error: &tunnelruntime.StructuredRuntimeError{Code: "FRPS_FAILED", Message: "address already in use"},
			},
			Settings: ServerHTTPServerSettings{
				Address:          "127.0.0.1",
				ControlPort:      8080,
				FRPPort:          7000,
				HTTPPort:         8081,
				PortRange:        ServerHTTPPortRange{Start: 20000, End: 20100},
				AdvertiseFRPAddr: &ServerHTTPFRPAddress{Host: "tunnels.example.test", Port: 7000},
				DataDir:          "/private/tunnel-data",
				AdminUser:        "ops-admin",
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	adminGrant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}

	type counts struct {
		Clients   int `json:"clients"`
		Connected int `json:"connected"`
		Tunnels   int `json:"tunnels"`
		Pending   int `json:"pending"`
		Errors    int `json:"errors"`
	}
	type serverView struct {
		FRPS struct {
			State tunnelruntime.FRPProcessState         `json:"state"`
			PID   *int                                  `json:"pid"`
			Error *tunnelruntime.StructuredRuntimeError `json:"error"`
		} `json:"frps"`
		Settings ServerHTTPServerSettings `json:"settings"`
	}
	type responseBody struct {
		Version int                   `json:"version"`
		Account serverHTTPAccountView `json:"account"`
		Counts  counts                `json:"counts"`
		Server  *serverView           `json:"server"`
	}

	aliceResponse := serverHTTPReadRequest(handler, http.MethodGet, "/api/state", aliceGrant.Token)
	if aliceResponse.Code != http.StatusOK {
		t.Fatalf("alice GET /api/state = (%d, %s)", aliceResponse.Code, aliceResponse.Body.String())
	}
	assertServerHTTPHeaders(t, aliceResponse.Result())
	var aliceBody responseBody
	if err := json.Unmarshal(aliceResponse.Body.Bytes(), &aliceBody); err != nil {
		t.Fatalf("decode alice state: %v", err)
	}
	if aliceBody.Version != 1 || aliceBody.Account.ID != alice.ID || aliceBody.Account.ManagedByEnvironment || aliceBody.Counts != (counts{Clients: 2, Connected: 1, Tunnels: 2, Pending: 2}) || aliceBody.Server != nil {
		t.Fatalf("alice state = %#v", aliceBody)
	}
	if strings.Contains(aliceResponse.Body.String(), "alice-secret") || strings.Contains(aliceResponse.Body.String(), "environment-secret") || strings.Contains(aliceResponse.Body.String(), "private/tunnel-data") {
		t.Fatalf("alice state leaked credential or deployment data: %s", aliceResponse.Body.String())
	}

	adminResponse := serverHTTPReadRequest(handler, http.MethodGet, "/api/state", adminGrant.Token)
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("admin GET /api/state = (%d, %s)", adminResponse.Code, adminResponse.Body.String())
	}
	assertServerHTTPHeaders(t, adminResponse.Result())
	var adminBody responseBody
	if err := json.Unmarshal(adminResponse.Body.Bytes(), &adminBody); err != nil {
		t.Fatalf("decode admin state: %v", err)
	}
	if adminBody.Version != 1 || adminBody.Account.ID != environmentAdministratorID || !adminBody.Account.ManagedByEnvironment || adminBody.Counts != (counts{Clients: 3, Connected: 2, Tunnels: 3, Pending: 2, Errors: 1}) || adminBody.Server == nil {
		t.Fatalf("admin state = %#v", adminBody)
	}
	settings := adminBody.Server.Settings
	if adminBody.Server.FRPS.State != tunnelruntime.FRPProcessConfigurationFailed || adminBody.Server.FRPS.PID == nil || *adminBody.Server.FRPS.PID != prefix || adminBody.Server.FRPS.Error == nil || adminBody.Server.FRPS.Error.Code != "FRPS_FAILED" || settings.Address != "127.0.0.1" || settings.ControlPort != 8080 || settings.FRPPort != 7000 || settings.HTTPPort != 8081 || settings.PortRange != (ServerHTTPPortRange{Start: 20000, End: 20100}) || settings.AdvertiseFRPAddr == nil || *settings.AdvertiseFRPAddr != (ServerHTTPFRPAddress{Host: "tunnels.example.test", Port: 7000}) || settings.DataDir != "/private/tunnel-data" || settings.AdminUser != "ops-admin" {
		t.Fatalf("admin server state = %#v", adminBody.Server)
	}
	if strings.Contains(adminResponse.Body.String(), "environment-secret") || strings.Contains(adminResponse.Body.String(), "adminPassword") || strings.Contains(adminResponse.Body.String(), "frpToken") {
		t.Fatalf("admin state leaked secret fields: %s", adminResponse.Body.String())
	}

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "http://tunnel.example.test/api/state", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /api/state = (%d, %s)", unauthenticated.Code, unauthenticated.Body.String())
	}
	assertServerHTTPError(t, unauthenticated.Body.Bytes(), "AUTHENTICATION_REQUIRED")
	unsupported := serverHTTPReadRequest(handler, http.MethodPost, "/api/state", aliceGrant.Token)
	if unsupported.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/state = (%d, %s)", unsupported.Code, unsupported.Body.String())
	}
	assertServerHTTPError(t, unsupported.Body.Bytes(), "METHOD_NOT_ALLOWED")
}

func TestServerHTTPHandlerControlsManagedFRPSForAdministrators(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	_, err = accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	controller := &serverHTTPTestFRPSController{}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions:     sessions,
		Accounts:     accounts,
		ControlPlane: plane,
		FRPS:         controller,
		ServerState: serverHTTPTestStateProvider{state: ServerHTTPState{
			FRPS: tunnelruntime.FRPSupervisorState{State: tunnelruntime.FRPProcessStopped},
			Settings: ServerHTTPServerSettings{
				Address: "127.0.0.1", FRPPort: 7000, HTTPPort: 8080, DataDir: "/private/tunnel-data", AdminUser: "admin",
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	adminGrant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	request := func(method, path, token, origin string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://tunnel.example.test"+path, nil)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	unauthenticated := request(http.MethodPost, "/api/server/frp/start", "", "https://tunnel.example.test")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated FRPS start status = %d, body = %s", unauthenticated.Code, unauthenticated.Body.String())
	}
	assertServerHTTPError(t, unauthenticated.Body.Bytes(), "AUTHENTICATION_REQUIRED")
	ordinary := request(http.MethodPost, "/api/server/frp/start", aliceGrant.Token, "https://tunnel.example.test")
	if ordinary.Code != http.StatusForbidden {
		t.Fatalf("ordinary-user FRPS start status = %d, body = %s", ordinary.Code, ordinary.Body.String())
	}
	assertServerHTTPError(t, ordinary.Body.Bytes(), "FORBIDDEN")
	foreign := request(http.MethodPost, "/api/server/frp/start", adminGrant.Token, "https://other.example.test")
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("foreign-origin FRPS start status = %d, body = %s", foreign.Code, foreign.Body.String())
	}
	assertServerHTTPError(t, foreign.Body.Bytes(), "ORIGIN_FORBIDDEN")
	wrongMethod := request(http.MethodGet, "/api/server/frp/start", adminGrant.Token, "")
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET FRPS start status = %d, body = %s", wrongMethod.Code, wrongMethod.Body.String())
	}
	assertServerHTTPError(t, wrongMethod.Body.Bytes(), "METHOD_NOT_ALLOWED")
	if len(controller.calls) != 0 {
		t.Fatalf("rejected FRPS controller calls = %v", controller.calls)
	}

	for _, action := range []ServerFRPSAction{ServerFRPSActionStart, ServerFRPSActionStop, ServerFRPSActionRestart} {
		response := request(http.MethodPost, "/api/server/frp/"+string(action), adminGrant.Token, "https://tunnel.example.test")
		if response.Code != http.StatusOK {
			t.Fatalf("POST FRPS %s status = %d, body = %s", action, response.Code, response.Body.String())
		}
		assertServerHTTPHeaders(t, response.Result())
		if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != adminGrant.Token || cookies[0].MaxAge < 1 {
			t.Fatalf("FRPS %s session refresh cookie = %#v", action, cookies)
		}
		var body struct {
			Version int                  `json:"version"`
			Server  serverHTTPServerView `json:"server"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode FRPS %s response: %v", action, err)
		}
		if body.Version != 1 || body.Server.FRPS.State != tunnelruntime.FRPProcessStopped || body.Server.Settings.AdminUser != "admin" {
			t.Fatalf("FRPS %s response = %#v", action, body)
		}
	}
	if got, want := controller.calls, []ServerFRPSAction{ServerFRPSActionStart, ServerFRPSActionStop, ServerFRPSActionRestart}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("successful FRPS controller calls = %v, want %v", got, want)
	}

	for _, failure := range []struct {
		code string
	}{
		{code: "CONFIGURATION_FAILED"},
		{code: "ACTIVATION_FAILED"},
	} {
		controller.err = serverDomainError(failure.code, "fixture failure")
		response := request(http.MethodPost, "/api/server/frp/restart", adminGrant.Token, "https://tunnel.example.test")
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("FRPS %s status = %d, body = %s", failure.code, response.Code, response.Body.String())
		}
		assertServerHTTPError(t, response.Body.Bytes(), failure.code)
	}
	controller.err = nil

	unconfigured, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane, ServerState: serverHTTPTestStateProvider{}})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler(unconfigured) error = %v", err)
	}
	requestWithoutController := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/server/frp/start", nil)
	requestWithoutController.Header.Set("Origin", "https://tunnel.example.test")
	requestWithoutController.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: adminGrant.Token})
	unconfiguredResponse := httptest.NewRecorder()
	unconfigured.ServeHTTP(unconfiguredResponse, requestWithoutController)
	if unconfiguredResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured FRPS start status = %d, body = %s", unconfiguredResponse.Code, unconfiguredResponse.Body.String())
	}
	assertServerHTTPError(t, unconfiguredResponse.Body.Bytes(), "FRPS_UNAVAILABLE")
}

func TestServerHTTPHandlerReadsManagedCustom404PageForAdministrators(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	if _, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	reader := &serverHTTPTestCustom404PageReader{content: "<main>custom 404</main>"}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions: sessions, Accounts: accounts, ControlPlane: plane, Custom404PageReader: reader,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	adminGrant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	request := func(method, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://tunnel.example.test/api/server/frps/config/custom-404-page", nil)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	unauthenticated := request(http.MethodGet, "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated custom-page read status = %d, body = %s", unauthenticated.Code, unauthenticated.Body.String())
	}
	assertServerHTTPError(t, unauthenticated.Body.Bytes(), "AUTHENTICATION_REQUIRED")
	ordinary := request(http.MethodGet, aliceGrant.Token)
	if ordinary.Code != http.StatusForbidden {
		t.Fatalf("ordinary-user custom-page read status = %d, body = %s", ordinary.Code, ordinary.Body.String())
	}
	assertServerHTTPError(t, ordinary.Body.Bytes(), "FORBIDDEN")
	if reader.reads != 0 {
		t.Fatalf("ordinary-user custom-page reads = %d", reader.reads)
	}
	unsupported := request(http.MethodPost, adminGrant.Token)
	if unsupported.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST custom-page read status = %d, body = %s", unsupported.Code, unsupported.Body.String())
	}
	assertServerHTTPError(t, unsupported.Body.Bytes(), "METHOD_NOT_ALLOWED")

	response := request(http.MethodGet, adminGrant.Token)
	if response.Code != http.StatusOK {
		t.Fatalf("GET custom-page status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != adminGrant.Token || cookies[0].MaxAge < 1 {
		t.Fatalf("custom-page read session refresh cookie = %#v", cookies)
	}
	var body struct {
		Version int    `json:"version"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode custom-page response: %v", err)
	}
	if body.Version != 1 || body.Content != "<main>custom 404</main>" || reader.reads != 1 {
		t.Fatalf("custom-page response = %#v, reads = %d", body, reader.reads)
	}

	reader.err = serverDomainError("CONFIGURATION_FAILED", "fixture read failure")
	failed := request(http.MethodGet, adminGrant.Token)
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("failed custom-page read status = %d, body = %s", failed.Code, failed.Body.String())
	}
	assertServerHTTPError(t, failed.Body.Bytes(), "CONFIGURATION_FAILED")

	unconfigured, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler(unconfigured) error = %v", err)
	}
	unconfiguredRequest := httptest.NewRequest(http.MethodGet, "http://tunnel.example.test/api/server/frps/config/custom-404-page", nil)
	unconfiguredRequest.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: adminGrant.Token})
	unconfiguredResponse := httptest.NewRecorder()
	unconfigured.ServeHTTP(unconfiguredResponse, unconfiguredRequest)
	if unconfiguredResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured custom-page read status = %d, body = %s", unconfiguredResponse.Code, unconfiguredResponse.Body.String())
	}
	assertServerHTTPError(t, unconfiguredResponse.Body.Bytes(), "FRPS_UNAVAILABLE")
}

func TestServerHTTPHandlerWritesManagedCustom404PageForAdministrators(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	if _, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	writer := &serverHTTPTestCustom404PageWriter{}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions: sessions, Accounts: accounts, ControlPlane: plane, Custom404PageWriter: writer,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	adminGrant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	request := func(token, origin, contentType, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "http://tunnel.example.test/api/server/frps/config/custom-404-page", strings.NewReader(body))
		if token != "" {
			req.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	valid := `{"content":"<main>configured 404</main>"}`
	unauthenticated := request("", "https://tunnel.example.test", "application/json", valid)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated custom-page write status = %d, body = %s", unauthenticated.Code, unauthenticated.Body.String())
	}
	assertServerHTTPError(t, unauthenticated.Body.Bytes(), "AUTHENTICATION_REQUIRED")
	ordinary := request(aliceGrant.Token, "https://tunnel.example.test", "application/json", valid)
	if ordinary.Code != http.StatusForbidden {
		t.Fatalf("ordinary-user custom-page write status = %d, body = %s", ordinary.Code, ordinary.Body.String())
	}
	assertServerHTTPError(t, ordinary.Body.Bytes(), "FORBIDDEN")
	foreign := request(adminGrant.Token, "https://other.example.test", "application/json", valid)
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("foreign-origin custom-page write status = %d, body = %s", foreign.Code, foreign.Body.String())
	}
	assertServerHTTPError(t, foreign.Body.Bytes(), "ORIGIN_FORBIDDEN")
	for _, input := range []struct {
		name        string
		contentType string
		body        string
		status      int
		code        string
	}{
		{name: "wrong media type", contentType: "text/plain", body: valid, status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "malformed JSON", contentType: "application/json", body: `{"content":`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "missing content", contentType: "application/json", body: `{}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "null content", contentType: "application/json", body: `{"content":null}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{name: "unknown property", contentType: "application/json", body: `{"content":"ok","extra":true}`, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
	} {
		t.Run(input.name, func(t *testing.T) {
			response := request(adminGrant.Token, "https://tunnel.example.test", input.contentType, input.body)
			if response.Code != input.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertServerHTTPError(t, response.Body.Bytes(), input.code)
		})
	}
	if len(writer.contents) != 0 {
		t.Fatalf("rejected custom-page writes = %v", writer.contents)
	}

	response := request(adminGrant.Token, "https://tunnel.example.test", "application/json", valid)
	if response.Code != http.StatusOK {
		t.Fatalf("custom-page write status = %d, body = %s", response.Code, response.Body.String())
	}
	assertServerHTTPHeaders(t, response.Result())
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != serverSessionCookieName || cookies[0].Value != adminGrant.Token || cookies[0].MaxAge < 1 {
		t.Fatalf("custom-page write session refresh cookie = %#v", cookies)
	}
	var body struct {
		Version int    `json:"version"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode custom-page write response: %v", err)
	}
	if body.Version != 1 || body.Content != "<main>configured 404</main>" || len(writer.contents) != 1 || writer.contents[0] != body.Content {
		t.Fatalf("custom-page write response = %#v, writes = %v", body, writer.contents)
	}
	removed := request(adminGrant.Token, "https://tunnel.example.test", "application/json", `{"content":""}`)
	if removed.Code != http.StatusOK {
		t.Fatalf("custom-page removal status = %d, body = %s", removed.Code, removed.Body.String())
	}
	if len(writer.contents) != 2 || writer.contents[1] != "" {
		t.Fatalf("custom-page removal writes = %v", writer.contents)
	}

	for _, failure := range []struct {
		code   string
		status int
	}{
		{code: "INVALID_CUSTOM_404_PAGE", status: http.StatusBadRequest},
		{code: "CONFIGURATION_FAILED", status: http.StatusInternalServerError},
	} {
		writer.err = serverDomainError(failure.code, "fixture write failure")
		failed := request(adminGrant.Token, "https://tunnel.example.test", "application/json", valid)
		if failed.Code != failure.status {
			t.Fatalf("%s write status = %d, body = %s", failure.code, failed.Code, failed.Body.String())
		}
		assertServerHTTPError(t, failed.Body.Bytes(), failure.code)
	}
	writer.err = nil

	unconfigured, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler(unconfigured) error = %v", err)
	}
	unconfiguredRequest := httptest.NewRequest(http.MethodPut, "http://tunnel.example.test/api/server/frps/config/custom-404-page", strings.NewReader(valid))
	unconfiguredRequest.Header.Set("Origin", "https://tunnel.example.test")
	unconfiguredRequest.Header.Set("Content-Type", "application/json")
	unconfiguredRequest.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: adminGrant.Token})
	unconfiguredResponse := httptest.NewRecorder()
	unconfigured.ServeHTTP(unconfiguredResponse, unconfiguredRequest)
	if unconfiguredResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured custom-page write status = %d, body = %s", unconfiguredResponse.Code, unconfiguredResponse.Body.String())
	}
	assertServerHTTPError(t, unconfiguredResponse.Body.Bytes(), "FRPS_UNAVAILABLE")
}

func TestServerHTTPHandlerServesNonUpgradeAgentBearerProbe(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	availability := &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning}
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{ControlPlane: plane, FRPS: availability})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(ctx, "environment-admin", "agent HTTP")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, AgentGateway: gateway})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	request := func(method, authorization string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://tunnel.example.test/api/agent", nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	wrongMethod := request(http.MethodPost, "Bearer "+client.Token)
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/agent = (%d, %s)", wrongMethod.Code, wrongMethod.Body.String())
	}
	assertServerHTTPHeaders(t, wrongMethod.Result())
	assertServerHTTPError(t, wrongMethod.Body.Bytes(), "METHOD_NOT_ALLOWED")

	unauthenticated := request(http.MethodGet, "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /api/agent = (%d, %s)", unauthenticated.Code, unauthenticated.Body.String())
	}
	assertServerHTTPAgentHeaders(t, unauthenticated.Result())
	assertServerHTTPError(t, unauthenticated.Body.Bytes(), "AUTHENTICATION_FAILED")

	availability.set(tunnelruntime.FRPProcessStopped)
	unavailable := request(http.MethodGet, "Bearer "+client.Token)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable GET /api/agent = (%d, %s)", unavailable.Code, unavailable.Body.String())
	}
	assertServerHTTPAgentHeaders(t, unavailable.Result())
	assertServerHTTPError(t, unavailable.Body.Bytes(), "FRPS_UNAVAILABLE")
	availability.set(tunnelruntime.FRPProcessRunning)

	pending, err := gateway.Authorize(ctx, "Bearer "+client.Token)
	if err != nil {
		t.Fatalf("Authorize(pending fixture) error = %v", err)
	}
	duplicate := request(http.MethodGet, "Bearer "+client.Token)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate GET /api/agent = (%d, %s)", duplicate.Code, duplicate.Body.String())
	}
	assertServerHTTPAgentHeaders(t, duplicate.Result())
	assertServerHTTPError(t, duplicate.Body.Bytes(), "CLIENT_CONNECTED")
	pending.Release()

	for attempt := range 2 {
		probe := request(http.MethodGet, "Bearer "+client.Token)
		if probe.Code != http.StatusUpgradeRequired {
			t.Fatalf("probe %d GET /api/agent = (%d, %s)", attempt, probe.Code, probe.Body.String())
		}
		assertServerHTTPHeaders(t, probe.Result())
		assertServerHTTPError(t, probe.Body.Bytes(), "UPGRADE_REQUIRED")
	}
}

func TestServerHTTPHandlerUpgradesAgentAndRetainsTheSlotUntilSocketClose(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(ctx, "environment-admin", "agent WebSocket")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions:     openServerSessions(t, accounts, state),
		AgentGateway: gateway,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent"
	socket, response, err := websocket.DefaultDialer.Dial(socketURL, http.Header{"Authorization": []string{"Bearer " + client.Token}})
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("WebSocket upgrade = (%d, %s, %v)", response.StatusCode, body, err)
		}
		t.Fatalf("WebSocket upgrade: %v", err)
	}

	probe := func() *http.Response {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodGet, server.URL+"/api/agent", nil)
		if requestErr != nil {
			t.Fatalf("NewRequest(agent probe): %v", requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+client.Token)
		result, requestErr := server.Client().Do(request)
		if requestErr != nil {
			t.Fatalf("GET /api/agent probe: %v", requestErr)
		}
		return result
	}

	duplicate := probe()
	if duplicate.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(duplicate.Body)
		_ = duplicate.Body.Close()
		t.Fatalf("duplicate agent probe = (%d, %s)", duplicate.StatusCode, body)
	}
	assertServerHTTPAgentHeaders(t, duplicate)
	body, _ := io.ReadAll(duplicate.Body)
	_ = duplicate.Body.Close()
	assertServerHTTPError(t, body, "CLIENT_CONNECTED")

	if err := socket.Close(); err != nil {
		t.Fatalf("close agent WebSocket: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		afterClose := probe()
		body, _ := io.ReadAll(afterClose.Body)
		_ = afterClose.Body.Close()
		if afterClose.StatusCode == http.StatusUpgradeRequired {
			assertServerHTTPError(t, body, "UPGRADE_REQUIRED")
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent probe after WebSocket close = (%d, %s), want 426", afterClose.StatusCode, body)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestServerHTTPHandlerProjectsAcceptedAgentSocketLifecycle(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(ctx, "environment-admin", "runtime socket")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	grant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions:     sessions,
		Accounts:     accounts,
		ControlPlane: plane,
		AgentGateway: gateway,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent"
	socket, response, err := websocket.DefaultDialer.Dial(socketURL, http.Header{"Authorization": []string{"Bearer " + client.Token}})
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("WebSocket upgrade = (%d, %s, %v)", response.StatusCode, body, err)
		}
		t.Fatalf("WebSocket upgrade: %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })

	runtime := func() ServerClientRuntimeState {
		result := serverHTTPReadRequest(handler, http.MethodGet, "/api/clients/"+client.ID, grant.Token)
		if result.Code != http.StatusOK {
			t.Fatalf("GET /api/clients/%s = (%d, %s)", client.ID, result.Code, result.Body.String())
		}
		var body struct {
			Client struct {
				Runtime ServerClientRuntimeState `json:"runtime"`
			} `json:"client"`
		}
		if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode client runtime = %v", err)
		}
		return body.Client.Runtime
	}
	wantConnected := ServerClientRuntimeState{ConnectionState: ServerClientConnected, ProcessState: tunnelruntime.FRPProcessStopped}
	if got := runtime(); got != wantConnected {
		t.Fatalf("runtime while WebSocket is open = %#v, want %#v", got, wantConnected)
	}

	if err := socket.Close(); err != nil {
		t.Fatalf("close agent WebSocket: %v", err)
	}
	wantDisconnected := ServerClientRuntimeState{ConnectionState: ServerClientDisconnected, ProcessState: tunnelruntime.FRPProcessStopped}
	deadline := time.Now().Add(time.Second)
	for {
		if got := runtime(); got == wantDisconnected {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("runtime after WebSocket close = %#v, want %#v", got, wantDisconnected)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestServerHTTPHandlerKeepsAwaitingHelloAgentAliveWhenItPongs(t *testing.T) {
	previousInterval := serverAgentWebSocketPingInterval
	serverAgentWebSocketPingInterval = 25 * time.Millisecond
	t.Cleanup(func() { serverAgentWebSocketPingInterval = previousInterval })

	gateway, client, socket := openServerHTTPAgentSocket(t)
	pings := make(chan struct{}, 3)
	socket.SetPingHandler(func(message string) error {
		select {
		case pings <- struct{}{}:
		default:
		}
		return socket.WriteControl(websocket.PongMessage, []byte(message), time.Now().Add(time.Second))
	})
	readDone := make(chan error, 1)
	go func() {
		for {
			if _, _, err := socket.ReadMessage(); err != nil {
				readDone <- err
				return
			}
		}
	}()
	for range 3 {
		select {
		case <-pings:
		case <-time.After(time.Second):
			t.Fatal("agent did not receive a WebSocket ping")
		}
	}
	if got := gateway.State(client.ID); got != (ServerClientRuntimeState{ConnectionState: ServerClientConnected, ProcessState: tunnelruntime.FRPProcessStopped}) {
		t.Fatalf("runtime after answered pings = %#v", got)
	}
	if err := socket.Close(); err != nil {
		t.Fatalf("close agent WebSocket: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("agent reader did not stop after WebSocket close")
	}
}

func TestServerHTTPHandlerClosesAgentWhenPongIsMissing(t *testing.T) {
	previousInterval := serverAgentWebSocketPingInterval
	serverAgentWebSocketPingInterval = 25 * time.Millisecond
	t.Cleanup(func() { serverAgentWebSocketPingInterval = previousInterval })

	gateway, client, socket := openServerHTTPAgentSocket(t)
	pinged := make(chan struct{}, 1)
	socket.SetPingHandler(func(string) error {
		select {
		case pinged <- struct{}{}:
		default:
		}
		return nil
	})
	if _, _, err := socket.ReadMessage(); err == nil {
		t.Fatal("read missing-pong close = nil")
	} else {
		var closeError *websocket.CloseError
		if !errors.As(err, &closeError) || closeError.Code != serverAgentCloseLivenessTimeout {
			t.Fatalf("missing-pong close = %v, want %d", err, serverAgentCloseLivenessTimeout)
		}
	}
	select {
	case <-pinged:
	default:
		t.Fatal("agent timed out without first receiving a WebSocket ping")
	}

	deadline := time.Now().Add(time.Second)
	for {
		if got := gateway.State(client.ID); got == (ServerClientRuntimeState{ConnectionState: ServerClientDisconnected, ProcessState: tunnelruntime.FRPProcessStopped}) {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("runtime after missing-pong close = %#v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func openServerHTTPAgentSocket(t *testing.T) (*ServerAgentGateway, TrustedTunnelClient, *websocket.Conn) {
	t.Helper()
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(ctx, "environment-admin", "liveness socket")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions:     openServerSessions(t, accounts, state),
		AgentGateway: gateway,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent"
	socket, response, err := websocket.DefaultDialer.Dial(socketURL, http.Header{"Authorization": []string{"Bearer " + client.Token}})
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("WebSocket upgrade = (%d, %s, %v)", response.StatusCode, body, err)
		}
		t.Fatalf("WebSocket upgrade: %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	return gateway, client, socket
}

func TestServerHTTPHandlerReleasesAgentReservationWhenUpgradeFails(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(ctx, "environment-admin", "failed agent WebSocket")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions:     openServerSessions(t, accounts, state),
		AgentGateway: gateway,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}

	failedUpgrade := httptest.NewRequest(http.MethodGet, "http://tunnel.example.test/api/agent", nil)
	failedUpgrade.Header.Set("Authorization", "Bearer "+client.Token)
	failedUpgrade.Header.Set("Connection", "Upgrade")
	failedUpgrade.Header.Set("Upgrade", "websocket")
	failedResponse := httptest.NewRecorder()
	handler.ServeHTTP(failedResponse, failedUpgrade)
	if failedResponse.Code != http.StatusBadRequest {
		t.Fatalf("malformed agent WebSocket upgrade = (%d, %s)", failedResponse.Code, failedResponse.Body.String())
	}

	probe := httptest.NewRequest(http.MethodGet, "http://tunnel.example.test/api/agent", nil)
	probe.Header.Set("Authorization", "Bearer "+client.Token)
	probeResponse := httptest.NewRecorder()
	handler.ServeHTTP(probeResponse, probe)
	if probeResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("agent probe after failed upgrade = (%d, %s)", probeResponse.Code, probeResponse.Body.String())
	}
	assertServerHTTPError(t, probeResponse.Body.Bytes(), "UPGRADE_REQUIRED")
}

func TestServerHTTPHandlerClosesInvalidAgentHello(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(ctx, "environment-admin", "invalid hello")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions:     openServerSessions(t, accounts, state),
		AgentGateway: gateway,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent"
	socket, response, err := websocket.DefaultDialer.Dial(socketURL, http.Header{"Authorization": []string{"Bearer " + client.Token}})
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("WebSocket upgrade = (%d, %s, %v)", response.StatusCode, body, err)
		}
		t.Fatalf("WebSocket upgrade: %v", err)
	}
	defer socket.Close()
	if err := socket.WriteMessage(websocket.TextMessage, []byte(`{"type":"apply_result","tunnelProtocolVersion":3}`)); err != nil {
		t.Fatalf("write invalid hello: %v", err)
	}
	if _, _, err := socket.ReadMessage(); err == nil {
		t.Fatal("read invalid hello close = nil")
	} else {
		var closeError *websocket.CloseError
		if !errors.As(err, &closeError) || closeError.Code != serverAgentCloseInvalidMessage {
			t.Fatalf("invalid hello close = %v, want %d", err, serverAgentCloseInvalidMessage)
		}
	}

	deadline := time.Now().Add(time.Second)
	for {
		probe, probeErr := http.NewRequest(http.MethodGet, server.URL+"/api/agent", nil)
		if probeErr != nil {
			t.Fatalf("NewRequest(agent probe): %v", probeErr)
		}
		probe.Header.Set("Authorization", "Bearer "+client.Token)
		result, probeErr := server.Client().Do(probe)
		if probeErr != nil {
			t.Fatalf("GET /api/agent probe: %v", probeErr)
		}
		body, _ := io.ReadAll(result.Body)
		_ = result.Body.Close()
		if result.StatusCode == http.StatusUpgradeRequired {
			assertServerHTTPError(t, body, "UPGRADE_REQUIRED")
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent probe after invalid hello = (%d, %s), want 426", result.StatusCode, body)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestServerHTTPHandlerSendsWelcomeAfterValidAgentHello(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
		WelcomeSource: serverAgentTestWelcomeSource{settings: ServerAgentWelcomeSettings{
			AdvertisedFRPHost: "frp.example.test",
			AdvertisedFRPPort: 7001,
			InternalFRPToken:  "agent-only-token",
		}},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(ctx, "environment-admin", "welcome agent")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions:     openServerSessions(t, accounts, state),
		AgentGateway: gateway,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent"
	socket, response, err := websocket.DefaultDialer.Dial(socketURL, http.Header{"Authorization": []string{"Bearer " + client.Token}})
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("WebSocket upgrade = (%d, %s, %v)", response.StatusCode, body, err)
		}
		t.Fatalf("WebSocket upgrade: %v", err)
	}
	defer socket.Close()
	if err := socket.WriteMessage(websocket.TextMessage, []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`)); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	messageType, source, err := socket.ReadMessage()
	if err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("welcome message type = %d", messageType)
	}
	var welcome tunnelruntime.AgentWelcome
	if err := json.Unmarshal(source, &welcome); err != nil {
		t.Fatalf("decode welcome: %v", err)
	}
	if welcome.Type != "welcome" || welcome.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion || welcome.RequiredFRPVersion != tunnelruntime.FRPVersion || welcome.Artifact.Version != tunnelruntime.FRPVersion || welcome.AdvertisedFRPHost != "frp.example.test" || welcome.AdvertisedFRPPort != 7001 || welcome.InternalFRPToken != "agent-only-token" || welcome.Snapshot.ClientKey != client.ID || welcome.Snapshot.Revision != 0 || len(welcome.Snapshot.Tunnels) != 0 {
		t.Fatalf("welcome = %#v", welcome)
	}
	if _, err := plane.CreateTunnel(ctx, client.ID, TunnelMutationInput{
		Protocol:  tunnelruntime.TunnelProtocolTCP,
		LocalPort: 3000,
	}); err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	if err := socket.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set desired-state read deadline: %v", err)
	}
	messageType, source, err = socket.ReadMessage()
	if err != nil {
		t.Fatalf("read desired state: %v", err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("desired-state message type = %d", messageType)
	}
	var desired tunnelruntime.DesiredState
	if err := json.Unmarshal(source, &desired); err != nil {
		t.Fatalf("decode desired state: %v", err)
	}
	if desired.Type != "desired_state" || desired.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion || desired.Snapshot.ClientKey != client.ID || desired.Snapshot.Revision != 1 || len(desired.Snapshot.Tunnels) != 1 {
		t.Fatalf("desired state = %#v", desired)
	}
	if err := socket.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear desired-state read deadline: %v", err)
	}
	if err := socket.WriteMessage(websocket.TextMessage, []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":1,"success":true}`)); err != nil {
		t.Fatalf("write apply result: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		current, err := plane.GetClient(ctx, client.ID)
		if err != nil {
			t.Fatalf("GetClient(after apply result) error = %v", err)
		}
		if current.LastAppliedRevision == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("LastAppliedRevision after apply result = %d, want 1", current.LastAppliedRevision)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := plane.RotateClientToken(ctx, client.ID); err != nil {
		t.Fatalf("RotateClientToken() error = %v", err)
	}
	messageType, source, err = socket.ReadMessage()
	if err != nil {
		t.Fatalf("read revoke: %v", err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("revoke message type = %d", messageType)
	}
	var revoke tunnelruntime.Revoke
	if err := json.Unmarshal(source, &revoke); err != nil {
		t.Fatalf("decode revoke: %v", err)
	}
	if revoke.Type != "revoke" || revoke.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion || revoke.Reason != "rotated" {
		t.Fatalf("revoke = %#v", revoke)
	}
	if _, _, err := socket.ReadMessage(); err == nil {
		t.Fatal("read revoke close = nil")
	} else {
		var closeError *websocket.CloseError
		if !errors.As(err, &closeError) || closeError.Code != serverAgentCloseRevoked {
			t.Fatalf("revoke close = %v, want %d", err, serverAgentCloseRevoked)
		}
	}
}

func TestServerHTTPHandlerClosesInvalidAgentApplyResult(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
		WelcomeSource: serverAgentTestWelcomeSource{settings: ServerAgentWelcomeSettings{
			AdvertisedFRPHost: "frp.example.test",
			AdvertisedFRPPort: 7001,
			InternalFRPToken:  "agent-only-token",
		}},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(ctx, "environment-admin", "invalid apply result")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions: openServerSessions(t, accounts, state), AgentGateway: gateway,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent"
	socket, response, err := websocket.DefaultDialer.Dial(socketURL, http.Header{"Authorization": []string{"Bearer " + client.Token}})
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("WebSocket upgrade = (%d, %s, %v)", response.StatusCode, body, err)
		}
		t.Fatalf("WebSocket upgrade: %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	if err := socket.WriteMessage(websocket.TextMessage, []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`)); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if _, _, err := socket.ReadMessage(); err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	if err := socket.WriteMessage(websocket.TextMessage, []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":1,"success":true}`)); err != nil {
		t.Fatalf("write invalid apply result: %v", err)
	}
	if _, _, err := socket.ReadMessage(); err == nil {
		t.Fatal("read invalid apply result close = nil")
	} else {
		var closeError *websocket.CloseError
		if !errors.As(err, &closeError) || closeError.Code != serverAgentCloseInvalidMessage {
			t.Fatalf("invalid apply result close = %v, want %d", err, serverAgentCloseInvalidMessage)
		}
	}
	current, err := plane.GetClient(ctx, client.ID)
	if err != nil || current.LastAppliedRevision != 0 {
		t.Fatalf("client after rejected apply result = (%#v, %v)", current, err)
	}
}

func TestServerHTTPHandlerProjectsAgentProcessState(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
		WelcomeSource: serverAgentTestWelcomeSource{settings: ServerAgentWelcomeSettings{
			AdvertisedFRPHost: "frp.example.test",
			AdvertisedFRPPort: 7001,
			InternalFRPToken:  "agent-only-token",
		}},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(ctx, "environment-admin", "runtime process state")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions: sessions, Accounts: accounts, ControlPlane: plane, AgentGateway: gateway,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	grant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}

	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent"
	socket, response, err := websocket.DefaultDialer.Dial(socketURL, http.Header{"Authorization": []string{"Bearer " + client.Token}})
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("WebSocket upgrade = (%d, %s, %v)", response.StatusCode, body, err)
		}
		t.Fatalf("WebSocket upgrade: %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	if err := socket.WriteMessage(websocket.TextMessage, []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`)); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if _, _, err := socket.ReadMessage(); err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	if err := socket.WriteMessage(websocket.TextMessage, []byte(`{"type":"process_state","tunnelProtocolVersion":3,"state":"running"}`)); err != nil {
		t.Fatalf("write process state: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		response := serverHTTPReadRequest(handler, http.MethodGet, "/api/clients/"+client.ID, grant.Token)
		if response.Code != http.StatusOK {
			t.Fatalf("GET /api/clients/:id = (%d, %s)", response.Code, response.Body.String())
		}
		var body struct {
			Client serverHTTPClientView `json:"client"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode client runtime: %v", err)
		}
		if body.Client.Runtime.ConnectionState == ServerClientConnected && body.Client.Runtime.ProcessState == tunnelruntime.FRPProcessRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("client runtime after process state = %#v", body.Client.Runtime)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := socket.WriteMessage(websocket.TextMessage, []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":0,"success":false,"error":{"code":"APPLY_FAILED","message":"client configuration failed","revision":0}}`)); err != nil {
		t.Fatalf("write failed apply result: %v", err)
	}
	deadline = time.Now().Add(time.Second)
	for {
		response := serverHTTPReadRequest(handler, http.MethodGet, "/api/clients/"+client.ID, grant.Token)
		if response.Code != http.StatusOK {
			t.Fatalf("GET /api/clients/:id after failed apply result = (%d, %s)", response.Code, response.Body.String())
		}
		var body struct {
			Client serverHTTPClientView `json:"client"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode client runtime after failed apply result: %v", err)
		}
		if body.Client.Runtime.ProcessState == tunnelruntime.FRPProcessRunning && body.Client.Runtime.LastError != nil && body.Client.Runtime.LastError.Code == "APPLY_FAILED" && body.Client.Runtime.LastError.Revision != nil && *body.Client.Runtime.LastError.Revision == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("client runtime after failed apply result = %#v", body.Client.Runtime)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestServerHTTPHandlerRestartsOnlyOwnedOnlineClients(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := accounts.CreateLocalAccount(ctx, "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
		WelcomeSource: serverAgentTestWelcomeSource{settings: ServerAgentWelcomeSettings{
			AdvertisedFRPHost: "frp.example.test",
			AdvertisedFRPPort: 7001,
			InternalFRPToken:  "agent-only-token",
		}},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	online, err := plane.CreateClient(ctx, alice.ID, "online restart")
	if err != nil {
		t.Fatalf("CreateClient(online) error = %v", err)
	}
	offline, err := plane.CreateClient(ctx, alice.ID, "offline restart")
	if err != nil {
		t.Fatalf("CreateClient(offline) error = %v", err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions: sessions, Accounts: accounts, ControlPlane: plane, AgentGateway: gateway,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	aliceGrant, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	bobGrant, err := sessions.SignIn(ctx, "bob", "bob-secret")
	if err != nil {
		t.Fatalf("SignIn(bob) error = %v", err)
	}

	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent"
	socket, response, err := websocket.DefaultDialer.Dial(socketURL, http.Header{"Authorization": []string{"Bearer " + online.Token}})
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("WebSocket upgrade = (%d, %s, %v)", response.StatusCode, body, err)
		}
		t.Fatalf("WebSocket upgrade: %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	if err := socket.WriteMessage(websocket.TextMessage, []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`)); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if _, _, err := socket.ReadMessage(); err != nil {
		t.Fatalf("read welcome: %v", err)
	}

	restart := func(clientID, method, token, origin string) (int, []byte) {
		t.Helper()
		request, err := http.NewRequest(method, server.URL+"/api/clients/"+clientID+"/restart", nil)
		if err != nil {
			t.Fatalf("NewRequest(%s /restart): %v", method, err)
		}
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if token != "" {
			request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatalf("%s /restart: %v", method, err)
		}
		body, err := io.ReadAll(response.Body)
		if closeErr := response.Body.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatalf("read %s /restart response: %v", method, err)
		}
		return response.StatusCode, body
	}
	origin := "https://" + strings.TrimPrefix(server.URL, "http://")
	before, err := plane.GetClient(ctx, online.ID)
	if err != nil {
		t.Fatalf("GetClient(before restart) error = %v", err)
	}
	status, body := restart(online.ID, http.MethodPost, aliceGrant.Token, origin)
	if status != http.StatusAccepted {
		t.Fatalf("POST /api/clients/:id/restart status = %d, body = %s", status, body)
	}
	var accepted struct {
		Version  int  `json:"version"`
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decode restart response: %v", err)
	}
	if accepted.Version != 1 || !accepted.Accepted {
		t.Fatalf("restart response = %#v", accepted)
	}
	if err := socket.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set restart read deadline: %v", err)
	}
	messageType, source, err := socket.ReadMessage()
	if err != nil {
		t.Fatalf("read restart: %v", err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("restart message type = %d", messageType)
	}
	var frame tunnelruntime.RestartFRPC
	if err := json.Unmarshal(source, &frame); err != nil {
		t.Fatalf("decode restart frame: %v", err)
	}
	if frame.Type != "restart_frpc" || frame.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion {
		t.Fatalf("restart frame = %#v", frame)
	}
	after, err := plane.GetClient(ctx, online.ID)
	if err != nil {
		t.Fatalf("GetClient(after restart) error = %v", err)
	}
	if after.Remark != before.Remark || after.Token != before.Token || after.DesiredRevision != before.DesiredRevision || after.LastAppliedRevision != before.LastAppliedRevision || after.RevocationPending != before.RevocationPending {
		t.Fatalf("restart changed durable client state: before=%#v after=%#v", before, after)
	}

	for _, test := range []struct {
		name     string
		clientID string
		method   string
		token    string
		origin   string
		status   int
		code     string
	}{
		{name: "offline", clientID: offline.ID, method: http.MethodPost, token: aliceGrant.Token, origin: origin, status: http.StatusConflict, code: "CLIENT_OFFLINE"},
		{name: "other owner", clientID: online.ID, method: http.MethodPost, token: bobGrant.Token, origin: origin, status: http.StatusNotFound, code: "NOT_FOUND"},
		{name: "foreign origin", clientID: online.ID, method: http.MethodPost, token: aliceGrant.Token, origin: "https://other.example.test", status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "unsupported method", clientID: online.ID, method: http.MethodGet, token: aliceGrant.Token, origin: origin, status: http.StatusMethodNotAllowed, code: "METHOD_NOT_ALLOWED"},
		{name: "unauthenticated", clientID: online.ID, method: http.MethodPost, origin: origin, status: http.StatusUnauthorized, code: "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, body := restart(test.clientID, test.method, test.token, test.origin)
			if status != test.status {
				t.Fatalf("%s /restart status = %d, body = %s", test.method, status, body)
			}
			assertServerHTTPError(t, body, test.code)
		})
	}
}

func TestServerHTTPHandlerAcknowledgesReplacementTokenOnAgentUpgrade(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(ctx, "environment-admin", "replacement socket")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	rotated, err := plane.RotateClientToken(ctx, client.ID)
	if err != nil {
		t.Fatalf("RotateClientToken() error = %v", err)
	}
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions:     openServerSessions(t, accounts, state),
		AgentGateway: gateway,
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent"
	socket, response, err := websocket.DefaultDialer.Dial(socketURL, http.Header{"Authorization": []string{"Bearer " + rotated.Token}})
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("WebSocket upgrade = (%d, %s, %v)", response.StatusCode, body, err)
		}
		t.Fatalf("WebSocket upgrade: %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	current, err := plane.GetClient(ctx, client.ID)
	if err != nil || current.RevocationPending {
		t.Fatalf("client after WebSocket upgrade = (%#v, %v)", current, err)
	}
}

func TestServerAgentRequestHostnameUsesRequestHostWithoutForwardedHeaders(t *testing.T) {
	for _, test := range []struct {
		name string
		host string
		want string
	}{
		{name: "hostname and port", host: "tunnel.example.test:8443", want: "tunnel.example.test"},
		{name: "hostname", host: "tunnel.example.test", want: "tunnel.example.test"},
		{name: "IPv6", host: "[2001:db8::1]:7443", want: "2001:db8::1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://internal.example.test/api/agent", nil)
			request.Host = test.host
			request.Header.Set("Forwarded", "host=public.example.test")
			request.Header.Set("X-Forwarded-Host", "other.example.test")
			if got := serverAgentRequestHostname(request); got != test.want {
				t.Fatalf("serverAgentRequestHostname() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestServerHTTPHandlerStreamsAdministratorFRPSInvalidation(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	if _, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	changes := &serverHTTPTestFRPSChangeObserver{}
	controller := &serverHTTPTestFRPSController{changes: changes}
	writer := &serverHTTPTestCustom404PageWriter{changes: changes}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions: sessions, Accounts: accounts, ControlPlane: plane,
		FRPS: controller, Custom404PageWriter: writer, FRPSChanges: changes,
		ServerState: serverHTTPTestStateProvider{state: ServerHTTPState{FRPS: tunnelruntime.FRPSupervisorState{State: tunnelruntime.FRPProcessStopped}}},
	})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adminGrant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	adminResponse, adminEvents := openServerHTTPEvents(t, server.Client(), server.URL, adminGrant.Token)
	defer adminResponse.Body.Close()
	aliceResponse, aliceEvents := openServerHTTPEvents(t, server.Client(), server.URL, aliceGrant.Token)
	defer aliceResponse.Body.Close()
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator initial event = %q, want changed", event)
	}
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "changed" {
		t.Fatalf("alice initial event = %q, want changed", event)
	}

	origin := "https://" + strings.TrimPrefix(server.URL, "http://")
	control, err := http.NewRequest(http.MethodPost, server.URL+"/api/server/frp/start", nil)
	if err != nil {
		t.Fatalf("NewRequest(frps start) error = %v", err)
	}
	control.Header.Set("Origin", origin)
	control.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: adminGrant.Token})
	controlResponse, err := server.Client().Do(control)
	if err != nil {
		t.Fatalf("POST /api/server/frp/start error = %v", err)
	}
	if controlResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(controlResponse.Body)
		_ = controlResponse.Body.Close()
		t.Fatalf("POST /api/server/frp/start = (%d, %s)", controlResponse.StatusCode, body)
	}
	if err := controlResponse.Body.Close(); err != nil {
		t.Fatalf("close FRPS control response: %v", err)
	}
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator FRPS event = %q, want changed", event)
	}

	page, err := http.NewRequest(http.MethodPut, server.URL+"/api/server/frps/config/custom-404-page", strings.NewReader(`{"content":"<main>custom 404</main>"}`))
	if err != nil {
		t.Fatalf("NewRequest(custom page) error = %v", err)
	}
	page.Header.Set("Origin", origin)
	page.Header.Set("Content-Type", "application/json")
	page.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: adminGrant.Token})
	pageResponse, err := server.Client().Do(page)
	if err != nil {
		t.Fatalf("PUT custom 404 page error = %v", err)
	}
	if pageResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(pageResponse.Body)
		_ = pageResponse.Body.Close()
		t.Fatalf("PUT custom 404 page = (%d, %s)", pageResponse.StatusCode, body)
	}
	if err := pageResponse.Body.Close(); err != nil {
		t.Fatalf("close custom 404 response: %v", err)
	}
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator custom-page event = %q, want changed", event)
	}
	assertNoServerHTTPEvent(t, aliceEvents, aliceResponse.Body)
}

func TestServerHTTPHandlerStreamsScopedInvalidationEvents(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := accounts.CreateLocalAccount(ctx, "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	aliceGrant, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	bobGrant, err := sessions.SignIn(ctx, "bob", "bob-secret")
	if err != nil {
		t.Fatalf("SignIn(bob) error = %v", err)
	}
	adminGrant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}

	aliceResponse, aliceEvents := openServerHTTPEvents(t, server.Client(), server.URL, aliceGrant.Token)
	defer aliceResponse.Body.Close()
	bobResponse, bobEvents := openServerHTTPEvents(t, server.Client(), server.URL, bobGrant.Token)
	defer bobResponse.Body.Close()
	adminResponse, adminEvents := openServerHTTPEvents(t, server.Client(), server.URL, adminGrant.Token)
	defer adminResponse.Body.Close()
	for _, events := range []*bufio.Reader{aliceEvents, bobEvents, adminEvents} {
		if event := waitForServerHTTPEvent(t, events); event != "changed" {
			t.Fatalf("initial event = %q, want changed", event)
		}
	}

	if _, err := plane.CreateClient(ctx, alice.ID, "Alice workstation"); err != nil {
		t.Fatalf("CreateClient(alice) error = %v", err)
	}
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "changed" {
		t.Fatalf("alice resource event = %q, want changed", event)
	}
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator resource event = %q, want changed", event)
	}
	assertNoServerHTTPEvent(t, bobEvents, bobResponse.Body)

	if _, err := sessions.ChangeLocalAccountRole(ctx, alice.ID, AccountRoleAdmin); err != nil {
		t.Fatalf("ChangeLocalAccountRole() error = %v", err)
	}
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "session_revoked" {
		t.Fatalf("alice revocation event = %q, want session_revoked", event)
	}
	if _, err := readServerHTTPEvent(aliceEvents); err != io.EOF {
		t.Fatalf("event stream after revocation error = %v, want EOF", err)
	}

	unauthenticated, err := server.Client().Get(server.URL + "/api/events")
	if err != nil {
		t.Fatalf("unauthenticated GET /api/events error = %v", err)
	}
	defer unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(unauthenticated.Body)
		t.Fatalf("unauthenticated GET /api/events = (%d, %s)", unauthenticated.StatusCode, body)
	}
	unauthenticatedBody, _ := io.ReadAll(unauthenticated.Body)
	assertServerHTTPError(t, unauthenticatedBody, "AUTHENTICATION_REQUIRED")

	unsupportedRequest, err := http.NewRequest(http.MethodPost, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("NewRequest(unsupported events) error = %v", err)
	}
	unsupportedRequest.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: adminGrant.Token})
	unsupported, err := server.Client().Do(unsupportedRequest)
	if err != nil {
		t.Fatalf("POST /api/events error = %v", err)
	}
	defer unsupported.Body.Close()
	if unsupported.StatusCode != http.StatusMethodNotAllowed {
		body, _ := io.ReadAll(unsupported.Body)
		t.Fatalf("POST /api/events = (%d, %s)", unsupported.StatusCode, body)
	}
	unsupportedBody, _ := io.ReadAll(unsupported.Body)
	assertServerHTTPError(t, unsupportedBody, "METHOD_NOT_ALLOWED")
}

func TestServerHTTPHandlerStreamsAdministratorRoleInvalidation(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adminGrant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	adminResponse, adminEvents := openServerHTTPEvents(t, server.Client(), server.URL, adminGrant.Token)
	defer adminResponse.Body.Close()
	aliceResponse, aliceEvents := openServerHTTPEvents(t, server.Client(), server.URL, aliceGrant.Token)
	defer aliceResponse.Body.Close()
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator initial event = %q, want changed", event)
	}
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "changed" {
		t.Fatalf("alice initial event = %q, want changed", event)
	}

	request := httptest.NewRequest(http.MethodPatch, "http://tunnel.example.test/api/accounts/"+alice.ID, strings.NewReader(`{"role":"admin"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://tunnel.example.test")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: adminGrant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH /api/accounts/:id = (%d, %s)", response.Code, response.Body.String())
	}
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator account event = %q, want changed", event)
	}
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "session_revoked" {
		t.Fatalf("changed-account event = %q, want session_revoked", event)
	}
	if _, err := readServerHTTPEvent(aliceEvents); err != io.EOF {
		t.Fatalf("changed-account stream after revocation error = %v, want EOF", err)
	}
}

func TestServerHTTPHandlerStreamsAdministratorLocalAccountCreationInvalidation(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	if _, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adminGrant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	adminResponse, adminEvents := openServerHTTPEvents(t, server.Client(), server.URL, adminGrant.Token)
	defer adminResponse.Body.Close()
	aliceResponse, aliceEvents := openServerHTTPEvents(t, server.Client(), server.URL, aliceGrant.Token)
	defer aliceResponse.Body.Close()
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator initial event = %q, want changed", event)
	}
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "changed" {
		t.Fatalf("alice initial event = %q, want changed", event)
	}

	request := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/accounts", strings.NewReader(`{"username":"operator","password":"operator-secret"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://tunnel.example.test")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: adminGrant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/accounts = (%d, %s)", response.Code, response.Body.String())
	}
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator account-create event = %q, want changed", event)
	}
	assertNoServerHTTPEvent(t, aliceEvents, aliceResponse.Body)
}

func TestServerHTTPHandlerStreamsAdministratorLocalAccountPasswordResetInvalidation(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adminGrant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	adminResponse, adminEvents := openServerHTTPEvents(t, server.Client(), server.URL, adminGrant.Token)
	defer adminResponse.Body.Close()
	aliceResponse, aliceEvents := openServerHTTPEvents(t, server.Client(), server.URL, aliceGrant.Token)
	defer aliceResponse.Body.Close()
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator initial event = %q, want changed", event)
	}
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "changed" {
		t.Fatalf("alice initial event = %q, want changed", event)
	}

	request := httptest.NewRequest(http.MethodPut, "http://tunnel.example.test/api/accounts/"+alice.ID+"/password", strings.NewReader(`{"password":"replacement-secret"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://tunnel.example.test")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: adminGrant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("PUT /api/accounts/:id/password = (%d, %s)", response.Code, response.Body.String())
	}
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator password-reset event = %q, want changed", event)
	}
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "session_revoked" {
		t.Fatalf("reset-account event = %q, want session_revoked", event)
	}
	if _, err := readServerHTTPEvent(aliceEvents); err != io.EOF {
		t.Fatalf("reset-account stream after revocation error = %v, want EOF", err)
	}
}

func TestServerHTTPHandlerStreamsAdministratorLocalAccountDeletionInvalidation(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane})
	if err != nil {
		t.Fatalf("NewServerHTTPHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adminGrant, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	aliceGrant, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn(alice) error = %v", err)
	}
	adminResponse, adminEvents := openServerHTTPEvents(t, server.Client(), server.URL, adminGrant.Token)
	defer adminResponse.Body.Close()
	aliceResponse, aliceEvents := openServerHTTPEvents(t, server.Client(), server.URL, aliceGrant.Token)
	defer aliceResponse.Body.Close()
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator initial event = %q, want changed", event)
	}
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "changed" {
		t.Fatalf("alice initial event = %q, want changed", event)
	}

	request := httptest.NewRequest(http.MethodDelete, "http://tunnel.example.test/api/accounts/"+alice.ID, nil)
	request.Header.Set("Origin", "https://tunnel.example.test")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: adminGrant.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/accounts/:id = (%d, %s)", response.Code, response.Body.String())
	}
	if event := waitForServerHTTPEvent(t, adminEvents); event != "changed" {
		t.Fatalf("administrator account-deletion event = %q, want changed", event)
	}
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "session_revoked" {
		t.Fatalf("deleted-account event = %q, want session_revoked", event)
	}
	if _, err := readServerHTTPEvent(aliceEvents); err != io.EOF {
		t.Fatalf("deleted-account stream after revocation error = %v, want EOF", err)
	}
}

func openServerHTTPEvents(t *testing.T, client *http.Client, serverURL, token string) (*http.Response, *bufio.Reader) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, serverURL+"/api/events", nil)
	if err != nil {
		t.Fatalf("NewRequest(events) error = %v", err)
	}
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET /api/events error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		t.Fatalf("GET /api/events = (%d, %s)", response.StatusCode, body)
	}
	assertServerHTTPEventHeaders(t, response)
	return response, bufio.NewReader(response.Body)
}

func waitForServerHTTPEvent(t *testing.T, events *bufio.Reader) string {
	t.Helper()
	result := make(chan struct {
		event string
		err   error
	}, 1)
	go func() {
		event, err := readServerHTTPEvent(events)
		result <- struct {
			event string
			err   error
		}{event: event, err: err}
	}()
	select {
	case result := <-result:
		if result.err != nil {
			t.Fatalf("read event: %v", result.err)
		}
		return result.event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return ""
	}
}

func assertNoServerHTTPEvent(t *testing.T, events *bufio.Reader, body io.ReadCloser) {
	t.Helper()
	result := make(chan struct {
		event string
		err   error
	}, 1)
	go func() {
		event, err := readServerHTTPEvent(events)
		result <- struct {
			event string
			err   error
		}{event: event, err: err}
	}()
	select {
	case result := <-result:
		t.Fatalf("unexpected event for unrelated owner = (%q, %v)", result.event, result.err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := body.Close(); err != nil {
		t.Fatalf("close unrelated event stream: %v", err)
	}
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("unrelated event reader did not stop after close")
	}
}

func readServerHTTPEvent(events *bufio.Reader) (string, error) {
	line, err := events.ReadString('\n')
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(line, "data: ") {
		return "", errors.New("event line does not start with data")
	}
	if separator, err := events.ReadString('\n'); err != nil {
		return "", err
	} else if separator != "\n" {
		return "", errors.New("event is missing its blank line")
	}
	var payload struct {
		Version int    `json:"version"`
		Event   string `json:"event"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSuffix(line, "\n"), "data: ")), &payload); err != nil {
		return "", err
	}
	if payload.Version != 1 {
		return "", errors.New("event has an unsupported version")
	}
	return payload.Event, nil
}

func serverHTTPReadRequest(handler http.Handler, method, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://tunnel.example.test"+path, nil)
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestServerHTTPErrorMapsStorageAndUnexpectedFailures(t *testing.T) {
	storage := httptest.NewRecorder()
	writeServerHTTPDomainError(storage, serverDomainError("SESSION_UNAVAILABLE", "Session storage is unavailable"))
	if storage.Code != http.StatusServiceUnavailable {
		t.Fatalf("session error status = %d", storage.Code)
	}
	assertServerHTTPHeaders(t, storage.Result())
	assertServerHTTPError(t, storage.Body.Bytes(), "SESSION_UNAVAILABLE")

	unexpected := httptest.NewRecorder()
	writeServerHTTPDomainError(unexpected, errors.New("database driver detail"))
	if unexpected.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected error status = %d", unexpected.Code)
	}
	assertServerHTTPError(t, unexpected.Body.Bytes(), "INTERNAL_ERROR")
	if strings.Contains(unexpected.Body.String(), "database driver detail") {
		t.Fatalf("unexpected error leaked internal detail: %s", unexpected.Body.String())
	}
}

func assertServerHTTPHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	for name, want := range serverAPIHeaders {
		if got := response.Header.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if got := response.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func assertServerHTTPAgentHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	for name := range serverAPIHeaders {
		if got := response.Header.Get(name); got != "" {
			t.Fatalf("agent %s = %q, want empty", name, got)
		}
	}
	if got := response.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("agent Content-Type = %q", got)
	}
}

func assertServerHTTPEventHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	for name, want := range serverAPIHeaders {
		if got := response.Header.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q", got)
	}
}

func assertServerHTTPNoContentHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	for name, want := range serverAPIHeaders {
		if got := response.Header.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if got := response.Header.Get("Content-Type"); got != "" {
		t.Fatalf("Content-Type = %q, want empty", got)
	}
}

func assertServerHTTPError(t *testing.T, source []byte, wantCode string) {
	t.Helper()
	var body struct {
		Version int `json:"version"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(source, &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Version != 1 || body.Error.Code != wantCode || body.Error.Message == "" {
		t.Fatalf("error response = %#v, want %q", body, wantCode)
	}
}
