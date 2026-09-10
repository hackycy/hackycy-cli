package webassets

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist is generated and structurally verified before any ycy compilation.
//
//go:embed dist
var embedded embed.FS

var applications = map[string]string{
	"diff":          "diff/index.html",
	"fs":            "fs/index.html",
	"tunnel-server": "tunnel-server/index.html",
}

var assetContentTypes = map[string]string{
	".avif":  "image/avif",
	".css":   "text/css; charset=utf-8",
	".gif":   "image/gif",
	".ico":   "image/vnd.microsoft.icon",
	".jpeg":  "image/jpeg",
	".jpg":   "image/jpeg",
	".js":    "text/javascript; charset=utf-8",
	".json":  "application/json",
	".mjs":   "text/javascript; charset=utf-8",
	".otf":   "font/otf",
	".png":   "image/png",
	".svg":   "image/svg+xml",
	".ttf":   "font/ttf",
	".wasm":  "application/wasm",
	".webp":  "image/webp",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

// Site is one selected Vite shell and the shared generated asset tree.
type Site struct {
	files fs.FS
	shell string
}

// Validate proves every product shell is present in the embedded Vite output.
func Validate() error {
	for application := range applications {
		if _, err := Load(application); err != nil {
			return err
		}
	}
	return nil
}

// Load opens one fixed application shell from the unconditional embedded output.
func Load(application string) (*Site, error) {
	shell, ok := applications[application]
	if !ok {
		return nil, fmt.Errorf("unknown embedded web application %q", application)
	}
	files, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, fmt.Errorf("open embedded web output: %w", err)
	}
	if _, err := fs.Stat(files, shell); err != nil {
		return nil, fmt.Errorf("embedded web shell %q is unavailable: %w", shell, err)
	}
	return &Site{files: files, shell: shell}, nil
}

// ServeAsset serves an exact generated asset for GET or HEAD and reports whether it existed.
func (site *Site) ServeAsset(writer http.ResponseWriter, request *http.Request) bool {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return false
	}
	asset := strings.TrimPrefix(request.URL.Path, "/")
	if !strings.HasPrefix(asset, "assets/") || !fs.ValidPath(asset) {
		return false
	}
	contents, err := fs.ReadFile(site.files, asset)
	if err != nil {
		return false
	}
	writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Type", assetContentType(asset))
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = writer.Write(contents)
	}
	return true
}

func assetContentType(asset string) string {
	if contentType, ok := assetContentTypes[strings.ToLower(path.Ext(asset))]; ok {
		return contentType
	}
	return "application/octet-stream"
}

// ServeShell writes the selected shell with the common no-store headers.
func (site *Site) ServeShell(writer http.ResponseWriter, request *http.Request, contentSecurityPolicy string) {
	contents, err := fs.ReadFile(site.files, site.shell)
	if err != nil {
		http.Error(writer, "embedded web shell is unavailable", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	if contentSecurityPolicy != "" {
		writer.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	}
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = writer.Write(contents)
	}
}
