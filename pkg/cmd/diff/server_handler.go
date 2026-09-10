package diff

import (
	"net/http"
	"strings"

	webassets "github.com/hackycy/hackycy-cli/web"
)

// NewServerHandler composes the Diff-owned protocol routes with the selected
// embedded Diff shell. It does not own a listener or process lifecycle.
func NewServerHandler(workspace *Workspace, bindingAddress string) (http.Handler, error) {
	handler, err := newServerHandler(workspace, bindingAddress)
	if err != nil {
		return nil, err
	}
	return handler, nil
}

type diffServerHandler struct {
	site      *webassets.Site
	protocols ProtocolHandlers
}

func newServerHandler(workspace *Workspace, bindingAddress string) (*diffServerHandler, error) {
	return newServerHandlerWithLifecycle(workspace, bindingAddress, nil)
}

func newServerHandlerWithLifecycle(workspace *Workspace, bindingAddress string, lifecycle *diffLifecycle) (*diffServerHandler, error) {
	site, err := webassets.Load("diff")
	if err != nil {
		return nil, err
	}
	return &diffServerHandler{
		site:      site,
		protocols: newProtocolHandlers(workspace, bindingAddress, lifecycle),
	}, nil
}

func (handler *diffServerHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/mcp":
		handler.protocols.MCP.ServeHTTP(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/"):
		handler.protocols.REST.ServeHTTP(writer, request)
	case handler.site.ServeAsset(writer, request):
	default:
		handler.site.ServeShell(writer, request, diffAPICSP)
	}
}
