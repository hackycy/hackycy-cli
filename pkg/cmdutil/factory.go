package cmdutil

import (
	"net/http"
	"time"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
)

// Factory contains only process-level capabilities shared by command packages.
type Factory struct {
	Version           string
	IOStreams         IOStreams
	Terminal          *terminal.Runtime
	Logging           *logging.Runtime
	Environment       func(string) string
	EnvironmentLookup func(string) (string, bool)
	WorkingDirectory  func() (string, error)
	HTTPClient        *http.Client
	Now               func() time.Time
	ConfigStore       func() (*appconfig.Store, error)
	GitRunner         func() *gitprocess.Runner
}
