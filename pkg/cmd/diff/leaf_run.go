package diff

import (
	"errors"
	"net"
	"net/netip"
	"strings"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

// runDiff retains ownership of the foreground server lifecycle in the leaf.
func runDiff(options *Options) error {
	if options == nil {
		return errors.New("diff options are required")
	}
	if options.Terminal == nil {
		return errors.New("diff terminal is required")
	}
	module, err := New(Dependencies{
		NetworkInterfaces: options.NetworkInterfaces,
		Logger:            options.Logger,
		Now:               options.Now,
	})
	if err != nil {
		return err
	}
	operation, err := module.Start(options.Context, options.Input)
	if err != nil || operation == nil {
		return err
	}
	if options.Context != nil && options.Context.Err() != nil {
		return operation.Wait(options.Context)
	}
	run := options.Terminal.Open(options.Context)
	defer run.Close()
	if err := run.Result(terminalDiffStartupDocument(operation.Startup)); err != nil {
		return errors.Join(err, operation.session.startupOutputFailure())
	}
	operation.session.lifecycle.commitStartup()
	return operation.Wait(options.Context)
}

func osDiffNetworkInterfaces() ([]NetworkInterface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	return collectDiffNetworkInterfaces(interfaces, func(network net.Interface) ([]net.Addr, error) {
		return network.Addrs()
	})
}

func collectDiffNetworkInterfaces(interfaces []net.Interface, addresses func(net.Interface) ([]net.Addr, error)) ([]NetworkInterface, error) {
	result := make([]NetworkInterface, 0, len(interfaces))
	for _, network := range interfaces {
		observed, err := addresses(network)
		if err != nil {
			return nil, err
		}
		item := NetworkInterface{Internal: network.Flags&net.FlagLoopback != 0}
		for _, address := range observed {
			if ip, ok := diffNetworkIP(address); ok {
				item.Addresses = append(item.Addresses, ip)
			}
		}
		if len(item.Addresses) > 0 {
			result = append(result, item)
		}
	}
	return result, nil
}

func diffNetworkIP(address net.Addr) (netip.Addr, bool) {
	var raw net.IP
	switch value := address.(type) {
	case *net.IPNet:
		raw = value.IP
	case *net.IPAddr:
		raw = value.IP
	default:
		return netip.Addr{}, false
	}
	parsed, ok := netip.AddrFromSlice(raw)
	if !ok {
		return netip.Addr{}, false
	}
	return parsed.Unmap(), true
}

func terminalDiffStartupDocument(start Startup) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleActive,
		Text: "Directory diff: " + start.LocalURL,
	}, {
		Role: terminalexperience.VisualRoleMuted,
		Text: "MCP endpoint:   " + start.LocalURL + "/mcp",
	}}
	for _, url := range start.NetworkURLs {
		blocks = append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleActive, Text: "Network: " + url},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Network MCP: " + url + "/mcp"},
		)
	}
	blocks = append(blocks,
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Baseline: " + start.BaselineDirectory},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Target:   " + start.TargetDirectory},
	)
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalDiffStartupPlainText(start Startup) string {
	var output strings.Builder
	output.WriteString("Directory diff: ")
	output.WriteString(start.LocalURL)
	output.WriteString("\nMCP endpoint:   ")
	output.WriteString(start.LocalURL)
	output.WriteString("/mcp\n")
	for _, url := range start.NetworkURLs {
		output.WriteString("Network: ")
		output.WriteString(url)
		output.WriteString("\nNetwork MCP: ")
		output.WriteString(url)
		output.WriteString("/mcp\n")
	}
	output.WriteString("Baseline: ")
	output.WriteString(start.BaselineDirectory)
	output.WriteString("\nTarget:   ")
	output.WriteString(start.TargetDirectory)
	output.WriteByte('\n')
	return output.String()
}
