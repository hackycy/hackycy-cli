package fs

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

// runFS retains ownership of the foreground server lifecycle in the leaf.
func runFS(options *Options) error {
	if options == nil {
		return errors.New("fs options are required")
	}
	if options.Terminal == nil {
		return errors.New("fs terminal is required")
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
	if err := run.ResultCheckpoint("fs-startup", terminalFSStartupDocument(operation.Startup)); err != nil {
		operation.setShutdownReason("startup-output-failed")
		operation.recordShutdownCause(err)
		if closeErr := operation.Close(); closeErr != nil {
			return reportedFSError{error: errors.Join(err, closeErr)}
		}
		return err
	}
	if operation.lifecycle != nil {
		operation.lifecycle.commitStartup()
	}
	if err := operation.Wait(options.Context); err != nil {
		return reportedFSError{error: err}
	}
	if err := run.ResultCheckpoint("fs-stopped", terminalFSStoppedDocument()); err != nil {
		return err
	}
	return nil
}

func osFSNetworkInterfaces() ([]NetworkInterface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := make([]NetworkInterface, 0, len(interfaces))
	for _, network := range interfaces {
		addresses, err := network.Addrs()
		if err != nil {
			return nil, err
		}
		item := NetworkInterface{Internal: network.Flags&net.FlagLoopback != 0}
		for _, address := range addresses {
			if parsed, ok := fsNetworkIP(address); ok {
				item.Addresses = append(item.Addresses, parsed)
			}
		}
		if len(item.Addresses) > 0 {
			result = append(result, item)
		}
	}
	return result, nil
}

func fsNetworkIP(address net.Addr) (netip.Addr, bool) {
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

func terminalFSStartupDocument(start Startup) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleActive,
		Text: "File Browser",
	}}
	for _, url := range start.URLs {
		blocks = append(blocks, terminalexperience.PresentationBlock{
			Role: terminalexperience.VisualRoleActive,
			Text: url.Label + ": " + url.URL,
		})
	}
	blocks = append(blocks,
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Directory: " + start.Directory},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("Bind: %s:%d", start.BindingAddress, start.Port)},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("Management: %t", start.ManagementEnabled)},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("Chunked uploads: %t", start.ChunkedUploads)},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("HTML execution: %t", !start.SafeHTML)},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("Authentication: %t", start.Authentication)},
	)
	if start.Authentication {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Session storage: " + start.SessionDirectory})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalFSStoppedDocument() terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleSuccess,
		Text: "File Browser stopped.",
	}}}
}

func terminalFSStartupPlainText(start Startup) string {
	var output strings.Builder
	output.WriteString("File Browser\n")
	for _, url := range start.URLs {
		_, _ = fmt.Fprintf(&output, "%s: %s\n", url.Label, url.URL)
	}
	_, _ = fmt.Fprintf(&output, "Directory: %s\nBind: %s:%d\nManagement: %t\nChunked uploads: %t\nHTML execution: %t\nAuthentication: %t\n", start.Directory, start.BindingAddress, start.Port, start.ManagementEnabled, start.ChunkedUploads, !start.SafeHTML, start.Authentication)
	if start.Authentication {
		_, _ = fmt.Fprintf(&output, "Session storage: %s\n", start.SessionDirectory)
	}
	return output.String()
}
