package server

import (
	"errors"
	"strings"
	"testing"
)

func TestServerDomainNormalizesExactHTTPHostnames(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "Example.COM.", want: "example.com"},
		{input: "\u4f8b\u5b50.\u6d4b\u8bd5", want: "xn--fsqu00a.xn--0zwm56d"},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := normalizeExactHostname(test.input)
			if err != nil || got != test.want {
				t.Fatalf("normalizeExactHostname(%q) = (%q, %v), want %q", test.input, got, err, test.want)
			}
		})
	}
	for _, input := range []string{"https://example.com", "*.example.com", "example.com/path", "127.0.0.1", "localhost", "example.com:80"} {
		t.Run("reject "+input, func(t *testing.T) {
			assertServerDomainCode(t, func() error {
				_, err := normalizeExactHostname(input)
				return err
			}(), "INVALID_HOSTNAME")
		})
	}
}

func TestServerDomainNormalizesHTTPRoutesAndDomainSets(t *testing.T) {
	location := " /service-a "
	gotLocation, err := normalizeHTTPLocation(&location)
	if err != nil || gotLocation == nil || *gotLocation != "/service-a" {
		t.Fatalf("normalizeHTTPLocation() = (%#v, %v)", gotLocation, err)
	}
	for _, input := range []string{"service-a", "/with space", "/path?query=1", "/path#fragment", ""} {
		input := input
		assertServerDomainCode(t, func() error {
			_, err := normalizeHTTPLocation(&input)
			return err
		}(), "INVALID_HTTP_ROUTE")
	}
	gotDomains, err := normalizeCustomDomains([]string{"App.Example.com", "app.example.com", "\u4f8b\u5b50.\u6d4b\u8bd5"}, nil)
	if err != nil || strings.Join(gotDomains, ",") != "app.example.com,xn--fsqu00a.xn--0zwm56d" {
		t.Fatalf("normalizeCustomDomains() = (%#v, %v)", gotDomains, err)
	}
	legacy := "Legacy.Example.com"
	gotDomains, err = normalizeCustomDomains(nil, &legacy)
	if err != nil || strings.Join(gotDomains, ",") != "legacy.example.com" {
		t.Fatalf("legacy normalizeCustomDomains() = (%#v, %v)", gotDomains, err)
	}
	assertServerDomainCode(t, func() error {
		_, err := normalizeCustomDomains([]string{}, nil)
		return err
	}(), "INVALID_HOSTNAME")
	assertServerDomainCode(t, func() error {
		_, err := normalizeCustomDomains([]string{"app.example.com"}, &legacy)
		return err
	}(), "INVALID_TUNNEL")
}

func TestServerDomainNormalizesClientAndTunnelFields(t *testing.T) {
	remark, err := normalizeClientRemark("  line one\nline two  ")
	if err != nil || remark != "line one\nline two" {
		t.Fatalf("normalizeClientRemark() = (%q, %v)", remark, err)
	}
	assertServerDomainCode(t, func() error {
		_, err := normalizeClientRemark(strings.Repeat("x", 101))
		return err
	}(), "INVALID_CLIENT_REMARK")
	label := " Ticket H5 "
	gotLabel, err := normalizeTunnelLabel(&label)
	if err != nil || gotLabel != "Ticket H5" {
		t.Fatalf("normalizeTunnelLabel() = (%q, %v)", gotLabel, err)
	}
	host := " local-service "
	gotHost, gotPort, err := normalizeLocalEndpoint(&host, 9001)
	if err != nil || gotHost != "local-service" || gotPort != 9001 {
		t.Fatalf("normalizeLocalEndpoint() = (%q, %d, %v)", gotHost, gotPort, err)
	}
	assertServerDomainCode(t, func() error {
		_, _, err := normalizeLocalEndpoint(nil, 0)
		return err
	}(), "INVALID_LOCAL_ENDPOINT")
}

func assertServerDomainCode(t *testing.T, err error, want string) {
	t.Helper()
	var domainErr *ServerDomainError
	if !errors.As(err, &domainErr) || domainErr.Code != want {
		t.Fatalf("error = %#v, want server domain code %q", err, want)
	}
}
