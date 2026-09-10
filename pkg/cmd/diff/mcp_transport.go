package diff

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

const (
	mcpProtocolVersionHeader        = "Mcp-Protocol-Version"
	mcpLegacyProtocolVersions       = "2025-11-25, 2025-06-18, 2025-03-26, 2024-11-05, 2024-10-07"
	mcpPostAcceptError              = "Not Acceptable: Client must accept both application/json and text/event-stream"
	mcpGetAcceptError               = "Not Acceptable: Client must accept text/event-stream"
	mcpContentTypeError             = "Unsupported Media Type: Content-Type must be application/json"
	mcpInvalidJSONError             = "Parse error: Invalid JSON"
	mcpInvalidJSONRPCMessageError   = "Parse error: Invalid JSON-RPC message"
	mcpUnsupportedHTTPMethodMessage = "Method not allowed."
)

var mcpLegacyToSDKProtocolVersion = map[string]string{
	"2025-11-25": "2025-06-18",
	"2025-06-18": "2025-06-18",
	"2025-03-26": "2025-03-26",
	"2024-11-05": "2024-11-05",
	"2024-10-07": "2024-11-05",
}

// serveMCPTransportCompatibility owns the observable Streamable HTTP layer
// that differs from the Go SDK. Valid POST requests still use the SDK for MCP
// dispatch; this adapter only preserves the legacy transport contract.
func serveMCPTransportCompatibility(writer http.ResponseWriter, request *http.Request) bool {
	switch request.Method {
	case http.MethodPost:
		return validateMCPPostTransport(writer, request)
	case http.MethodGet:
		if !mcpHeaderContains(request.Header, "Accept", "text/event-stream") {
			writeMCPProtocolError(writer, http.StatusNotAcceptable, -32000, mcpGetAcceptError)
			return true
		}
		if !normalizeMCPProtocolVersion(writer, request) {
			return true
		}
		serveMCPStatelessEventStream(writer, request)
		return true
	case http.MethodDelete:
		if !normalizeMCPProtocolVersion(writer, request) {
			return true
		}
		setHTTPAPIHeaders(writer.Header())
		writer.WriteHeader(http.StatusOK)
		return true
	default:
		writer.Header().Set("Allow", "GET, POST, DELETE")
		writeMCPProtocolError(writer, http.StatusMethodNotAllowed, -32000, mcpUnsupportedHTTPMethodMessage)
		return true
	}
}

func validateMCPPostTransport(writer http.ResponseWriter, request *http.Request) bool {
	if !mcpHeaderContains(request.Header, "Accept", "application/json") || !mcpHeaderContains(request.Header, "Accept", "text/event-stream") {
		writeMCPProtocolError(writer, http.StatusNotAcceptable, -32000, mcpPostAcceptError)
		return true
	}
	if !mcpHeaderContains(request.Header, "Content-Type", "application/json") {
		writeMCPProtocolError(writer, http.StatusUnsupportedMediaType, -32000, mcpContentTypeError)
		return true
	}

	contents, err := readMCPRequestBody(request)
	if err != nil || !json.Valid(contents) {
		writeMCPProtocolError(writer, http.StatusBadRequest, -32700, mcpInvalidJSONError)
		return true
	}
	valid, hasInitialize := isMCPJSONRPCPayload(contents)
	if !valid {
		writeMCPProtocolError(writer, http.StatusBadRequest, -32700, mcpInvalidJSONRPCMessageError)
		return true
	}
	if hasInitialize {
		normalizeKnownMCPProtocolVersion(request)
	} else if !normalizeMCPProtocolVersion(writer, request) {
		return true
	}

	// The Go SDK only recognizes a subset of the legacy SDK's historical
	// protocol values. The transport behavior is compatible for this stateless
	// JSON endpoint after mapping those values to the nearest supported version.
	request.Header.Set("Accept", "application/json, text/event-stream")
	return false
}

func readMCPRequestBody(request *http.Request) ([]byte, error) {
	if request.Body == nil {
		request.Body = io.NopCloser(bytes.NewReader(nil))
		return nil, nil
	}
	contents, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(contents))
	return contents, nil
}

func mcpHeaderContains(headers http.Header, name, value string) bool {
	return strings.Contains(strings.Join(headers.Values(name), ","), value)
}

func normalizeMCPProtocolVersion(writer http.ResponseWriter, request *http.Request) bool {
	version := request.Header.Get(mcpProtocolVersionHeader)
	if version == "" {
		return true
	}

	normalized, ok := mcpLegacyToSDKProtocolVersion[version]
	if !ok {
		message := "Bad Request: Unsupported protocol version: " + version + " (supported versions: " + mcpLegacyProtocolVersions + ")"
		writeMCPProtocolError(writer, http.StatusBadRequest, -32000, message)
		return false
	}
	request.Header.Set(mcpProtocolVersionHeader, normalized)
	return true
}

func normalizeKnownMCPProtocolVersion(request *http.Request) {
	if normalized, ok := mcpLegacyToSDKProtocolVersion[request.Header.Get(mcpProtocolVersionHeader)]; ok {
		request.Header.Set(mcpProtocolVersionHeader, normalized)
		return
	}
	request.Header.Del(mcpProtocolVersionHeader)
}

func serveMCPStatelessEventStream(writer http.ResponseWriter, request *http.Request) {
	setHTTPAPIHeaders(writer.Header())
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	<-request.Context().Done()
}

func writeMCPProtocolError(writer http.ResponseWriter, status, code int, message string) {
	setHTTPAPIHeaders(writer.Header())
	writer.Header().Set("Content-Type", "application/json")
	payload := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{JSONRPC: "2.0"}
	payload.Error.Code = code
	payload.Error.Message = message
	contents, _ := json.Marshal(payload)
	writer.WriteHeader(status)
	_, _ = writer.Write(contents)
}

func isMCPJSONRPCPayload(contents []byte) (valid, hasInitialize bool) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return false, false
	}

	messages, batch := payload.([]any)
	if !batch {
		messages = []any{payload}
	}
	for _, message := range messages {
		messageValid, initialize := isMCPJSONRPCMessage(message)
		if !messageValid {
			return false, false
		}
		hasInitialize = hasInitialize || initialize
	}
	return true, hasInitialize
}

func isMCPJSONRPCMessage(value any) (valid, initialize bool) {
	message, ok := value.(map[string]any)
	if !ok || message["jsonrpc"] != "2.0" {
		return false, false
	}

	if method, hasMethod := message["method"].(string); hasMethod {
		if !mcpHasOnlyFields(message, "jsonrpc", "id", "method", "params") {
			return false, false
		}
		if params, hasParams := message["params"]; hasParams && !mcpJSONObject(params) {
			return false, false
		}
		if id, hasID := message["id"]; hasID && !mcpJSONRPCID(id) {
			return false, false
		}
		return true, method == "initialize" && message["id"] != nil
	}

	if result, hasResult := message["result"]; hasResult {
		if !mcpJSONRPCID(message["id"]) || !mcpJSONObject(result) {
			return false, false
		}
		return true, false
	}

	errorValue, hasError := message["error"]
	if !hasError || !mcpHasOnlyFields(message, "jsonrpc", "id", "error") {
		return false, false
	}
	if id, hasID := message["id"]; hasID && !mcpJSONRPCID(id) {
		return false, false
	}
	errorObject, ok := errorValue.(map[string]any)
	if !ok || !mcpHasOnlyFields(errorObject, "code", "message", "data") || !mcpJSONRPCInteger(errorObject["code"]) {
		return false, false
	}
	_, messageOK := errorObject["message"].(string)
	return messageOK, false
}

func mcpHasOnlyFields(value map[string]any, names ...string) bool {
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
	}
	for name := range value {
		if _, ok := allowed[name]; !ok {
			return false
		}
	}
	return true
}

func mcpJSONObject(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}

func mcpJSONRPCID(value any) bool {
	if _, ok := value.(string); ok {
		return true
	}
	return mcpJSONRPCInteger(value)
}

func mcpJSONRPCInteger(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	return err == nil && !math.IsInf(parsed, 0) && math.Trunc(parsed) == parsed
}
