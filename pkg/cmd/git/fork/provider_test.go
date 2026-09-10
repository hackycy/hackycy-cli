package fork

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestProviderClientUsesLegacyGitHubAndGitLabRequests(t *testing.T) {
	tests := []struct {
		name             string
		provider         string
		owner            string
		nameValue        string
		token            string
		wantPath         string
		wantAccept       string
		wantAuthHeader   string
		wantPrivateToken string
	}{
		{
			name: "GitHub Enterprise", provider: providerGitHub, owner: "group", nameValue: "project", token: "github-token",
			wantPath: "/api/v3/repos/group/project", wantAccept: githubAcceptHeader, wantAuthHeader: "Bearer github-token",
		},
		{
			name: "GitLab project encoding", provider: providerGitLab, owner: "group/sub group", nameValue: "project", token: "gitlab-token",
			wantPath: "/api/v4/projects/group%2Fsub%20group%2Fproject", wantPrivateToken: "gitlab-token",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if got := request.URL.EscapedPath(); got != test.wantPath {
					t.Errorf("path = %q, want %q", got, test.wantPath)
				}
				if got := request.Header.Get("Accept"); got != test.wantAccept {
					t.Errorf("Accept = %q, want %q", got, test.wantAccept)
				}
				if got := request.Header.Get("Authorization"); got != test.wantAuthHeader {
					t.Errorf("Authorization = %q, want %q", got, test.wantAuthHeader)
				}
				if got := request.Header.Get("PRIVATE-TOKEN"); got != test.wantPrivateToken {
					t.Errorf("PRIVATE-TOKEN = %q, want %q", got, test.wantPrivateToken)
				}
				_, _ = io.WriteString(response, `{"default_branch":"release/main"}`)
			}))
			defer server.Close()

			client, err := NewProviderClient(server.Client())
			if err != nil {
				t.Fatalf("NewProviderClient() error = %v", err)
			}
			branch, err := client.DefaultBranch(context.Background(), testRepository(server.URL, test.provider, test.owner, test.nameValue, test.token))
			if err != nil {
				t.Fatalf("DefaultBranch() error = %v", err)
			}
			if branch != "release/main" {
				t.Fatalf("DefaultBranch() = %q, want release/main", branch)
			}
		})
	}
}

func TestProviderClientPreservesArchiveRedirectsAndFailureSemantics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v3/repos/owner/project/tarball/topic":
			if request.Header.Get("Authorization") != "Bearer github-token" || request.Header.Get("Accept") != githubAcceptHeader {
				t.Errorf("archive headers = %#v", request.Header)
			}
			http.Redirect(response, request, "/archive", http.StatusFound)
		case "/archive":
			if request.Header.Get("Authorization") != "Bearer github-token" || request.Header.Get("Accept") != githubAcceptHeader {
				t.Errorf("redirected archive headers = %#v", request.Header)
			}
			_, _ = response.Write([]byte("archive-bytes"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client, err := NewProviderClient(server.Client())
	if err != nil {
		t.Fatalf("NewProviderClient() error = %v", err)
	}
	archive, err := client.DownloadArchive(context.Background(), testRepository(server.URL, providerGitHub, "owner", "project", "github-token"), "topic")
	if err != nil || !bytes.Equal(archive, []byte("archive-bytes")) {
		t.Fatalf("DownloadArchive() = %q, %v", archive, err)
	}

	for _, test := range []struct {
		name     string
		status   int
		contains string
	}{
		{name: "authentication", status: http.StatusUnauthorized, contains: "Authentication failed. Check your token"},
		{name: "ordinary status", status: http.StatusBadGateway, contains: "502 Bad Gateway"},
	} {
		t.Run(test.name, func(t *testing.T) {
			failureServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(test.status)
			}))
			defer failureServer.Close()
			failureClient, err := NewProviderClient(failureServer.Client())
			if err != nil {
				t.Fatalf("NewProviderClient() error = %v", err)
			}
			_, err = failureClient.DownloadArchive(context.Background(), testRepository(failureServer.URL, providerGitHub, "owner", "project", "credential-not-for-errors"), "main")
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("DownloadArchive() error = %v, want %q", err, test.contains)
			}
			if strings.Contains(err.Error(), "credential-not-for-errors") {
				t.Fatalf("DownloadArchive() leaked a credential: %v", err)
			}
		})
	}
}

func TestProviderClientRejectsMalformedResponsesWithoutLeakingCredentials(t *testing.T) {
	tests := []struct {
		name    string
		client  HTTPClient
		wantErr string
	}{
		{name: "transport failure", client: failingHTTPClient{err: errors.New("token=do-not-disclose")}, wantErr: "Git Fork provider request failed"},
		{name: "malformed JSON", client: staticHTTPClient{response: responseWithBody("not-json")}, wantErr: "failed to decode Git Fork provider response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewProviderClient(test.client)
			if err != nil {
				t.Fatalf("NewProviderClient() error = %v", err)
			}
			_, err = client.DefaultBranch(context.Background(), Repository{Host: "github.example", Scheme: "https", Owner: "owner", Name: "project", ProviderType: providerGitHub, Token: "credential-not-for-errors"})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("DefaultBranch() error = %v, want %q", err, test.wantErr)
			}
			if strings.Contains(err.Error(), "do-not-disclose") || strings.Contains(err.Error(), "credential-not-for-errors") {
				t.Fatalf("DefaultBranch() leaked a credential: %v", err)
			}
		})
	}

	if _, err := NewProviderClient(nil); err == nil || err.Error() != "git fork HTTP client is required" {
		t.Fatalf("NewProviderClient(nil) error = %v", err)
	}
}

func TestProviderURLsAndCloneURLsMatchTheLegacyProviderRules(t *testing.T) {
	githubPublic := Repository{Host: "github.com", Scheme: "https", Owner: "owner", Name: "project", ProviderType: providerGitHub}
	if got, want := defaultBranchURL(githubPublic), "https://api.github.com/repos/owner/project"; got != want {
		t.Fatalf("public default URL = %q, want %q", got, want)
	}
	if got, want := archiveURL(githubPublic, "release/topic"), "https://api.github.com/repos/owner/project/tarball/release/topic"; got != want {
		t.Fatalf("public archive URL = %q, want %q", got, want)
	}

	gitlab := Repository{Host: "gitlab.example", Scheme: "http", Owner: "group/sub group", Name: "project", ProviderType: providerGitLab, Token: "gitlab-secret"}
	if got, want := archiveURL(gitlab, "release/topic one"), "http://gitlab.example/api/v4/projects/group%2Fsub%20group%2Fproject/repository/archive.tar.gz?sha=release%2Ftopic%20one"; got != want {
		t.Fatalf("GitLab archive URL = %q, want %q", got, want)
	}

	tests := []struct {
		name       string
		repository Repository
		want       string
	}{
		{
			name: "anonymous remote", repository: githubPublic,
			want: "https://github.com/owner/project.git",
		},
		{
			name: "GitHub token remote", repository: Repository{Host: "github.example", Scheme: "http", Owner: "owner", Name: "project", ProviderType: providerGitHub, Token: "github-secret"},
			want: "http://github-secret@github.example/owner/project.git",
		},
		{
			name: "GitLab token remote", repository: gitlab,
			want: "http://oauth2:gitlab-secret@gitlab.example/group/sub group/project.git",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CloneURL(test.repository); got != test.want {
				t.Fatalf("CloneURL() = %q, want %q", got, test.want)
			}
		})
	}

	if got, want := providerHeaders(Repository{ProviderType: providerGitLab}), make(http.Header); !reflect.DeepEqual(got, want) {
		t.Fatalf("anonymous GitLab headers = %#v, want %#v", got, want)
	}
}

func testRepository(serverURL, provider, owner, name, token string) Repository {
	trimmed := strings.TrimPrefix(serverURL, "http://")
	return Repository{Host: trimmed, Scheme: "http", Owner: owner, Name: name, ProviderType: provider, Token: token}
}

type failingHTTPClient struct {
	err error
}

func (client failingHTTPClient) Do(*http.Request) (*http.Response, error) {
	return nil, client.err
}

type staticHTTPClient struct {
	response *http.Response
}

func (client staticHTTPClient) Do(*http.Request) (*http.Response, error) {
	return client.response, nil
}

func responseWithBody(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body))}
}
