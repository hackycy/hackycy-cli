package diff

import (
	"net/netip"
	"strconv"
)

// NetworkInterface is one observed host network interface for startup URLs.
type NetworkInterface struct {
	Internal  bool
	Addresses []netip.Addr
}

type diffStartupURLs struct {
	local   string
	network []string
}

// Startup records the human-visible Diff server facts after binding.
type Startup struct {
	LocalURL          string
	NetworkURLs       []string
	BaselineDirectory string
	TargetDirectory   string
	Port              int
	Public            bool
}

func (session *comparisonSession) startupPresentation(interfaces []NetworkInterface) Startup {
	urls := makeDiffStartupURLs(session.bindingAddress == "0.0.0.0", session.server.Port(), interfaces)
	return Startup{
		LocalURL:          urls.local,
		NetworkURLs:       urls.network,
		BaselineDirectory: session.workspace.baseline.path,
		TargetDirectory:   session.workspace.target.path,
		Port:              session.server.Port(),
		Public:            session.bindingAddress == "0.0.0.0",
	}
}

func makeDiffStartupURLs(public bool, port int, interfaces []NetworkInterface) diffStartupURLs {
	localHost := "127.0.0.1"
	if public {
		localHost = "localhost"
	}
	result := diffStartupURLs{local: "http://" + localHost + ":" + strconv.Itoa(port)}
	if !public {
		return result
	}
	for _, network := range interfaces {
		if network.Internal {
			continue
		}
		for _, address := range network.Addresses {
			if address.Is4() {
				result.network = append(result.network, "http://"+address.String()+":"+strconv.Itoa(port))
			}
		}
	}
	return result
}
