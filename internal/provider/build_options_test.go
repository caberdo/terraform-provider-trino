package provider

import (
	"strings"
	"testing"
	"time"
)

func TestBuildOptionsMapsConfiguration(t *testing.T) {
	clearTrinoEnv(t)
	canonical := false

	cfg := Config{
		Endpoint:                        "https://trino.example:8443",
		Auth:                            "jwt",
		User:                            "alice",
		AccessToken:                     "token",
		OAuth2ClientID:                  "client",
		OAuth2ClientSecret:              "secret",
		OAuth2TokenEndpoint:             "https://idp.example/token",
		OAuth2Scope:                     "openid",
		KerberosPrincipal:               "alice",
		KerberosRealm:                   "EXAMPLE.COM",
		KerberosCredentialCache:         "FILE:/tmp/krb5cc_1000",
		KerberosConfigFile:              "/etc/krb5.conf",
		KerberosServiceName:             "HTTP",
		KerberosServicePrincipalPattern: "${SERVICE}@${HOST}",
		KerberosUseCanonicalHostname:    &canonical,
		Source:                          "terraform-provider-trino",
		Catalog:                         "hive",
		Schema:                          "sales",
		SessionProperties:               map[string]string{"query_max_run_time": "1h"},
		ExtraCredentials:                map[string]string{"user": "alice"},
		Roles:                           map[string]string{"hive": "admin"},
		HTTPHeaders:                     map[string]string{"X-Gateway-Token": "secret"},
		QueryTimeout:                    "90s",
		InsecureSkipVerify:              true,
	}

	opts, client, diags := cfg.BuildOptions()
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if opts.Auth != "jwt" || opts.ServerURI != cfg.Endpoint || opts.User != "alice" {
		t.Fatalf("auth/server/user not mapped: %+v", opts)
	}
	if opts.AccessToken != "token" || opts.OAuth2TokenEndpoint != cfg.OAuth2TokenEndpoint {
		t.Fatalf("credentials not mapped: %+v", opts)
	}
	if opts.KerberosRealm != "EXAMPLE.COM" || opts.KerberosServiceName != "HTTP" {
		t.Fatalf("kerberos settings not mapped: %+v", opts)
	}
	if opts.KerberosUseCanonicalHostname {
		t.Fatal("kerberos_use_canonical_hostname=false was not mapped")
	}
	if opts.QueryTimeout != 90*time.Second {
		t.Fatalf("QueryTimeout = %s, want 90s", opts.QueryTimeout)
	}
	if opts.Roles["hive"] != "admin" || opts.SessionProperties["query_max_run_time"] != "1h" {
		t.Fatalf("maps not mapped: %+v", opts)
	}
	if client == nil || opts.HTTPClient != client {
		t.Fatal("HTTP client not attached to the options")
	}
}

func TestBuildOptionsDefaultsCanonicalHostnameToTrue(t *testing.T) {
	clearTrinoEnv(t)

	opts, _, diags := (Config{Endpoint: "https://trino.example:8443", Auth: "none"}).BuildOptions()
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !opts.KerberosUseCanonicalHostname {
		t.Fatal("canonical hostname should default to true")
	}
	if opts.QueryTimeout != 5*time.Minute {
		t.Fatalf("QueryTimeout = %s, want the 5m default", opts.QueryTimeout)
	}
}

func TestBuildOptionsReportsInvalidQueryTimeout(t *testing.T) {
	clearTrinoEnv(t)

	_, _, diags := (Config{Endpoint: "https://trino.example:8443", QueryTimeout: "nope"}).BuildOptions()
	if !hasAttr(diags, "query_timeout") {
		t.Fatalf("expected a query_timeout diagnostic, got %v", diags)
	}
}

func TestBuildOptionsReportsInvalidTLS(t *testing.T) {
	clearTrinoEnv(t)

	cases := []struct {
		name string
		cfg  Config
		attr string
	}{
		{
			name: "ca certificate content",
			cfg:  Config{Endpoint: "https://trino.example:8443", CACert: "not a certificate"},
			attr: "ca_cert",
		},
		{
			name: "client certificate without key",
			cfg:  Config{Endpoint: "https://trino.example:8443", ClientCert: "cert"},
			attr: "client_cert",
		},
		{
			name: "client key without certificate",
			cfg:  Config{Endpoint: "https://trino.example:8443", ClientKey: "key"},
			attr: "client_key",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, diags := tc.cfg.BuildOptions()
			if !hasAttr(diags, tc.attr) {
				t.Fatalf("expected a %s diagnostic, got %v", tc.attr, diags)
			}
		})
	}
}

func TestTLSErrorAttribute(t *testing.T) {
	cases := map[string]string{
		"ca_cert_file: open /nope: no such file": "ca_cert_file",
		"ca_cert: no certificates found":         "ca_cert",
		"client certificate: bad pem":            "client_cert",
		"client_key_file: permission denied":     "client_key",
		"something else entirely":                "client_cert",
	}
	for message, want := range cases {
		t.Run(want+"_"+strings.ReplaceAll(message, " ", "_"), func(t *testing.T) {
			if got := tlsErrorAttribute(errString(message)); got != want {
				t.Fatalf("tlsErrorAttribute(%q) = %q, want %q", message, got, want)
			}
		})
	}
}

func TestValidateEndpointRejections(t *testing.T) {
	clearTrinoEnv(t)

	cases := []struct {
		name     string
		endpoint string
		attr     string
	}{
		{name: "empty", endpoint: "", attr: "endpoint"},
		{name: "no scheme", endpoint: "trino.example:8443", attr: "endpoint"},
		{name: "unsupported scheme", endpoint: "ftp://trino.example:8443", attr: "endpoint"},
		{name: "path", endpoint: "https://trino.example:8443/v1", attr: "endpoint"},
		{name: "query", endpoint: "https://trino.example:8443?x=1", attr: "endpoint"},
		{name: "userinfo", endpoint: "https://alice@trino.example:8443", attr: "endpoint"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := (&Config{Endpoint: tc.endpoint}).Validate()
			if !hasAttr(diags, tc.attr) {
				t.Fatalf("expected an %s diagnostic for %q, got %v", tc.attr, tc.endpoint, diags)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
