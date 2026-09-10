package fork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const githubAcceptHeader = "application/vnd.github.v3+json"

// HTTPClient is the command-owned HTTP boundary for configured Git providers.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// ProviderClient accesses the GitHub and GitLab APIs required by Git Fork.
type ProviderClient struct {
	http HTTPClient
}

// NewProviderClient constructs a provider adapter without adding a shared provider framework.
func NewProviderClient(client HTTPClient) (*ProviderClient, error) {
	if client == nil {
		return nil, errors.New("git fork HTTP client is required")
	}
	return &ProviderClient{http: client}, nil
}

// DefaultBranch reads the provider's default branch for a repository.
func (client *ProviderClient) DefaultBranch(ctx context.Context, repository Repository) (string, error) {
	request, err := providerRequest(ctx, http.MethodGet, defaultBranchURL(repository), providerHeaders(repository))
	if err != nil {
		return "", err
	}
	response, err := client.http.Do(request)
	if err != nil {
		return "", errors.New("Git Fork provider request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", defaultBranchFailure(repository, response.Status)
	}
	var payload struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", errors.New("failed to decode Git Fork provider response")
	}
	return payload.DefaultBranch, nil
}

// DownloadArchive retrieves an archive while preserving the legacy redirect and memory behavior.
func (client *ProviderClient) DownloadArchive(ctx context.Context, repository Repository, ref string) ([]byte, error) {
	request, err := providerRequest(ctx, http.MethodGet, archiveURL(repository, ref), providerHeaders(repository))
	if err != nil {
		return nil, err
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, errors.New("Git Fork archive request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return nil, errors.New("Authentication failed. Check your token with \"ycy config fork add\".")
		}
		return nil, errors.New(response.Status)
	}
	archive, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, errors.New("failed to read Git Fork archive response")
	}
	return archive, nil
}

// CloneURL returns the legacy provider-specific clone remote, including configured credentials.
func CloneURL(repository Repository) string {
	base := providerBaseURL(repository)
	if repository.Token == "" {
		return fmt.Sprintf("%s/%s/%s.git", base, repository.Owner, repository.Name)
	}
	withoutScheme := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	scheme := "http"
	if strings.HasPrefix(base, "https") {
		scheme = "https"
	}
	if isGitHub(repository) {
		return fmt.Sprintf("%s://%s@%s/%s/%s.git", scheme, repository.Token, withoutScheme, repository.Owner, repository.Name)
	}
	return fmt.Sprintf("%s://oauth2:%s@%s/%s/%s.git", scheme, repository.Token, withoutScheme, repository.Owner, repository.Name)
}

func providerRequest(ctx context.Context, method, target string, headers http.Header) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header = headers
	return request, nil
}

func defaultBranchURL(repository Repository) string {
	base := providerBaseURL(repository)
	if isGitHub(repository) {
		if base == "https://github.com" {
			base = "https://api.github.com"
		} else {
			base += "/api/v3"
		}
		return fmt.Sprintf("%s/repos/%s/%s", base, repository.Owner, repository.Name)
	}
	return fmt.Sprintf("%s/api/v4/projects/%s", base, encodeProviderComponent(repository.Owner+"/"+repository.Name))
}

func archiveURL(repository Repository, ref string) string {
	base := providerBaseURL(repository)
	if isGitHub(repository) {
		if base == "https://github.com" {
			base = "https://api.github.com"
		} else {
			base += "/api/v3"
		}
		return fmt.Sprintf("%s/repos/%s/%s/tarball/%s", base, repository.Owner, repository.Name, ref)
	}
	return fmt.Sprintf("%s/api/v4/projects/%s/repository/archive.tar.gz?sha=%s", base, encodeProviderComponent(repository.Owner+"/"+repository.Name), encodeProviderComponent(ref))
}

func providerHeaders(repository Repository) http.Header {
	headers := make(http.Header)
	if isGitHub(repository) {
		headers.Set("Accept", githubAcceptHeader)
		if repository.Token != "" {
			headers.Set("Authorization", "Bearer "+repository.Token)
		}
		return headers
	}
	if repository.Token != "" {
		headers.Set("PRIVATE-TOKEN", repository.Token)
	}
	return headers
}

func defaultBranchFailure(repository Repository, status string) error {
	if isGitHub(repository) {
		return fmt.Errorf("Failed to get repo info: %s", status)
	}
	return fmt.Errorf("Failed to get project info: %s", status)
}

func providerBaseURL(repository Repository) string {
	return repository.Scheme + "://" + repository.Host
}

func encodeProviderComponent(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func isGitHub(repository Repository) bool {
	return repository.ProviderType == providerGitHub
}
