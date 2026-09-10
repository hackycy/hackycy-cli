// Package fork owns Git Fork's repository acquisition behavior.
package fork

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

const (
	providerGitHub = "github"
	providerGitLab = "gitlab"
)

// ConfigReader supplies the decrypted Fork credentials that appconfig owns.
type ConfigReader interface {
	ForkInstance(string) (appconfig.ForkCredentials, bool, error)
	ForkInstanceByHost(string) (appconfig.ForkCredentials, bool, error)
}

// Repository identifies one repository and the configured provider used to acquire it.
type Repository struct {
	Host         string
	Scheme       string
	Owner        string
	Name         string
	Ref          string
	InstanceName string
	ProviderType string
	Token        string
}

// ResolveRepository reproduces Git Fork's input grammar and configured-instance precedence.
func ResolveRepository(input string, config ConfigReader) (Repository, error) {
	if config == nil {
		return Repository{}, errors.New("git fork config reader is required")
	}

	ref, rest := splitRef(input)
	if strings.Contains(rest, "://") {
		return resolveURLRepository(input, rest, ref, config)
	}
	if strings.Contains(rest, ":") {
		return resolveAliasRepository(input, rest, ref, config)
	}

	host := "github.com"
	scheme := "https"
	path := strings.TrimSuffix(rest, ".git")
	if strings.Contains(rest, "/") {
		firstSlash := strings.IndexByte(rest, '/')
		if strings.Contains(rest[:firstSlash], ".") {
			host = rest[:firstSlash]
			path = strings.TrimSuffix(rest[firstSlash+1:], ".git")
		}
	}

	owner, name, err := splitOwnerName(path, input)
	if err != nil {
		return Repository{}, err
	}
	return resolveHostRepository(host, scheme, owner, name, ref, config)
}

func splitRef(input string) (string, string) {
	index := strings.IndexByte(input, '#')
	if index < 0 {
		return "", input
	}
	return input[index+1:], input[:index]
}

func resolveURLRepository(input, rest, ref string, config ConfigReader) (Repository, error) {
	parsed, err := url.Parse(rest)
	if err != nil {
		return Repository{}, err
	}
	owner, name, err := splitOwnerName(strings.TrimSuffix(strings.TrimPrefix(parsed.Path, "/"), ".git"), input)
	if err != nil {
		return Repository{}, err
	}
	return resolveHostRepository(strings.ToLower(parsed.Host), parsed.Scheme, owner, name, ref, config)
}

func resolveAliasRepository(input, rest, ref string, config ConfigReader) (Repository, error) {
	index := strings.IndexByte(rest, ':')
	alias := rest[:index]
	credentials, found, err := config.ForkInstance(alias)
	if err != nil {
		return Repository{}, errors.New("failed to resolve configured Git Fork instance")
	}
	if !found {
		return Repository{}, fmt.Errorf("Unknown instance alias: %q. Run \"ycy config fork add\" to configure it.", alias)
	}
	owner, name, err := splitOwnerName(strings.TrimSuffix(rest[index+1:], ".git"), input)
	if err != nil {
		return Repository{}, err
	}
	return Repository{
		Host:         credentials.Host,
		Scheme:       credentials.Scheme,
		Owner:        owner,
		Name:         name,
		Ref:          ref,
		InstanceName: credentials.Name,
		ProviderType: credentials.Type,
		Token:        credentials.Token,
	}, nil
}

func resolveHostRepository(host, scheme, owner, name, ref string, config ConfigReader) (Repository, error) {
	credentials, found, err := config.ForkInstanceByHost(host)
	if err != nil {
		return Repository{}, errors.New("failed to resolve configured Git Fork instance")
	}
	if found {
		return Repository{
			Host:         host,
			Scheme:       credentials.Scheme,
			Owner:        owner,
			Name:         name,
			Ref:          ref,
			InstanceName: credentials.Name,
			ProviderType: credentials.Type,
			Token:        credentials.Token,
		}, nil
	}

	providerType, err := detectProviderType(host)
	if err != nil {
		return Repository{}, err
	}
	return Repository{
		Host:         host,
		Scheme:       scheme,
		Owner:        owner,
		Name:         name,
		Ref:          ref,
		ProviderType: providerType,
	}, nil
}

func splitOwnerName(path, input string) (string, string, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("Invalid repository path: %s. Expected format: owner/repo", input)
	}
	return strings.Join(parts[:len(parts)-1], "/"), parts[len(parts)-1], nil
}

func detectProviderType(host string) (string, error) {
	if host == "github.com" || strings.Contains(host, providerGitHub) {
		return providerGitHub, nil
	}
	if host == "gitlab.com" || strings.Contains(host, providerGitLab) {
		return providerGitLab, nil
	}
	return "", fmt.Errorf("Cannot determine provider type for host %q. Run \"ycy config fork add\" to configure it.", host)
}
