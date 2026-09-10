package fork

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestResolveRepositoryAcceptsTheLegacyInputForms(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		config fakeConfigReader
		want   Repository
	}{
		{
			name:  "full URL with nested owner, port, git suffix, and ref",
			input: "http://gitlab.example:8443/group/subgroup/project.git#release/2026%2F08",
			want: Repository{
				Host: "gitlab.example:8443", Scheme: "http", Owner: "group/subgroup", Name: "project", Ref: "release/2026%2F08", ProviderType: providerGitLab,
			},
		},
		{
			name:  "host path defaults to HTTPS",
			input: "github.enterprise/owner/project",
			want:  Repository{Host: "github.enterprise", Scheme: "https", Owner: "owner", Name: "project", ProviderType: providerGitHub},
		},
		{
			name:  "owner path defaults to public GitHub",
			input: "owner/project.git#main",
			want:  Repository{Host: "github.com", Scheme: "https", Owner: "owner", Name: "project", Ref: "main", ProviderType: providerGitHub},
		},
		{
			name:  "alias selects its configured provider and credentials",
			input: "work:group/project.git#v1",
			config: fakeConfigReader{byName: map[string]appconfig.ForkCredentials{
				"work": {Name: "work", Host: "gitlab.internal", Scheme: "http", Type: providerGitLab, Token: "alias-secret"},
			}},
			want: Repository{
				Host: "gitlab.internal", Scheme: "http", Owner: "group", Name: "project", Ref: "v1", InstanceName: "work", ProviderType: providerGitLab, Token: "alias-secret",
			},
		},
		{
			name:  "matching configured host overrides input scheme",
			input: "https://GITHUB.example/owner/project",
			config: fakeConfigReader{byHost: map[string]appconfig.ForkCredentials{
				"github.example": {Name: "enterprise", Host: "github.example", Scheme: "http", Type: providerGitLab, Token: "host-secret"},
			}},
			want: Repository{
				Host: "github.example", Scheme: "http", Owner: "owner", Name: "project", InstanceName: "enterprise", ProviderType: providerGitLab, Token: "host-secret",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveRepository(test.input, test.config)
			if err != nil {
				t.Fatalf("ResolveRepository() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ResolveRepository() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveRepositoryRetainsObservedPermissiveForms(t *testing.T) {
	tests := []struct {
		input string
		want  Repository
	}{
		{
			input: "/project",
			want:  Repository{Host: "github.com", Scheme: "https", Name: "project", ProviderType: providerGitHub},
		},
		{
			input: "owner/",
			want:  Repository{Host: "github.com", Scheme: "https", Owner: "owner", ProviderType: providerGitHub},
		},
		{
			input: "group//project",
			want:  Repository{Host: "github.com", Scheme: "https", Owner: "group/", Name: "project", ProviderType: providerGitHub},
		},
		{
			input: "ssh://github.com/owner/project",
			want:  Repository{Host: "github.com", Scheme: "ssh", Owner: "owner", Name: "project", ProviderType: providerGitHub},
		},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ResolveRepository(test.input, fakeConfigReader{})
			if err != nil {
				t.Fatalf("ResolveRepository() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ResolveRepository() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveRepositoryReportsInputAndConfigurationFailuresWithoutCredentials(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		config   ConfigReader
		contains string
		mustOmit string
	}{
		{
			name:     "missing repository path",
			input:    "owner",
			config:   fakeConfigReader{},
			contains: "Invalid repository path: owner. Expected format: owner/repo",
		},
		{
			name:     "unknown alias including SSH syntax",
			input:    "git@github.com:owner/project",
			config:   fakeConfigReader{},
			contains: "Unknown instance alias: \"git@github.com\"",
		},
		{
			name:     "unknown custom host",
			input:    "sourcehut.example/owner/project",
			config:   fakeConfigReader{},
			contains: "Cannot determine provider type for host \"sourcehut.example\"",
		},
		{
			name:     "configured instance failure",
			input:    "work:owner/project",
			config:   fakeConfigReader{err: errors.New("token=do-not-disclose")},
			contains: "failed to resolve configured Git Fork instance",
			mustOmit: "do-not-disclose",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveRepository(test.input, test.config)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("ResolveRepository() error = %v, want %q", err, test.contains)
			}
			if test.mustOmit != "" && strings.Contains(err.Error(), test.mustOmit) {
				t.Fatalf("ResolveRepository() leaked credential through error %q", err)
			}
		})
	}

	_, err := ResolveRepository("owner/project", nil)
	if err == nil || err.Error() != "git fork config reader is required" {
		t.Fatalf("ResolveRepository() nil config error = %v", err)
	}
}

type fakeConfigReader struct {
	byName map[string]appconfig.ForkCredentials
	byHost map[string]appconfig.ForkCredentials
	err    error
}

func (reader fakeConfigReader) ForkInstance(name string) (appconfig.ForkCredentials, bool, error) {
	if reader.err != nil {
		return appconfig.ForkCredentials{}, false, reader.err
	}
	credentials, found := reader.byName[name]
	return credentials, found, nil
}

func (reader fakeConfigReader) ForkInstanceByHost(host string) (appconfig.ForkCredentials, bool, error) {
	if reader.err != nil {
		return appconfig.ForkCredentials{}, false, reader.err
	}
	credentials, found := reader.byHost[host]
	return credentials, found, nil
}
