package fs

import (
	"net/netip"
	"strconv"
	"time"
)

// NetworkInterface is one observed host network interface for FS startup
// presentation.
type NetworkInterface struct {
	Internal  bool
	Addresses []netip.Addr
}

// StartupURL is one human-visible FS server address.
type StartupURL struct {
	Label string
	URL   string
}

// Startup contains the complete presentation facts available after FS binds.
type Startup struct {
	URLs                []StartupURL
	Directory           string
	BindingAddress      string
	Port                int
	ManagementEnabled   bool
	ChunkedUploads      bool
	UploadChunkSize     int64
	SafeHTML            bool
	Authentication      bool
	AccountCount        int
	SessionDirectory    string
	SessionIdleDuration time.Duration
}

func runtimeStartup(runtime *Runtime, interfaces []NetworkInterface) Startup {
	urls := makeFSStartupURLs(runtime.bindingAddress, runtime.port, interfaces)
	startup := Startup{
		URLs:              urls,
		Directory:         runtime.workspace.rootDirectory,
		BindingAddress:    runtime.bindingAddress,
		Port:              runtime.port,
		ManagementEnabled: runtime.managementEnabled,
		ChunkedUploads:    runtime.chunkedUploads != nil,
		SafeHTML:          runtime.safeHTML,
	}
	if runtime.chunkedUploads != nil {
		startup.UploadChunkSize = runtime.chunkedUploads.chunkSize
	}
	if runtime.authentication != nil {
		startup.Authentication = true
		startup.AccountCount = len(runtime.authentication.accounts)
		startup.SessionDirectory = runtime.authentication.SessionDirectory()
		startup.SessionIdleDuration = runtime.authentication.SessionIdleLifetime()
	}
	return startup
}

func makeFSStartupURLs(address string, port int, interfaces []NetworkInterface) []StartupURL {
	portText := strconv.Itoa(port)
	if address != "0.0.0.0" {
		return []StartupURL{{Label: "Local", URL: "http://" + address + ":" + portText}}
	}
	urls := []StartupURL{{Label: "Local", URL: "http://localhost:" + portText}}
	for _, network := range interfaces {
		if network.Internal {
			continue
		}
		for _, address := range network.Addresses {
			if address.Is4() {
				urls = append(urls, StartupURL{Label: "Network", URL: "http://" + address.String() + ":" + portText})
			}
		}
	}
	return urls
}
