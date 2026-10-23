package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
)

var trinoEnv = []string{
	"TRINO_ENDPOINT",
	"TRINO_AUTH",
	"TRINO_USER",
	"TRINO_PASSWORD",
	"TRINO_ACCESS_TOKEN",
	"TRINO_OAUTH2_CLIENT_ID",
	"TRINO_OAUTH2_CLIENT_SECRET",
	"TRINO_OAUTH2_TOKEN_ENDPOINT",
	"TRINO_OAUTH2_SCOPE",
	"TRINO_KERBEROS_PRINCIPAL",
	"TRINO_KERBEROS_REALM",
	"TRINO_KERBEROS_KEYTAB",
	"TRINO_KERBEROS_CREDENTIAL_CACHE",
	"TRINO_KERBEROS_CONFIG_FILE",
	"TRINO_KERBEROS_SERVICE_NAME",
	"TRINO_KERBEROS_SERVICE_PRINCIPAL_PATTERN",
	"TRINO_KERBEROS_USE_CANONICAL_HOSTNAME",
	"TRINO_SOURCE",
	"TRINO_CATALOG",
	"TRINO_SCHEMA",
	"TRINO_QUERY_TIMEOUT",
	"TRINO_INSECURE_SKIP_VERIFY",
	"TRINO_CA_CERT",
	"TRINO_CA_CERT_FILE",
	"TRINO_CLIENT_CERT",
	"TRINO_CLIENT_CERT_FILE",
	"TRINO_CLIENT_KEY",
	"TRINO_CLIENT_KEY_FILE",
}

func clearTrinoEnv(t *testing.T) {
	t.Helper()
	for _, name := range trinoEnv {
		t.Setenv(name, "")
	}
}

func TestResolveEnvFallbacks(t *testing.T) {
	clearTrinoEnv(t)
	t.Setenv("TRINO_ENDPOINT", "https://trino.example:8443")
	t.Setenv("TRINO_AUTH", "basic")
	t.Setenv("TRINO_USER", "alice")
	t.Setenv("TRINO_PASSWORD", "secret")
	t.Setenv("TRINO_SOURCE", "ci")
	t.Setenv("TRINO_CATALOG", "hive")
	t.Setenv("TRINO_SCHEMA", "sales")
	t.Setenv("TRINO_QUERY_TIMEOUT", "30s")
	t.Setenv("TRINO_INSECURE_SKIP_VERIFY", "true")
	t.Setenv("TRINO_KERBEROS_SERVICE_NAME", "HTTP")

	cfg := Config{}.Resolve()

	if cfg.Endpoint != "https://trino.example:8443" {
		t.Fatalf("Endpoint = %q", cfg.Endpoint)
	}
	if cfg.Auth != "basic" {
		t.Fatalf("Auth = %q, want basic", cfg.Auth)
	}
	if cfg.User != "alice" || cfg.Password != "secret" {
		t.Fatalf("User/Password = %q/%q", cfg.User, cfg.Password)
	}
	if cfg.Source != "ci" || cfg.Catalog != "hive" || cfg.Schema != "sales" {
		t.Fatalf("Source/Catalog/Schema = %q/%q/%q", cfg.Source, cfg.Catalog, cfg.Schema)
	}
	if cfg.QueryTimeout != "30s" {
		t.Fatalf("QueryTimeout = %q", cfg.QueryTimeout)
	}
	if !cfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify = false, want true")
	}
	if cfg.KerberosServiceName != "HTTP" {
		t.Fatalf("KerberosServiceName = %q", cfg.KerberosServiceName)
	}
}

func TestResolveDefaults(t *testing.T) {
	clearTrinoEnv(t)

	cfg := Config{Endpoint: "https://trino.example:8443"}.Resolve()

	if cfg.User != "terraform" {
		t.Fatalf("User = %q, want terraform", cfg.User)
	}
	if cfg.Source != "terraform-provider-trino" {
		t.Fatalf("Source = %q", cfg.Source)
	}
	if cfg.QueryTimeout != "5m" {
		t.Fatalf("QueryTimeout = %q, want 5m", cfg.QueryTimeout)
	}
	if cfg.KerberosServiceName != "HTTP" {
		t.Fatalf("KerberosServiceName = %q, want HTTP", cfg.KerberosServiceName)
	}
	if cfg.KerberosServicePrincipalPattern != "${SERVICE}@${HOST}" {
		t.Fatalf("KerberosServicePrincipalPattern = %q", cfg.KerberosServicePrincipalPattern)
	}
	if cfg.KerberosUseCanonicalHostname == nil || !*cfg.KerberosUseCanonicalHostname {
		t.Fatal("KerberosUseCanonicalHostname should default to true")
	}
}

func TestValidatePasswordRequiresTLS(t *testing.T) {
	clearTrinoEnv(t)
	cfg := Config{Endpoint: "http://localhost:8080", User: "u", Password: "p"}
	diags := cfg.Validate()
	if !diags.HasError() {
		t.Fatal("expected error")
	}
	if !hasAttr(diags, "password") {
		t.Fatalf("expected password attribute error, got %v", diags)
	}
	if got := diags[0].Detail(); !strings.Contains(got, "https") {
		t.Fatalf("detail %q", got)
	}
}

func TestValidateAuthModesExclusive(t *testing.T) {
	clearTrinoEnv(t)
	cfg := Config{Endpoint: "https://x:8443", User: "u", Auth: "basic", Password: "p", AccessToken: "t"}
	diags := cfg.Validate()
	if !hasAttr(diags, "access_token") {
		t.Fatalf("expected access_token attribute error, got %v", diags)
	}
}

func TestValidateAuthNoneRejectsCredentials(t *testing.T) {
	clearTrinoEnv(t)
	cfg := Config{Endpoint: "https://x:8443", Auth: "none", Password: "p"}
	diags := cfg.Validate()
	if !hasAttr(diags, "password") {
		t.Fatalf("expected password attribute error, got %v", diags)
	}
}

func TestInferAuthMode(t *testing.T) {
	clearTrinoEnv(t)

	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "none", cfg: Config{}, want: "none"},
		{name: "password", cfg: Config{Password: "p"}, want: "basic"},
		{name: "token", cfg: Config{AccessToken: "t"}, want: "jwt"},
		{name: "oauth2", cfg: Config{OAuth2ClientID: "id"}, want: "oauth2"},
		{name: "oauth2 secret alone is not a mode", cfg: Config{OAuth2ClientSecret: "s"}, want: "none"},
		{name: "kerberos", cfg: Config{KerberosPrincipal: "svc"}, want: "kerberos"},
		{name: "explicit wins", cfg: Config{Auth: "none", Password: "p"}, want: "none"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Resolve().Auth; got != tc.want {
				t.Fatalf("Auth = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateKerberos(t *testing.T) {
	clearTrinoEnv(t)
	dir := t.TempDir()
	keytab := filepath.Join(dir, "trino.keytab")
	if err := os.WriteFile(keytab, []byte("keytab"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("requires https", func(t *testing.T) {
		cfg := Config{Endpoint: "http://x:8080", Auth: "kerberos", KerberosCredentialCache: "FILE:" + keytab}
		if diags := cfg.Validate(); !hasAttr(diags, "endpoint") {
			t.Fatalf("expected endpoint attribute error, got %v", diags)
		}
	})

	t.Run("keytab and cache conflict", func(t *testing.T) {
		cfg := Config{
			Endpoint:                "https://x:8443",
			Auth:                    "kerberos",
			KerberosKeytab:          keytab,
			KerberosPrincipal:       "svc",
			KerberosRealm:           "EXAMPLE.COM",
			KerberosCredentialCache: "FILE:" + keytab,
		}
		if diags := cfg.Validate(); !hasAttr(diags, "kerberos_keytab") {
			t.Fatalf("expected kerberos_keytab error, got %v", diags)
		}
	})

	t.Run("keytab requires principal and realm", func(t *testing.T) {
		cfg := Config{Endpoint: "https://x:8443", Auth: "kerberos", KerberosKeytab: keytab}
		diags := cfg.Validate()
		if !hasAttr(diags, "kerberos_principal") || !hasAttr(diags, "kerberos_realm") {
			t.Fatalf("expected principal and realm errors, got %v", diags)
		}
	})

	t.Run("pattern must not name a realm", func(t *testing.T) {
		cfg := Config{
			Endpoint:                        "https://x:8443",
			Auth:                            "kerberos",
			KerberosCredentialCache:         "FILE:" + keytab,
			KerberosServicePrincipalPattern: "svc/realm@host",
		}
		if diags := cfg.Validate(); !hasAttr(diags, "kerberos_service_principal_pattern") {
			t.Fatalf("expected pattern error, got %v", diags)
		}
	})

	t.Run("valid keytab config", func(t *testing.T) {
		cfg := Config{
			Endpoint:          "https://x:8443",
			Auth:              "kerberos",
			KerberosKeytab:    keytab,
			KerberosPrincipal: "svc",
			KerberosRealm:     "EXAMPLE.COM",
		}
		if diags := cfg.Validate(); diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
	})
}

func TestValidateOAuth2RequiresEndpoint(t *testing.T) {
	clearTrinoEnv(t)
	cfg := Config{Endpoint: "https://x:8443", Auth: "oauth2", OAuth2ClientID: "id", OAuth2ClientSecret: "s"}
	diags := cfg.Validate()
	if !hasAttr(diags, "oauth2_token_endpoint") {
		t.Fatalf("expected oauth2_token_endpoint attribute error, got %v", diags)
	}
}

func TestValidateOAuth2RequiresClientCredentials(t *testing.T) {
	clearTrinoEnv(t)
	cfg := Config{Endpoint: "https://x:8443", Auth: "oauth2", OAuth2TokenEndpoint: "https://idp/token"}
	diags := cfg.Validate()
	if !hasAttr(diags, "oauth2_client_id") || !hasAttr(diags, "oauth2_client_secret") {
		t.Fatalf("expected client id and secret errors, got %v", diags)
	}
}

func TestValidateCertConflicts(t *testing.T) {
	clearTrinoEnv(t)
	cfg := Config{Endpoint: "https://x:8443", CACert: "pem", CACertFile: "/tmp/ca.pem"}
	diags := cfg.Validate()
	if !hasAttr(diags, "ca_cert") || !hasAttr(diags, "ca_cert_file") {
		t.Fatalf("expected conflict errors naming both attributes, got %v", diags)
	}
}

func TestValidateCertWithoutKey(t *testing.T) {
	clearTrinoEnv(t)
	cfg := Config{Endpoint: "https://x:8443", ClientCert: "pem"}
	diags := cfg.Validate()
	if !hasAttr(diags, "client_key") {
		t.Fatalf("expected client_key error, got %v", diags)
	}
}

func TestValidateMissingFiles(t *testing.T) {
	clearTrinoEnv(t)
	missing := filepath.Join(t.TempDir(), "missing")
	directory := t.TempDir()

	cases := []struct {
		name string
		cfg  Config
		attr string
	}{
		{name: "ca", cfg: Config{Endpoint: "https://x:8443", CACertFile: missing}, attr: "ca_cert_file"},
		{name: "client cert", cfg: Config{Endpoint: "https://x:8443", ClientCertFile: missing, ClientKey: "pem"}, attr: "client_cert_file"},
		{name: "client key", cfg: Config{Endpoint: "https://x:8443", ClientKeyFile: missing, ClientCert: "pem"}, attr: "client_key_file"},
		{name: "kerberos keytab", cfg: Config{Endpoint: "https://x:8443", Auth: "kerberos", KerberosKeytab: missing, KerberosPrincipal: "svc", KerberosRealm: "EXAMPLE.COM"}, attr: "kerberos_keytab"},
		{name: "kerberos config", cfg: Config{Endpoint: "https://x:8443", Auth: "kerberos", KerberosCredentialCache: "FILE:" + missing, KerberosConfigFile: missing}, attr: "kerberos_config_file"},
		{name: "ca directory", cfg: Config{Endpoint: "https://x:8443", CACertFile: directory}, attr: "ca_cert_file"},
		{name: "keytab directory", cfg: Config{Endpoint: "https://x:8443", Auth: "kerberos", KerberosKeytab: directory, KerberosPrincipal: "svc", KerberosRealm: "EXAMPLE.COM"}, attr: "kerberos_keytab"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if diags := tc.cfg.Validate(); !hasAttr(diags, tc.attr) {
				t.Fatalf("expected %s error, got %v", tc.attr, diags)
			}
		})
	}
}

func TestValidateInvalidBooleanEnv(t *testing.T) {
	clearTrinoEnv(t)

	for _, tc := range []struct {
		env  string
		attr string
	}{
		{"TRINO_INSECURE_SKIP_VERIFY", "insecure_skip_verify"},
		{"TRINO_KERBEROS_USE_CANONICAL_HOSTNAME", "kerberos_use_canonical_hostname"},
	} {
		t.Run(tc.env, func(t *testing.T) {
			clearTrinoEnv(t)
			t.Setenv(tc.env, "notabool")
			if diags := (&Config{Endpoint: "https://x:8443"}).Validate(); !hasAttr(diags, tc.attr) {
				t.Fatalf("expected %s error, got %v", tc.attr, diags)
			}
		})
	}

	clearTrinoEnv(t)
	t.Setenv("TRINO_INSECURE_SKIP_VERIFY", "true")
	t.Setenv("TRINO_KERBEROS_USE_CANONICAL_HOSTNAME", "false")
	cfg := (&Config{Endpoint: "https://x:8443"}).Resolve()
	if !cfg.InsecureSkipVerify {
		t.Fatal("TRINO_INSECURE_SKIP_VERIFY=true was not applied")
	}
	if cfg.KerberosUseCanonicalHostname == nil || *cfg.KerberosUseCanonicalHostname {
		t.Fatal("TRINO_KERBEROS_USE_CANONICAL_HOSTNAME=false was not applied")
	}
	if diags := cfg.Validate(); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
}

func TestParseQueryTimeoutDefault5m(t *testing.T) {
	got, err := ParseQueryTimeout("")
	if err != nil {
		t.Fatal(err)
	}
	if got != 5*time.Minute {
		t.Fatalf("duration = %s, want 5m", got)
	}

	got, err = ParseQueryTimeout("90s")
	if err != nil {
		t.Fatal(err)
	}
	if got != 90*time.Second {
		t.Fatalf("duration = %s, want 90s", got)
	}
}

func TestValidateQueryTimeout(t *testing.T) {
	clearTrinoEnv(t)

	if diags := (&Config{Endpoint: "https://x:8443", QueryTimeout: "5m"}).Validate(); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, value := range []string{"nope", "0s", "-1s"} {
		if diags := (&Config{Endpoint: "https://x:8443", QueryTimeout: value}).Validate(); !hasAttr(diags, "query_timeout") {
			t.Fatalf("%q: expected query_timeout error, got %v", value, diags)
		}
	}
}

func TestValidateRoles(t *testing.T) {
	clearTrinoEnv(t)

	valid := &Config{Endpoint: "https://x:8443", Roles: map[string]string{"hive": "admin"}}
	if diags := valid.Validate(); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	cases := []Config{
		{Endpoint: "https://x:8443", Roles: map[string]string{"": "admin"}},
		{Endpoint: "https://x:8443", Roles: map[string]string{"hive": ""}},
	}
	for _, cfg := range cases {
		if diags := cfg.Validate(); !hasAttr(diags, "roles") {
			t.Fatalf("expected roles error, got %v", diags)
		}
	}
}

func TestValidateSessionProperties(t *testing.T) {
	clearTrinoEnv(t)

	valid := &Config{Endpoint: "https://x:8443", SessionProperties: map[string]string{"query_priority": "1"}}
	if diags := valid.Validate(); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	for _, key := range []string{"", "has space", "has=equals", "has,comma", "nönascii"} {
		cfg := &Config{Endpoint: "https://x:8443", SessionProperties: map[string]string{key: "1"}}
		if diags := cfg.Validate(); !hasAttr(diags, "session_properties") {
			t.Fatalf("%q: expected session_properties error, got %v", key, diags)
		}
	}
}

func TestValidateEndpoint(t *testing.T) {
	clearTrinoEnv(t)

	cases := []string{"ftp://x:8443", "https://x:8443/path", "https://x:8443?q=1"}
	for _, endpoint := range cases {
		if diags := (&Config{Endpoint: endpoint}).Validate(); !hasAttr(diags, "endpoint") {
			t.Fatalf("%q: expected endpoint error, got %v", endpoint, diags)
		}
	}
	if diags := (&Config{}).Validate(); !hasAttr(diags, "endpoint") {
		t.Fatalf("expected endpoint required error, got %v", diags)
	}
}

func hasAttr(diags diag.Diagnostics, name string) bool {
	target := path.Root(name)
	for _, d := range diags {
		withPath, ok := d.(diag.DiagnosticWithPath)
		if ok && withPath.Path().Equal(target) {
			return true
		}
	}
	return false
}
