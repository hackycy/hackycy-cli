package server

import "net/http"

func (handler *ServerHTTPHandler) serveNodeClaimPreviews(writer http.ResponseWriter, request *http.Request) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil {
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
	var input struct {
		ManagementAddress string `json:"managementAddress"`
	}
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	preview, err := workspace.PreviewNode(request.Context(), input.ManagementAddress)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int `json:"version"`
		serverNodePreview
	}{Version: 1, serverNodePreview: preview})
}

func (handler *ServerHTTPHandler) serveNodes(writer http.ResponseWriter, request *http.Request) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil {
		return
	}
	if request.Method == http.MethodGet {
		nodes, err := workspace.ListNodes(request.Context())
		if err != nil {
			writeServerHTTPAuthenticatedDomainError(writer, session, err)
			return
		}
		writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
			Version int                 `json:"version"`
			Nodes   []serverNodeSummary `json:"nodes"`
		}{Version: 1, Nodes: nodes})
		return
	}
	if request.Method != http.MethodPost {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET or POST")
		return
	}
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var input struct {
		PreviewID            string `json:"previewId"`
		Name                 string `json:"name"`
		ConfirmedFingerprint string `json:"confirmedFingerprint"`
		Mode                 string `json:"mode"`
	}
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	record, err := workspace.RegisterNode(request.Context(), input.PreviewID, input.Name, input.ConfirmedFingerprint, input.Mode)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusCreated, struct {
		Version int `json:"version"`
		Node    struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Kind      string `json:"kind"`
			Lifecycle string `json:"lifecycle"`
		} `json:"node"`
	}{Version: 1, Node: struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Lifecycle string `json:"lifecycle"`
	}{ID: record.ID, Name: record.Name, Kind: record.Kind, Lifecycle: record.Lifecycle}})
}

func (handler *ServerHTTPHandler) serveNode(writer http.ResponseWriter, request *http.Request, nodeID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil {
		return
	}
	if request.Method == http.MethodGet {
		node, err := workspace.GetNode(request.Context(), nodeID)
		if err != nil {
			writeServerHTTPAuthenticatedDomainError(writer, session, err)
			return
		}
		writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
			Version int               `json:"version"`
			Node    serverNodeSummary `json:"node"`
		}{Version: 1, Node: node})
		return
	}
	if request.Method != http.MethodPatch {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET or PATCH")
		return
	}
	if !sameServerOrigin(request) {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must be same-origin")
		return
	}
	var patch serverNodeMetadataPatch
	if err := decodeServerHTTPJSON(writer, request, &patch); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	record, err := workspace.PatchNode(request.Context(), nodeID, patch)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int `json:"version"`
		Node    struct {
			ID                       string `json:"id"`
			Name                     string `json:"name"`
			PendingManagementAddress string `json:"pendingManagementAddress,omitempty"`
		} `json:"node"`
	}{Version: 1, Node: struct {
		ID                       string `json:"id"`
		Name                     string `json:"name"`
		PendingManagementAddress string `json:"pendingManagementAddress,omitempty"`
	}{ID: record.ID, Name: record.Name, PendingManagementAddress: record.PendingManagementAddress}})
}

func (handler *ServerHTTPHandler) serveNodeDesired(writer http.ResponseWriter, request *http.Request, nodeID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil {
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
	var input struct {
		ExpectedRevision *int64             `json:"expectedRevision"`
		Settings         serverNodeSettings `json:"settings"`
	}
	if err := decodeServerHTTPJSON(writer, request, &input); err != nil {
		writeServerHTTPAuthenticatedInputError(writer, session, err)
		return
	}
	if input.ExpectedRevision == nil {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusBadRequest, "INVALID_REQUEST", "Expected Node revision is required")
		return
	}
	revision, err := workspace.SaveNodeDesired(request.Context(), nodeID, *input.ExpectedRevision, input.Settings)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerNodePendingResponse(writer, session, revision)
}

func (handler *ServerHTTPHandler) serveNodeReapply(writer http.ResponseWriter, request *http.Request, nodeID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil {
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
	revision, err := workspace.ReapplyNode(request.Context(), nodeID)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerNodePendingResponse(writer, session, revision)
}

func writeServerNodePendingResponse(writer http.ResponseWriter, session *ServerSession, revision int64) {
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version         int    `json:"version"`
		DesiredRevision int64  `json:"desiredRevision"`
		Configuration   string `json:"configuration"`
	}{Version: 1, DesiredRevision: revision, Configuration: "pending"})
}

func (handler *ServerHTTPHandler) serveNodeManagement(writer http.ResponseWriter, request *http.Request, nodeID string) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil {
		return
	}
	if request.Method != http.MethodGet {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
		return
	}
	view, err := workspace.GetNodeManagement(request.Context(), nodeID)
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	writeServerAuthenticatedJSON(writer, session, http.StatusOK, struct {
		Version int                      `json:"version"`
		Node    serverNodeManagementView `json:"node"`
	}{Version: 1, Node: view})
}
