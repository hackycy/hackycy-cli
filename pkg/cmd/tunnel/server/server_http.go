package server

import (
	"context"
	"encoding/json"
	"errors"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const serverSessionCookieName = "ycy_tunnel_session"

const serverHTTPBodyLimit = 128 << 20

const serverAgentWebSocketPayloadLimit = 1 << 20

var serverAgentWebSocketPingInterval = 30 * time.Second

// Agent authentication is exclusively Bearer-token based, so it retains the
// legacy endpoint's lack of an Origin requirement.
var serverAgentWebSocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

var serverAPIHeaders = map[string]string{
	"Cache-Control":           "no-store",
	"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	"Referrer-Policy":         "no-referrer",
	"X-Content-Type-Options":  "nosniff",
}

// ServerHTTPOptions contains the dependencies needed by the Tunnel control
// plane's HTTP adapter. Resource routes and remaining mutations are added in
// later slices.
type ServerHTTPOptions struct {
	Sessions            *ServerSessions
	Accounts            *ServerAccounts
	ControlPlane        *ServerControlPlane
	Runtime             ServerClientRuntimeProvider
	FRPS                ServerFRPSController
	Custom404PageReader ServerFRPSCustom404PageReader
	Custom404PageWriter ServerFRPSCustom404PageWriter
	FRPSChanges         ServerFRPSChangeObserver
	AgentGateway        *ServerAgentGateway
	ServerState         ServerHTTPStateProvider
}

// ServerHTTPStateProvider exposes the current redacted deployment state for
// administrator overview responses.
type ServerHTTPStateProvider interface {
	State() ServerHTTPState
}

// ServerHTTPState contains the server-only state safe to project to an
// authenticated administrator. Secrets deliberately have no representation.
type ServerHTTPState struct {
	FRPS     tunnelruntime.FRPSupervisorState
	Settings ServerHTTPServerSettings
}

type ServerHTTPServerSettings struct {
	Address          string                `json:"address"`
	ControlPort      int                   `json:"controlPort"`
	FRPPort          int                   `json:"frpPort"`
	HTTPPort         int                   `json:"httpPort"`
	PortRange        ServerHTTPPortRange   `json:"portRange"`
	AdvertiseFRPAddr *ServerHTTPFRPAddress `json:"advertiseFrpAddress,omitempty"`
	DataDir          string                `json:"dataDir"`
	AdminUser        string                `json:"adminUser"`
}

type ServerHTTPPortRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type ServerHTTPFRPAddress struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// ServerHTTPHandler exposes the current Tunnel control-plane HTTP surface.
// It owns HTTP formatting and cookies while the session owner retains durable
// credential and revocation behavior.
type ServerHTTPHandler struct {
	sessions            *ServerSessions
	accounts            *ServerAccounts
	controlPlane        *ServerControlPlane
	runtime             ServerClientRuntimeProvider
	frps                ServerFRPSController
	custom404PageReader ServerFRPSCustom404PageReader
	custom404PageWriter ServerFRPSCustom404PageWriter
	frpsChanges         ServerFRPSChangeObserver
	agentGateway        *ServerAgentGateway
	serverState         ServerHTTPStateProvider
}

func NewServerHTTPHandler(options ServerHTTPOptions) (*ServerHTTPHandler, error) {
	if options.Sessions == nil {
		return nil, errors.New("Tunnel server HTTP sessions are required")
	}
	runtime := options.Runtime
	if runtime == nil && options.AgentGateway != nil {
		runtime = options.AgentGateway
	}
	return &ServerHTTPHandler{
		sessions:            options.Sessions,
		accounts:            options.Accounts,
		controlPlane:        options.ControlPlane,
		runtime:             runtime,
		frps:                options.FRPS,
		custom404PageReader: options.Custom404PageReader,
		custom404PageWriter: options.Custom404PageWriter,
		frpsChanges:         options.FRPSChanges,
		agentGateway:        options.AgentGateway,
		serverState:         options.ServerState,
	}, nil
}

func (handler *ServerHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/healthz":
		handler.serveHealth(writer, request)
	case "/api/session":
		handler.serveSession(writer, request)
	case "/api/session/password":
		handler.serveSessionPassword(writer, request)
	case "/api/agent":
		handler.serveAgent(writer, request)
	case "/api/events":
		handler.serveEvents(writer, request)
	case "/api/state":
		handler.serveState(writer, request)
	case "/api/server/frp/start":
		handler.serveFRPSControl(writer, request, ServerFRPSActionStart)
	case "/api/server/frp/stop":
		handler.serveFRPSControl(writer, request, ServerFRPSActionStop)
	case "/api/server/frp/restart":
		handler.serveFRPSControl(writer, request, ServerFRPSActionRestart)
	case "/api/server/frps/config/custom-404-page":
		handler.serveCustom404Page(writer, request)
	case "/api/accounts":
		handler.serveAccounts(writer, request)
	case "/api/clients":
		handler.serveClients(writer, request)
	default:
		if accountID, found := serverClientRouteID(request.URL.Path, "/api/accounts/", "/password"); found {
			handler.serveAccountPassword(writer, request, accountID)
			return
		}
		if accountID, found := serverClientRouteID(request.URL.Path, "/api/accounts/", ""); found {
			handler.serveAccount(writer, request, accountID)
			return
		}
		if clientID, found := serverClientRouteID(request.URL.Path, "/api/clients/", "/rotate"); found {
			handler.serveClientRotation(writer, request, clientID)
			return
		}
		if clientID, found := serverClientRouteID(request.URL.Path, "/api/clients/", "/restart"); found {
			handler.serveClientRestart(writer, request, clientID)
			return
		}
		if clientID, found := serverClientRouteID(request.URL.Path, "/api/clients/", "/tunnels/import/preview"); found {
			handler.serveTunnelImportPreview(writer, request, clientID)
			return
		}
		if clientID, found := serverClientRouteID(request.URL.Path, "/api/clients/", "/tunnels/import"); found {
			handler.serveTunnelImport(writer, request, clientID)
			return
		}
		if clientID, found := serverClientRouteID(request.URL.Path, "/api/clients/", "/tunnels"); found {
			handler.serveClientTunnels(writer, request, clientID)
			return
		}
		if clientID, found := serverClientRouteID(request.URL.Path, "/api/clients/", ""); found {
			handler.serveClient(writer, request, clientID)
			return
		}
		if tunnelID, found := serverClientRouteID(request.URL.Path, "/api/tunnels/", ""); found {
			handler.serveTunnel(writer, request, tunnelID)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/api/") {
			session := handler.authenticatedSession(writer, request)
			if session == nil {
				return
			}
			writeServerHTTPAuthenticatedError(writer, session, http.StatusNotFound, "NOT_FOUND", "Route not found")
			return
		}
		writeServerHTTPError(writer, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}
}

func (handler *ServerHTTPHandler) serveAgent(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeServerHTTPError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET with WebSocket upgrade")
		return
	}
	if handler.agentGateway == nil {
		writeServerHTTPError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	reservation, err := handler.agentGateway.Authorize(request.Context(), request.Header.Get("Authorization"))
	if err != nil {
		writeServerHTTPAgentDomainError(writer, err)
		return
	}
	if !websocket.IsWebSocketUpgrade(request) {
		reservation.Release()
		writeServerHTTPError(writer, http.StatusUpgradeRequired, "UPGRADE_REQUIRED", "WebSocket upgrade is required")
		return
	}
	requestHost := serverAgentRequestHostname(request)
	socket, err := serverAgentWebSocketUpgrader.Upgrade(writer, request, nil)
	if err != nil {
		handler.agentGateway.protocolWarning(reservation.ClientID(), "protocol")
		reservation.Release()
		return
	}
	connection := reservation.Activate()
	if connection == nil {
		_ = socket.Close()
		return
	}
	connection.AttachCloser(func(protocolError *ServerAgentProtocolError) {
		closeServerAgentSocket(socket, protocolError)
		_ = socket.Close()
	})
	if err := connection.AcknowledgeReplacementToken(request.Context()); err != nil {
		handler.agentGateway.protocolWarning(connection.ClientID(), "control-plane")
		closeServerAgentSocket(socket, &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server control plane is unavailable"})
		_ = socket.Close()
		connection.Close()
		return
	}
	stopLiveness := startServerAgentWebSocketLiveness(socket, func() {
		handler.agentGateway.protocolWarning(connection.ClientID(), "liveness")
	})
	defer func() {
		stopLiveness()
		_ = socket.Close()
		connection.Close()
	}()
	socket.SetReadLimit(serverAgentWebSocketPayloadLimit)
	welcomePresented := false
	for {
		_, reader, err := socket.NextReader()
		if err != nil {
			return
		}
		source, err := io.ReadAll(reader)
		if err != nil {
			return
		}
		if !welcomePresented {
			if protocolError := connection.AcceptHello(request.Context(), source); protocolError != nil {
				handler.agentGateway.protocolWarning(connection.ClientID(), serverAgentWarningCategory(protocolError))
				closeServerAgentSocket(socket, protocolError)
				return
			}
			if protocolError := connection.PresentWelcome(request.Context(), requestHost, socket.WriteJSON); protocolError != nil {
				handler.agentGateway.protocolWarning(connection.ClientID(), serverAgentWarningCategory(protocolError))
				closeServerAgentSocket(socket, protocolError)
				return
			}
			welcomePresented = true
			continue
		}
		if protocolError := connection.AcceptActiveMessage(request.Context(), source); protocolError != nil {
			handler.agentGateway.protocolWarning(connection.ClientID(), serverAgentWarningCategory(protocolError))
			closeServerAgentSocket(socket, protocolError)
			return
		}
	}
}

func closeServerAgentSocket(socket *websocket.Conn, protocolError *ServerAgentProtocolError) {
	if socket == nil || protocolError == nil {
		return
	}
	_ = socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(protocolError.CloseCode, protocolError.Message), time.Now().Add(time.Second))
}

// startServerAgentWebSocketLiveness retains v3's open-ended hello phase while
// requiring a pong after every server ping interval.
func startServerAgentWebSocketLiveness(socket *websocket.Conn, onTimeout ...func()) func() {
	if socket == nil {
		return func() {}
	}
	done := make(chan struct{})
	var stopOnce sync.Once
	var mu sync.Mutex
	awaitingPong := false
	socket.SetPongHandler(func(string) error {
		mu.Lock()
		awaitingPong = false
		mu.Unlock()
		return nil
	})
	go func() {
		ticker := time.NewTicker(serverAgentWebSocketPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			mu.Lock()
			if awaitingPong {
				mu.Unlock()
				if len(onTimeout) > 0 && onTimeout[0] != nil {
					onTimeout[0]()
				}
				closeServerAgentSocket(socket, &ServerAgentProtocolError{
					CloseCode: serverAgentCloseLivenessTimeout,
					Message:   "Control connection timed out",
				})
				_ = socket.Close()
				return
			}
			awaitingPong = true
			mu.Unlock()
			if err := socket.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
				_ = socket.Close()
				return
			}
		}
	}()
	return func() {
		stopOnce.Do(func() { close(done) })
	}
}

func serverAgentWarningCategory(protocolError *ServerAgentProtocolError) string {
	if protocolError == nil {
		return "protocol"
	}
	switch protocolError.CloseCode {
	case serverAgentCloseIncompatible:
		return "incompatible"
	case serverAgentCloseLivenessTimeout:
		return "liveness"
	case serverAgentCloseFRPSUnavailable:
		return "frps"
	case serverAgentCloseRevoked:
		return "revoked"
	default:
		return "protocol"
	}
}

func serverAgentRequestHostname(request *http.Request) string {
	if request == nil {
		return ""
	}
	host := request.Host
	if host == "" {
		return request.URL.Hostname()
	}
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		return hostname
	}
	return strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
}

func (handler *ServerHTTPHandler) serveClients(writer http.ResponseWriter, request *http.Request) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method == http.MethodPost {
		handler.createClient(writer, request, session, workspace)
		return
	}
	if request.Method != http.MethodGet {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET or POST")
		return
	}
	clients, err := workspace.ListClients(request.Context())
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	views := make([]serverHTTPClientView, 0, len(clients))
	for _, client := range clients {
		view, err := handler.presentClient(request.Context(), workspace, client)
		if err != nil {
			writeServerHTTPAuthenticatedDomainError(writer, session, err)
			return
		}
		views = append(views, view)
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int                    `json:"version"`
		Clients []serverHTTPClientView `json:"clients"`
	}{Version: 1, Clients: views})
}

func (handler *ServerHTTPHandler) serveState(writer http.ResponseWriter, request *http.Request) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method != http.MethodGet {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
		return
	}
	account, err := workspace.Account(request.Context())
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	clients, err := workspace.ListClients(request.Context())
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	counts := serverHTTPStateCounts{Clients: len(clients)}
	for _, client := range clients {
		runtime := handler.clientRuntime(client.ID)
		if runtime.ConnectionState == ServerClientConnected {
			counts.Connected++
		}
		tunnels, err := workspace.ListTunnels(request.Context(), client.ID)
		if err != nil {
			writeServerHTTPAuthenticatedDomainError(writer, session, err)
			return
		}
		counts.Tunnels += len(tunnels)
		for _, tunnel := range tunnels {
			switch serverTunnelPresentationStateFor(tunnel, client, runtime) {
			case ServerTunnelPending:
				counts.Pending++
			case ServerTunnelError:
				counts.Errors++
			}
		}
	}
	response := serverHTTPStateView{
		Version: 1,
		Account: presentServerHTTPAccount(account),
		Counts:  counts,
	}
	if account.Role == AccountRoleAdmin {
		if handler.serverState == nil {
			writeServerHTTPAuthenticatedError(writer, session, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
			return
		}
		server := presentServerHTTPServerState(handler.serverState.State())
		response.Server = &server
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, response)
}

func (handler *ServerHTTPHandler) serveFRPSControl(writer http.ResponseWriter, request *http.Request, action ServerFRPSAction) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method != http.MethodPost {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST")
		return
	}
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	if handler.serverState == nil {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	if err := workspace.ControlFRPS(request.Context(), action); err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int                  `json:"version"`
		Server  serverHTTPServerView `json:"server"`
	}{Version: 1, Server: presentServerHTTPServerState(handler.serverState.State())})
}

func (handler *ServerHTTPHandler) serveCustom404Page(writer http.ResponseWriter, request *http.Request) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.readCustom404Page(writer, request, session, workspace)
	case http.MethodPut:
		handler.writeCustom404Page(writer, request, session, workspace)
	default:
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET or PUT")
	}
}

func (handler *ServerHTTPHandler) readCustom404Page(writer http.ResponseWriter, request *http.Request, session *ServerSession, workspace *ServerWorkspace) {
	content, err := workspace.ReadCustom404Page(request.Context())
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int    `json:"version"`
		Content string `json:"content"`
	}{Version: 1, Content: content})
}

func (handler *ServerHTTPHandler) writeCustom404Page(writer http.ResponseWriter, request *http.Request, session *ServerSession, workspace *ServerWorkspace) {
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPCustom404PageInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	if err := workspace.WriteCustom404Page(request.Context(), input.Content); err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int    `json:"version"`
		Content string `json:"content"`
	}{Version: 1, Content: input.Content})
}

func (handler *ServerHTTPHandler) serveAccounts(writer http.ResponseWriter, request *http.Request) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method == http.MethodPost {
		handler.createAccount(writer, request, session, workspace)
		return
	}
	if request.Method != http.MethodGet {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET or POST")
		return
	}
	accounts, err := workspace.ListAccounts(request.Context())
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	views := make([]serverHTTPAccountListView, 0, len(accounts))
	for _, account := range accounts {
		views = append(views, presentServerHTTPAccountList(account))
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version  int                         `json:"version"`
		Accounts []serverHTTPAccountListView `json:"accounts"`
	}{Version: 1, Accounts: views})
}

func (handler *ServerHTTPHandler) createAccount(writer http.ResponseWriter, request *http.Request, session *ServerSession, workspace *ServerWorkspace) {
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPAccountCreateInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	account, err := workspace.CreateLocalAccount(request.Context(), input.Username, input.Password, input.Role)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	view := presentServerHTTPAccountList(ServerAccountView{ServerAccount: account})
	writeServerAuthenticatedJSON(writer, session, http.StatusCreated, struct {
		Version int                       `json:"version"`
		Account serverHTTPAccountListView `json:"account"`
	}{Version: 1, Account: view})
}

func (handler *ServerHTTPHandler) serveAccount(writer http.ResponseWriter, request *http.Request, accountID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	switch request.Method {
	case http.MethodPatch:
		handler.updateAccountRole(writer, request, session, workspace, accountID)
	case http.MethodDelete:
		handler.deleteAccount(writer, request, session, workspace, accountID)
	default:
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use PATCH or DELETE")
	}
}

func (handler *ServerHTTPHandler) updateAccountRole(writer http.ResponseWriter, request *http.Request, session *ServerSession, workspace *ServerWorkspace, accountID string) {
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPAccountRoleInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	account, err := workspace.ChangeLocalAccountRole(request.Context(), accountID, input.Role)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	accounts, err := handler.accounts.ListAccounts(request.Context())
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	var view serverHTTPAccountListView
	found := false
	for _, candidate := range accounts {
		if candidate.ID == account.ID {
			view = presentServerHTTPAccountList(candidate)
			found = true
			break
		}
	}
	if !found {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	response := struct {
		Version int                       `json:"version"`
		Account serverHTTPAccountListView `json:"account"`
	}{Version: 1, Account: view}
	if account.ID == session.Account.ID {
		http.SetCookie(writer, &http.Cookie{
			Name:     serverSessionCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		writeServerHTTPJSON(writer, http.StatusOK, response)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, response)
}

func (handler *ServerHTTPHandler) serveAccountPassword(writer http.ResponseWriter, request *http.Request, accountID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method != http.MethodPut {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use PUT")
		return
	}
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPAccountPasswordInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	account, err := workspace.ResetLocalAccountPassword(request.Context(), accountID, input.Password)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	if account.ID == session.Account.ID {
		http.SetCookie(writer, &http.Cookie{
			Name:     serverSessionCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		writeServerHTTPNoContent(writer)
		return
	}
	writeServerAuthenticatedNoContent(writer, session)
}

func (handler *ServerHTTPHandler) deleteAccount(writer http.ResponseWriter, request *http.Request, session *ServerSession, workspace *ServerWorkspace, accountID string) {
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	if err := workspace.DeleteLocalAccount(request.Context(), accountID); err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	if accountID == session.Account.ID {
		http.SetCookie(writer, &http.Cookie{
			Name:     serverSessionCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		writeServerHTTPNoContent(writer)
		return
	}
	writeServerAuthenticatedNoContent(writer, session)
}

func (handler *ServerHTTPHandler) createClient(writer http.ResponseWriter, request *http.Request, session *ServerSession, workspace *ServerWorkspace) {
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPClientCreateInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	client, err := workspace.CreateClient(request.Context(), input.Remark.Value)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	view, err := handler.presentClient(request.Context(), workspace, client)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusCreated, struct {
		Version int                  `json:"version"`
		Client  serverHTTPClientView `json:"client"`
	}{Version: 1, Client: view})
}

type serverHTTPClientCreateInput struct {
	Remark serverHTTPOptionalString `json:"remark"`
}

type serverHTTPOptionalString struct {
	Value string
}

func (value *serverHTTPOptionalString) UnmarshalJSON(source []byte) error {
	if string(source) == "null" {
		return errors.New("expected JSON string")
	}
	if err := json.Unmarshal(source, &value.Value); err != nil {
		return err
	}
	return nil
}

func (handler *ServerHTTPHandler) serveClient(writer http.ResponseWriter, request *http.Request, clientID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method == http.MethodPatch {
		handler.updateClientRemark(writer, request, session, workspace, clientID)
		return
	}
	if request.Method == http.MethodDelete {
		handler.deleteClient(writer, request, session, workspace, clientID)
		return
	}
	if request.Method != http.MethodGet {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET, PATCH, or DELETE")
		return
	}
	client, err := workspace.GetClient(request.Context(), clientID)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	view, err := handler.presentClient(request.Context(), workspace, client)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	tunnels, err := handler.presentTunnels(request.Context(), workspace, client)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int                      `json:"version"`
		Client  serverHTTPClientView     `json:"client"`
		Tunnels []serverHTTPPublicTunnel `json:"tunnels"`
	}{Version: 1, Client: view, Tunnels: tunnels})
}

func (handler *ServerHTTPHandler) updateClientRemark(writer http.ResponseWriter, request *http.Request, session *ServerSession, workspace *ServerWorkspace, clientID string) {
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPClientRemarkInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	if input.Remark == nil {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid")
		return
	}
	client, err := workspace.UpdateClientRemark(request.Context(), clientID, *input.Remark)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	view, err := handler.presentClient(request.Context(), workspace, client)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int                  `json:"version"`
		Client  serverHTTPClientView `json:"client"`
	}{Version: 1, Client: view})
}

func (handler *ServerHTTPHandler) serveClientRotation(writer http.ResponseWriter, request *http.Request, clientID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method != http.MethodPost {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST")
		return
	}
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	client, err := workspace.RotateClientToken(request.Context(), clientID)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	view, err := handler.presentClient(request.Context(), workspace, client)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int                  `json:"version"`
		Client  serverHTTPClientView `json:"client"`
	}{Version: 1, Client: view})
}

func (handler *ServerHTTPHandler) serveClientRestart(writer http.ResponseWriter, request *http.Request, clientID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method != http.MethodPost {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST")
		return
	}
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	if _, err := workspace.GetClient(request.Context(), clientID); err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	if handler.agentGateway == nil || !handler.agentGateway.RestartFRPC(clientID) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusConflict, "CLIENT_OFFLINE", "Trusted Tunnel Client is not connected")
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusAccepted, struct {
		Version  int  `json:"version"`
		Accepted bool `json:"accepted"`
	}{Version: 1, Accepted: true})
}

type serverHTTPClientRemarkInput struct {
	Remark *string `json:"remark"`
}

func (handler *ServerHTTPHandler) deleteClient(writer http.ResponseWriter, request *http.Request, session *ServerSession, workspace *ServerWorkspace, clientID string) {
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	if err := workspace.DeleteClient(request.Context(), clientID); err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedNoContent(writer, session)
}

func (handler *ServerHTTPHandler) serveClientTunnels(writer http.ResponseWriter, request *http.Request, clientID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method == http.MethodPost {
		handler.createTunnel(writer, request, session, workspace, clientID)
		return
	}
	if request.Method != http.MethodGet {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET or POST")
		return
	}
	client, err := workspace.GetClient(request.Context(), clientID)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	tunnels, err := handler.presentTunnels(request.Context(), workspace, client)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int                      `json:"version"`
		Tunnels []serverHTTPPublicTunnel `json:"tunnels"`
	}{Version: 1, Tunnels: tunnels})
}

func (handler *ServerHTTPHandler) createTunnel(writer http.ResponseWriter, request *http.Request, session *ServerSession, workspace *ServerWorkspace, clientID string) {
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPTunnelCreateInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	tunnel, err := workspace.CreateTunnel(request.Context(), clientID, input.Mutation)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	client, err := workspace.GetClient(request.Context(), clientID)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	view := presentServerHTTPPublicTunnel(tunnel, client, handler.clientRuntime(client.ID))
	writeServerAuthenticatedJSON(writer, session, http.StatusCreated, struct {
		Version int                    `json:"version"`
		Tunnel  serverHTTPPublicTunnel `json:"tunnel"`
	}{Version: 1, Tunnel: view})
}

func (handler *ServerHTTPHandler) serveTunnelImportPreview(writer http.ResponseWriter, request *http.Request, clientID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method != http.MethodPost {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST")
		return
	}
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPTunnelImportPreviewInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	preview, err := workspace.PreviewFRPCTunnelImport(request.Context(), clientID, input.Source)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int `json:"version"`
		TunnelImportPreview
	}{Version: 1, TunnelImportPreview: preview})
}

func (handler *ServerHTTPHandler) serveTunnelImport(writer http.ResponseWriter, request *http.Request, clientID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method != http.MethodPost {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST")
		return
	}
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPTunnelImportInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	tunnels, err := workspace.ImportFRPCTunnels(request.Context(), clientID, input.Source, input.CandidateIDs)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	client, err := workspace.GetClient(request.Context(), clientID)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	views := make([]serverHTTPPublicTunnel, 0, len(tunnels))
	for _, tunnel := range tunnels {
		views = append(views, presentServerHTTPPublicTunnel(tunnel, client, handler.clientRuntime(client.ID)))
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusCreated, struct {
		Version int                      `json:"version"`
		Tunnels []serverHTTPPublicTunnel `json:"tunnels"`
	}{Version: 1, Tunnels: views})
}

func (handler *ServerHTTPHandler) serveTunnel(writer http.ResponseWriter, request *http.Request, tunnelID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method == http.MethodPatch {
		handler.updateTunnel(writer, request, session, workspace, tunnelID)
		return
	}
	if request.Method == http.MethodDelete {
		handler.deleteTunnel(writer, request, session, workspace, tunnelID)
		return
	}
	writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use PATCH or DELETE")
}

func (handler *ServerHTTPHandler) updateTunnel(writer http.ResponseWriter, request *http.Request, session *ServerSession, workspace *ServerWorkspace, tunnelID string) {
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPTunnelPatchInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	tunnel, err := workspace.UpdateTunnel(request.Context(), tunnelID, input.Patch)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	stored, err := workspace.GetTunnel(request.Context(), tunnelID)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	client, err := workspace.GetClient(request.Context(), stored.ClientID)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	view := presentServerHTTPPublicTunnel(tunnel, client, handler.clientRuntime(client.ID))
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int                    `json:"version"`
		Tunnel  serverHTTPPublicTunnel `json:"tunnel"`
	}{Version: 1, Tunnel: view})
}

func (handler *ServerHTTPHandler) deleteTunnel(writer http.ResponseWriter, request *http.Request, session *ServerSession, workspace *ServerWorkspace, tunnelID string) {
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	if err := workspace.DeleteTunnel(request.Context(), tunnelID); err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedNoContent(writer, session)
}

func (handler *ServerHTTPHandler) authenticatedWorkspace(writer http.ResponseWriter, request *http.Request) (*ServerSession, *ServerWorkspace) {
	session := handler.authenticatedSession(writer, request)
	if session == nil {
		return nil, nil
	}
	if handler.accounts == nil || handler.controlPlane == nil {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return nil, nil
	}
	workspace, err := OpenServerWorkspace(request.Context(), ServerWorkspaceDependencies{
		Sessions:            handler.sessions,
		Accounts:            handler.accounts,
		ControlPlane:        handler.controlPlane,
		FRPS:                handler.frps,
		Custom404PageReader: handler.custom404PageReader,
		Custom404PageWriter: handler.custom404PageWriter,
		FRPSChanges:         handler.frpsChanges,
	}, session.Token)
	if err != nil {
		writeServerHTTPDomainError(writer, err)
		return nil, nil
	}
	return session, workspace
}

func serverClientRouteID(path, prefix, suffix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	value := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if value == "" || strings.Contains(value, "/") {
		return "", false
	}
	return value, true
}

type ServerClientConnectionState string

const (
	ServerClientDisconnected      ServerClientConnectionState = "disconnected"
	ServerClientConnected         ServerClientConnectionState = "connected"
	ServerClientIncompatible      ServerClientConnectionState = "incompatible"
	ServerClientRevocationPending ServerClientConnectionState = "revocation_pending"
)

type ServerClientRuntimeState struct {
	ConnectionState ServerClientConnectionState           `json:"connectionState"`
	ProcessState    tunnelruntime.FRPProcessState         `json:"processState"`
	LastError       *tunnelruntime.StructuredRuntimeError `json:"lastError,omitempty"`
}

// ServerClientRuntimeProvider supplies process-local state. Durable client
// records remain the sole source of desired and applied revisions.
type ServerClientRuntimeProvider interface {
	State(clientID string) ServerClientRuntimeState
}

func (handler *ServerHTTPHandler) clientRuntime(clientID string) ServerClientRuntimeState {
	if handler.runtime != nil {
		return handler.runtime.State(clientID)
	}
	return ServerClientRuntimeState{ConnectionState: ServerClientDisconnected, ProcessState: tunnelruntime.FRPProcessStopped}
}

type serverHTTPClientView struct {
	ID                  string                   `json:"id"`
	Remark              string                   `json:"remark"`
	Token               string                   `json:"token"`
	DesiredRevision     int64                    `json:"desiredRevision"`
	LastAppliedRevision int64                    `json:"lastAppliedRevision"`
	RevocationPending   bool                     `json:"revocationPending"`
	CreatedAt           string                   `json:"createdAt"`
	RotatedAt           *string                  `json:"rotatedAt"`
	Owner               serverHTTPClientOwner    `json:"owner"`
	Runtime             ServerClientRuntimeState `json:"runtime"`
	TunnelCounts        serverHTTPTunnelCounts   `json:"tunnelCounts"`
}

type serverHTTPClientOwner struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type serverHTTPTunnelCounts struct {
	Total   int `json:"total"`
	Enabled int `json:"enabled"`
	Applied int `json:"applied"`
	Pending int `json:"pending"`
	Error   int `json:"error"`
}

type serverHTTPStateView struct {
	Version int                   `json:"version"`
	Account serverHTTPAccountView `json:"account"`
	Counts  serverHTTPStateCounts `json:"counts"`
	Server  *serverHTTPServerView `json:"server,omitempty"`
}

type serverHTTPStateCounts struct {
	Clients   int `json:"clients"`
	Connected int `json:"connected"`
	Tunnels   int `json:"tunnels"`
	Pending   int `json:"pending"`
	Errors    int `json:"errors"`
}

type serverHTTPServerView struct {
	FRPS     serverHTTPFRPSView       `json:"frps"`
	Settings ServerHTTPServerSettings `json:"settings"`
}

func presentServerHTTPServerState(state ServerHTTPState) serverHTTPServerView {
	return serverHTTPServerView{
		FRPS: serverHTTPFRPSView{
			State: state.FRPS.State,
			PID:   state.FRPS.PID,
			Error: state.FRPS.Error,
		},
		Settings: state.Settings,
	}
}

type serverHTTPFRPSView struct {
	State tunnelruntime.FRPProcessState         `json:"state"`
	PID   *int                                  `json:"pid,omitempty"`
	Error *tunnelruntime.StructuredRuntimeError `json:"error,omitempty"`
}

type ServerTunnelPresentationState string

const (
	ServerTunnelDisabled ServerTunnelPresentationState = "Disabled"
	ServerTunnelPending  ServerTunnelPresentationState = "Pending"
	ServerTunnelApplied  ServerTunnelPresentationState = "Applied"
	ServerTunnelError    ServerTunnelPresentationState = "Error"
)

type serverHTTPPublicTunnel struct {
	ID            string                        `json:"id"`
	Label         string                        `json:"label"`
	Protocol      tunnelruntime.TunnelProtocol  `json:"protocol"`
	CustomDomains []string                      `json:"-"`
	Location      *string                       `json:"-"`
	ServerPort    *int64                        `json:"serverPort"`
	LocalHost     string                        `json:"localHost"`
	LocalPort     int64                         `json:"localPort"`
	Enabled       bool                          `json:"enabled"`
	Options       serverHTTPPublicTunnelOptions `json:"options"`
	CreatedAt     string                        `json:"createdAt"`
	UpdatedAt     string                        `json:"updatedAt"`
	State         ServerTunnelPresentationState `json:"state"`
}

type serverHTTPPublicTunnelOptions struct {
	Transport   tunnelruntime.TunnelTransportOptions `json:"transport"`
	HealthCheck *tunnelruntime.TunnelHealthCheck     `json:"healthCheck"`
	HTTP        *serverHTTPPublicTunnelHTTPOptions   `json:"http"`
}

type serverHTTPPublicTunnelHTTPOptions struct {
	BasicAuth         *serverHTTPPublicTunnelBasicAuth `json:"basicAuth"`
	HostHeaderRewrite *string                          `json:"hostHeaderRewrite"`
	RequestHeaders    []tunnelruntime.TunnelHeader     `json:"requestHeaders"`
	ResponseHeaders   []tunnelruntime.TunnelHeader     `json:"responseHeaders"`
}

type serverHTTPPublicTunnelBasicAuth struct {
	Username           string `json:"username"`
	PasswordConfigured bool   `json:"passwordConfigured"`
}

func (tunnel serverHTTPPublicTunnel) MarshalJSON() ([]byte, error) {
	type common struct {
		ID        string                        `json:"id"`
		Label     string                        `json:"label"`
		Protocol  tunnelruntime.TunnelProtocol  `json:"protocol"`
		LocalHost string                        `json:"localHost"`
		LocalPort int64                         `json:"localPort"`
		Enabled   bool                          `json:"enabled"`
		Options   serverHTTPPublicTunnelOptions `json:"options"`
		CreatedAt string                        `json:"createdAt"`
		UpdatedAt string                        `json:"updatedAt"`
		State     ServerTunnelPresentationState `json:"state"`
	}
	base := common{
		ID: tunnel.ID, Label: tunnel.Label, Protocol: tunnel.Protocol,
		LocalHost: tunnel.LocalHost, LocalPort: tunnel.LocalPort, Enabled: tunnel.Enabled,
		Options: tunnel.Options, CreatedAt: tunnel.CreatedAt, UpdatedAt: tunnel.UpdatedAt, State: tunnel.State,
	}
	if tunnel.Protocol == tunnelruntime.TunnelProtocolHTTP {
		return json.Marshal(struct {
			common
			CustomDomains []string `json:"customDomains"`
			Location      *string  `json:"location"`
			ServerPort    *int64   `json:"serverPort"`
		}{base, tunnel.CustomDomains, tunnel.Location, tunnel.ServerPort})
	}
	return json.Marshal(struct {
		common
		ServerPort *int64 `json:"serverPort"`
	}{base, tunnel.ServerPort})
}

func (handler *ServerHTTPHandler) presentClient(ctx context.Context, workspace *ServerWorkspace, client TrustedTunnelClient) (serverHTTPClientView, error) {
	owner, err := handler.accounts.GetAccount(ctx, client.OwnerAccountID)
	if err != nil {
		return serverHTTPClientView{}, err
	}
	tunnels, err := workspace.ListTunnels(ctx, client.ID)
	if err != nil {
		return serverHTTPClientView{}, err
	}
	runtime := handler.clientRuntime(client.ID)
	counts := serverHTTPTunnelCounts{Total: len(tunnels)}
	for _, tunnel := range tunnels {
		if tunnel.Enabled {
			counts.Enabled++
		}
		switch serverTunnelPresentationStateFor(tunnel, client, runtime) {
		case ServerTunnelApplied:
			counts.Applied++
		case ServerTunnelPending:
			counts.Pending++
		case ServerTunnelError:
			counts.Error++
		}
	}
	return serverHTTPClientView{
		ID:                  client.ID,
		Remark:              client.Remark,
		Token:               client.Token,
		DesiredRevision:     client.DesiredRevision,
		LastAppliedRevision: client.LastAppliedRevision,
		RevocationPending:   client.RevocationPending,
		CreatedAt:           client.CreatedAt,
		RotatedAt:           client.RotatedAt,
		Owner:               serverHTTPClientOwner{ID: owner.ID, Username: owner.Username},
		Runtime:             runtime,
		TunnelCounts:        counts,
	}, nil
}

func (handler *ServerHTTPHandler) presentTunnels(ctx context.Context, workspace *ServerWorkspace, client TrustedTunnelClient) ([]serverHTTPPublicTunnel, error) {
	tunnels, err := workspace.ListTunnels(ctx, client.ID)
	if err != nil {
		return nil, err
	}
	runtime := handler.clientRuntime(client.ID)
	views := make([]serverHTTPPublicTunnel, 0, len(tunnels))
	for _, tunnel := range tunnels {
		views = append(views, presentServerHTTPPublicTunnel(tunnel, client, runtime))
	}
	return views, nil
}

func serverTunnelPresentationStateFor(tunnel tunnelruntime.TunnelDefinition, client TrustedTunnelClient, runtime ServerClientRuntimeState) ServerTunnelPresentationState {
	if !tunnel.Enabled {
		return ServerTunnelDisabled
	}
	if runtime.LastError != nil && runtime.LastError.Revision != nil && *runtime.LastError.Revision == client.DesiredRevision {
		return ServerTunnelError
	}
	if client.LastAppliedRevision != client.DesiredRevision || runtime.ProcessState != tunnelruntime.FRPProcessRunning {
		return ServerTunnelPending
	}
	return ServerTunnelApplied
}

func presentServerHTTPPublicTunnel(tunnel tunnelruntime.TunnelDefinition, client TrustedTunnelClient, runtime ServerClientRuntimeState) serverHTTPPublicTunnel {
	options := serverHTTPPublicTunnelOptions{
		Transport:   tunnel.Options.Transport,
		HealthCheck: tunnel.Options.HealthCheck,
	}
	if tunnel.Options.HTTP != nil {
		options.HTTP = &serverHTTPPublicTunnelHTTPOptions{
			HostHeaderRewrite: tunnel.Options.HTTP.HostHeaderRewrite,
			RequestHeaders:    tunnel.Options.HTTP.RequestHeaders,
			ResponseHeaders:   tunnel.Options.HTTP.ResponseHeaders,
		}
		if tunnel.Options.HTTP.BasicAuth != nil {
			options.HTTP.BasicAuth = &serverHTTPPublicTunnelBasicAuth{
				Username:           tunnel.Options.HTTP.BasicAuth.Username,
				PasswordConfigured: true,
			}
		}
	}
	return serverHTTPPublicTunnel{
		ID:            tunnel.ID,
		Label:         tunnel.Label,
		Protocol:      tunnel.Protocol,
		CustomDomains: tunnel.CustomDomains,
		Location:      tunnel.Location,
		ServerPort:    tunnel.ServerPort,
		LocalHost:     tunnel.LocalHost,
		LocalPort:     tunnel.LocalPort,
		Enabled:       tunnel.Enabled,
		Options:       options,
		CreatedAt:     tunnel.CreatedAt,
		UpdatedAt:     tunnel.UpdatedAt,
		State:         serverTunnelPresentationStateFor(tunnel, client, runtime),
	}
}

func (handler *ServerHTTPHandler) serveHealth(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeServerHTTPError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
		return
	}
	writeServerHTTPJSON(writer, http.StatusOK, struct {
		Status string `json:"status"`
	}{Status: "ok"})
}

func (handler *ServerHTTPHandler) serveSession(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodPost:
		handler.signIn(writer, request)
	case http.MethodGet:
		handler.readSession(writer, request)
	case http.MethodDelete:
		handler.signOut(writer, request)
	default:
		session := handler.authenticatedSession(writer, request)
		if session == nil {
			return
		}
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET, POST, or DELETE")
	}
}

func (handler *ServerHTTPHandler) serveSessionPassword(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPut {
		handler.changeOwnPassword(writer, request)
		return
	}
	session := handler.authenticatedSession(writer, request)
	if session == nil {
		return
	}
	writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use PUT")
}

func (handler *ServerHTTPHandler) readSession(writer http.ResponseWriter, request *http.Request) {
	session := handler.authenticatedSession(writer, request)
	if session == nil {
		return
	}
	writeServerAuthenticatedSession(writer, session)
}

func (handler *ServerHTTPHandler) authenticatedSession(writer http.ResponseWriter, request *http.Request) *ServerSession {
	session, err := handler.sessions.Resume(request.Context(), serverSessionToken(request))
	if err != nil {
		writeServerHTTPDomainError(writer, err)
		return nil
	}
	if session == nil {
		writeServerHTTPError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authenticated session is required")
		return nil
	}
	return session
}

func (handler *ServerHTTPHandler) signIn(writer http.ResponseWriter, request *http.Request) {
	if !sameServerOrigin(request) {
		writeServerHTTPError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPSignInInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		var requestError *serverHTTPInputError
		if errors.As(err, &requestError) {
			writeServerHTTPError(writer, requestError.Status, requestError.Code, requestError.Message)
			return
		}
		writeServerHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid")
		return
	}
	if input.Username == nil || input.Password == nil {
		writeServerHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid")
		return
	}
	session, err := handler.sessions.SignIn(request.Context(), *input.Username, *input.Password)
	if err != nil {
		writeServerHTTPDomainError(writer, err)
		return
	}
	writeServerAuthenticatedSession(writer, &session)
}

func (handler *ServerHTTPHandler) signOut(writer http.ResponseWriter, request *http.Request) {
	session := handler.authenticatedSession(writer, request)
	if session == nil {
		return
	}
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	if err := handler.sessions.SignOut(session.Token); err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     serverSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeServerHTTPNoContent(writer)
}

func (handler *ServerHTTPHandler) changeOwnPassword(writer http.ResponseWriter, request *http.Request) {
	session := handler.authenticatedSession(writer, request)
	if session == nil {
		return
	}
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input serverHTTPPasswordChangeInput
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	if input.CurrentPassword == nil || input.NewPassword == nil {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid")
		return
	}
	if _, err := handler.sessions.ChangeOwnLocalAccountPassword(request.Context(), session.Account.ID, *input.CurrentPassword, *input.NewPassword); err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     serverSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeServerHTTPNoContent(writer)
}

type serverHTTPSignInInput struct {
	Username *string `json:"username"`
	Password *string `json:"password"`
}

type serverHTTPPasswordChangeInput struct {
	CurrentPassword *string `json:"currentPassword"`
	NewPassword     *string `json:"newPassword"`
}

func writeServerAuthenticatedSession(writer http.ResponseWriter, session *ServerSession) {
	if err := writeServerSessionCookie(writer, session.Token, session.ExpiresAt); err != nil {
		writeServerHTTPError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	writeServerHTTPJSON(writer, http.StatusOK, struct {
		Version       int                   `json:"version"`
		Authenticated bool                  `json:"authenticated"`
		Account       serverHTTPAccountView `json:"account"`
	}{
		Version:       1,
		Authenticated: true,
		Account:       presentServerHTTPAccount(session.Account),
	})
}

func writeServerAuthenticatedJSON(writer http.ResponseWriter, session *ServerSession, status int, value any) {
	if err := writeServerSessionCookie(writer, session.Token, session.ExpiresAt); err != nil {
		writeServerHTTPError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	writeServerHTTPJSON(writer, status, value)
}

func writeServerAuthenticatedNoContent(writer http.ResponseWriter, session *ServerSession) {
	if err := writeServerSessionCookie(writer, session.Token, session.ExpiresAt); err != nil {
		writeServerHTTPError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	writeServerHTTPNoContent(writer)
}

func writeServerHTTPAuthenticatedError(writer http.ResponseWriter, session *ServerSession, status int, code, message string) {
	if err := writeServerSessionCookie(writer, session.Token, session.ExpiresAt); err != nil {
		writeServerHTTPError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	writeServerHTTPError(writer, status, code, message)
}

func writeServerHTTPAuthenticatedDomainError(writer http.ResponseWriter, session *ServerSession, err error) {
	if err := writeServerSessionCookie(writer, session.Token, session.ExpiresAt); err != nil {
		writeServerHTTPError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	writeServerHTTPDomainError(writer, err)
}

func writeServerHTTPAuthenticatedInputError(writer http.ResponseWriter, session *ServerSession, err error) {
	var requestError *serverHTTPInputError
	if errors.As(err, &requestError) {
		writeServerHTTPAuthenticatedError(writer, session, requestError.Status, requestError.Code, requestError.Message)
		return
	}
	writeServerHTTPAuthenticatedError(writer, session, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid")
}

type serverHTTPInputError struct {
	Status  int
	Code    string
	Message string
}

func (err *serverHTTPInputError) Error() string { return err.Message }

func decodeServerHTTPJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return &serverHTTPInputError{Status: http.StatusUnsupportedMediaType, Code: "UNSUPPORTED_MEDIA_TYPE", Message: "Request body must use JSON"}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, serverHTTPBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func sameServerOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, request.Host)
}

type serverHTTPAccountView struct {
	ID                   string      `json:"id"`
	Kind                 AccountKind `json:"kind"`
	Username             string      `json:"username"`
	Role                 AccountRole `json:"role"`
	CreatedAt            string      `json:"createdAt"`
	UpdatedAt            string      `json:"updatedAt"`
	ManagedByEnvironment bool        `json:"managedByEnvironment"`
}

type serverHTTPAccountListView struct {
	ID                   string      `json:"id"`
	Kind                 AccountKind `json:"kind"`
	Username             string      `json:"username"`
	Role                 AccountRole `json:"role"`
	CreatedAt            string      `json:"createdAt"`
	UpdatedAt            string      `json:"updatedAt"`
	ManagedByEnvironment bool        `json:"managedByEnvironment"`
	ClientCount          int64       `json:"clientCount"`
}

func presentServerHTTPAccount(account ServerAccount) serverHTTPAccountView {
	return serverHTTPAccountView{
		ID:                   account.ID,
		Kind:                 account.Kind,
		Username:             account.Username,
		Role:                 account.Role,
		CreatedAt:            account.CreatedAt,
		UpdatedAt:            account.UpdatedAt,
		ManagedByEnvironment: account.Kind == AccountKindEnvironment,
	}
}

func presentServerHTTPAccountList(account ServerAccountView) serverHTTPAccountListView {
	view := presentServerHTTPAccount(account.ServerAccount)
	return serverHTTPAccountListView{
		ID:                   view.ID,
		Kind:                 view.Kind,
		Username:             view.Username,
		Role:                 view.Role,
		CreatedAt:            view.CreatedAt,
		UpdatedAt:            view.UpdatedAt,
		ManagedByEnvironment: view.ManagedByEnvironment,
		ClientCount:          account.ClientCount,
	}
}

func serverSessionToken(request *http.Request) string {
	cookie, err := request.Cookie(serverSessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func writeServerSessionCookie(writer http.ResponseWriter, token, expiresAt string) error {
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return err
	}
	remaining := math.Max(1, math.Ceil(time.Until(expires).Seconds()))
	http.SetCookie(writer, &http.Cookie{
		Name:     serverSessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(remaining),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

func writeServerHTTPJSON(writer http.ResponseWriter, status int, value any) {
	writeServerHTTPSecurityHeaders(writer)
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeServerHTTPNoContent(writer http.ResponseWriter) {
	writeServerHTTPSecurityHeaders(writer)
	writer.WriteHeader(http.StatusNoContent)
}

func writeServerHTTPSecurityHeaders(writer http.ResponseWriter) {
	for name, value := range serverAPIHeaders {
		writer.Header().Set(name, value)
	}
}

func writeServerHTTPError(writer http.ResponseWriter, status int, code, message string) {
	writeServerHTTPJSON(writer, status, struct {
		Version int `json:"version"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		Version: 1,
		Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{Code: code, Message: message},
	})
}

func writeServerHTTPAgentError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(struct {
		Version int `json:"version"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		Version: 1,
		Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{Code: code, Message: message},
	})
}

func writeServerHTTPAgentDomainError(writer http.ResponseWriter, err error) {
	var domainError *ServerDomainError
	if !errors.As(err, &domainError) {
		writeServerHTTPAgentError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	status, found := map[string]int{
		"AUTHENTICATION_FAILED": http.StatusUnauthorized,
		"CLIENT_CONNECTED":      http.StatusConflict,
		"FRPS_UNAVAILABLE":      http.StatusServiceUnavailable,
	}[domainError.Code]
	if !found {
		writeServerHTTPAgentError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	writeServerHTTPAgentError(writer, status, domainError.Code, domainError.Message)
}

func writeServerHTTPDomainError(writer http.ResponseWriter, err error) {
	var domainError *ServerDomainError
	if !errors.As(err, &domainError) {
		writeServerHTTPError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	status, found := map[string]int{
		"ACCOUNT_NOT_EMPTY":        http.StatusConflict,
		"ACTIVATION_FAILED":        http.StatusInternalServerError,
		"AUTHENTICATION_FAILED":    http.StatusUnauthorized,
		"AUTHENTICATION_REQUIRED":  http.StatusUnauthorized,
		"CLIENT_OFFLINE":           http.StatusConflict,
		"CONFIGURATION_FAILED":     http.StatusInternalServerError,
		"FORBIDDEN":                http.StatusForbidden,
		"FRPS_UNAVAILABLE":         http.StatusServiceUnavailable,
		"INVALID_ACCOUNT":          http.StatusBadRequest,
		"INVALID_CLIENT_REMARK":    http.StatusBadRequest,
		"INVALID_CONFIG":           http.StatusBadRequest,
		"INVALID_CUSTOM_404_PAGE":  http.StatusBadRequest,
		"INVALID_CURRENT_PASSWORD": http.StatusBadRequest,
		"INVALID_HOSTNAME":         http.StatusBadRequest,
		"INVALID_HTTP_ROUTE":       http.StatusBadRequest,
		"INVALID_LOCAL_ENDPOINT":   http.StatusBadRequest,
		"INVALID_PROTOCOL":         http.StatusBadRequest,
		"INVALID_REVISION":         http.StatusBadRequest,
		"INVALID_TUNNEL":           http.StatusBadRequest,
		"MANAGED_ACCOUNT":          http.StatusConflict,
		"NOT_FOUND":                http.StatusNotFound,
		"PORT_OUTSIDE_POOL":        http.StatusBadRequest,
		"PORT_POOL_EXHAUSTED":      http.StatusConflict,
		"RESOURCE_RESERVED":        http.StatusConflict,
		"SESSION_UNAVAILABLE":      http.StatusServiceUnavailable,
		"USERNAME_TAKEN":           http.StatusConflict,
	}[domainError.Code]
	if !found {
		writeServerHTTPError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The tunnel control request failed")
		return
	}
	writeServerHTTPError(writer, status, domainError.Code, domainError.Message)
}
