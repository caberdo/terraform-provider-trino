package trino

import (
	"database/sql"
	"net/http"
	"testing"
	"time"
)

func TestBuildDriverConfigBasic(t *testing.T) {
	client := &http.Client{}
	opts := Options{
		Auth:      "basic",
		ServerURI: "https://trino.example.com:8443",
		User:      "alice",
		Password:  "s3cret",
		Source:    "terraform-provider-trino",
		Catalog:   "hive",
		Schema:    "sales",
		SessionProperties: map[string]string{
			"query_max_run_time": "1h",
		},
		ExtraCredentials: map[string]string{"user": "alice"},
		Roles:            map[string]string{"hive": "admin"},
		QueryTimeout:     5 * time.Minute,
		HTTPClient:       client,
	}

	cfg, err := buildDriverConfig(opts)
	if err != nil {
		t.Fatalf("buildDriverConfig: %v", err)
	}
	if got, want := cfg.ServerURI, "https://alice:s3cret@trino.example.com:8443"; got != want {
		t.Errorf("ServerURI = %q, want %q", got, want)
	}
	if cfg.Source != "terraform-provider-trino" || cfg.Catalog != "hive" || cfg.Schema != "sales" {
		t.Errorf("source/catalog/schema = %q/%q/%q", cfg.Source, cfg.Catalog, cfg.Schema)
	}
	if cfg.SessionProperties["query_max_run_time"] != "1h" {
		t.Error("session properties not copied")
	}
	if cfg.ExtraCredentials["user"] != "alice" {
		t.Error("extra credentials not copied")
	}
	if cfg.Roles["hive"] != "admin" {
		t.Error("roles not copied")
	}
	if cfg.QueryTimeout == nil || *cfg.QueryTimeout != 5*time.Minute {
		t.Errorf("QueryTimeout = %v", cfg.QueryTimeout)
	}
	if cfg.RequestRetryTimeout == nil || *cfg.RequestRetryTimeout != 2*time.Minute {
		t.Errorf("RequestRetryTimeout = %v", cfg.RequestRetryTimeout)
	}
	if cfg.RequestRetryMaxAttempts == nil || *cfg.RequestRetryMaxAttempts != 5 {
		t.Errorf("RequestRetryMaxAttempts = %v", cfg.RequestRetryMaxAttempts)
	}
	if cfg.HTTPClient != client {
		t.Error("HTTPClient not passed through")
	}
	if cfg.KerberosEnabled || cfg.AccessToken != "" || cfg.OAuth2ClientID != "" {
		t.Error("credential fields should be empty for basic auth")
	}
}

func TestBuildDriverConfigJWT(t *testing.T) {
	cfg, err := buildDriverConfig(Options{
		Auth:        "jwt",
		ServerURI:   "https://trino.example.com",
		User:        "alice",
		AccessToken: "header.payload.signature",
	})
	if err != nil {
		t.Fatalf("buildDriverConfig: %v", err)
	}
	if cfg.AccessToken != "header.payload.signature" {
		t.Errorf("AccessToken = %q", cfg.AccessToken)
	}
	if cfg.ServerURI != "https://alice@trino.example.com" {
		t.Errorf("ServerURI = %q", cfg.ServerURI)
	}
}

func TestBuildDriverConfigOAuth2(t *testing.T) {
	cfg, err := buildDriverConfig(Options{
		Auth:                "oauth2",
		ServerURI:           "https://trino.example.com",
		User:                "alice",
		OAuth2ClientID:      "client-id",
		OAuth2ClientSecret:  "client-secret",
		OAuth2TokenEndpoint: "https://auth.example.com/token",
		OAuth2Scope:         "trino",
	})
	if err != nil {
		t.Fatalf("buildDriverConfig: %v", err)
	}
	if cfg.OAuth2ClientID != "client-id" ||
		cfg.OAuth2ClientSecret != "client-secret" ||
		cfg.OAuth2TokenEndpoint != "https://auth.example.com/token" ||
		cfg.OAuth2Scope != "trino" {
		t.Errorf("oauth2 fields = %q/%q/%q/%q",
			cfg.OAuth2ClientID, cfg.OAuth2ClientSecret, cfg.OAuth2TokenEndpoint, cfg.OAuth2Scope)
	}
	if cfg.AccessToken != "" {
		t.Error("oauth2 config must not carry a static access token")
	}
}

func TestBuildDriverConfigKerberos(t *testing.T) {
	cfg, err := buildDriverConfig(Options{
		Auth:                            "kerberos",
		ServerURI:                       "https://trino.example.com:8443",
		User:                            "alice@EXAMPLE.COM",
		KerberosPrincipal:               "alice",
		KerberosRealm:                   "EXAMPLE.COM",
		KerberosKeytab:                  "/etc/security/alice.keytab",
		KerberosConfigFile:              "/etc/krb5.conf",
		KerberosServicePrincipalPattern: "${SERVICE}@${HOST}",
		KerberosUseCanonicalHostname:    true,
	})
	if err != nil {
		t.Fatalf("buildDriverConfig: %v", err)
	}
	if !cfg.KerberosEnabled {
		t.Error("KerberosEnabled should be true")
	}
	if cfg.KerberosPrincipal != "alice" || cfg.KerberosRealm != "EXAMPLE.COM" {
		t.Errorf("principal/realm = %q/%q", cfg.KerberosPrincipal, cfg.KerberosRealm)
	}
	if cfg.KerberosKeytabPath != "/etc/security/alice.keytab" {
		t.Errorf("KerberosKeytabPath = %q", cfg.KerberosKeytabPath)
	}
	if cfg.KerberosConfigPath != "/etc/krb5.conf" {
		t.Errorf("KerberosConfigPath = %q", cfg.KerberosConfigPath)
	}
	if cfg.KerberosRemoteServiceName != "HTTP" {
		t.Errorf("KerberosRemoteServiceName = %q, want HTTP", cfg.KerberosRemoteServiceName)
	}
	if cfg.KerberosServicePrincipalPattern != "${SERVICE}@${HOST}" {
		t.Errorf("KerberosServicePrincipalPattern = %q", cfg.KerberosServicePrincipalPattern)
	}
	if cfg.KerberosDisableCanonicalHostname {
		t.Error("use_canonical_hostname=true should set DisableCanonicalHostname=false")
	}
	if cfg.AccessToken != "" || cfg.OAuth2ClientID != "" {
		t.Error("kerberos config must not carry other credentials")
	}
}

func TestBuildDriverConfigKerberosCacheMode(t *testing.T) {
	cfg, err := buildDriverConfig(Options{
		Auth:                         "kerberos",
		ServerURI:                    "https://trino.example.com:8443",
		User:                         "alice",
		KerberosCredentialCache:      "FILE:/tmp/krb5cc_1000",
		KerberosServiceName:          "trino",
		KerberosUseCanonicalHostname: false,
	})
	if err != nil {
		t.Fatalf("buildDriverConfig: %v", err)
	}
	if cfg.KerberosKeytabPath != "" {
		t.Errorf("KerberosKeytabPath = %q, want empty", cfg.KerberosKeytabPath)
	}
	if cfg.KerberosCredentialCachePath != "FILE:/tmp/krb5cc_1000" {
		t.Errorf("KerberosCredentialCachePath = %q", cfg.KerberosCredentialCachePath)
	}
	if cfg.KerberosRemoteServiceName != "trino" {
		t.Errorf("KerberosRemoteServiceName = %q, want trino", cfg.KerberosRemoteServiceName)
	}
	if !cfg.KerberosDisableCanonicalHostname {
		t.Error("use_canonical_hostname=false should set DisableCanonicalHostname=true")
	}
}

func TestBuildDriverConfigNone(t *testing.T) {
	cfg, err := buildDriverConfig(Options{
		Auth:      "none",
		ServerURI: "https://trino.example.com:8443",
		User:      "terraform",
	})
	if err != nil {
		t.Fatalf("buildDriverConfig: %v", err)
	}
	if cfg.ServerURI != "https://terraform@trino.example.com:8443" {
		t.Errorf("ServerURI = %q", cfg.ServerURI)
	}
	if cfg.AccessToken != "" || cfg.KerberosEnabled || cfg.OAuth2ClientID != "" {
		t.Error("none auth must not carry credentials")
	}
}

func TestBuildDriverConfigRejectsBadServerURI(t *testing.T) {
	cases := map[string]string{
		"no scheme": "trino.example.com:8443",
		"path":      "https://trino.example.com/v1/statement",
		"query":     "https://trino.example.com?x=1",
		"scheme":    "ftp://trino.example.com",
	}
	for name, serverURI := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := buildDriverConfig(Options{Auth: "none", ServerURI: serverURI}); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestOpenRejectsPasswordOverHTTP(t *testing.T) {
	_, err := Open(Options{
		Auth:      "basic",
		ServerURI: "http://trino.example.com:8080",
		User:      "alice",
		Password:  "s3cret",
	})
	if err == nil {
		t.Fatal("Open should reject basic auth over http")
	}
}

func TestBuildExecDriverConfigDisablesRetries(t *testing.T) {
	opts := Options{Auth: "none", ServerURI: "http://trino.example.com:8080", User: "alice"}
	queryCfg, err := buildDriverConfig(opts)
	if err != nil {
		t.Fatalf("buildDriverConfig: %v", err)
	}
	execCfg := buildExecDriverConfig(queryCfg)

	if execCfg.RequestRetryMaxAttempts == nil || *execCfg.RequestRetryMaxAttempts != 1 {
		t.Errorf("exec RequestRetryMaxAttempts = %v, want 1", execCfg.RequestRetryMaxAttempts)
	}
	if queryCfg.RequestRetryMaxAttempts == nil || *queryCfg.RequestRetryMaxAttempts != queryRetryAttempts {
		t.Errorf("query RequestRetryMaxAttempts = %v, want %d", queryCfg.RequestRetryMaxAttempts, queryRetryAttempts)
	}
	if execCfg.ServerURI != queryCfg.ServerURI || execCfg.Source != queryCfg.Source {
		t.Errorf("exec config lost the endpoint or source: %q %q", execCfg.ServerURI, execCfg.Source)
	}
	if execCfg.RequestRetryTimeout != queryCfg.RequestRetryTimeout {
		t.Error("exec config did not keep the retry timeout")
	}
}

func TestOpenDefaults(t *testing.T) {
	executor, err := Open(Options{
		Auth:      "none",
		ServerURI: "http://trino.example.com:8080",
		User:      "terraform",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		if err := executor.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	sqlExecutor, ok := executor.(*sqlExecutor)
	if !ok {
		t.Fatalf("Open returned %T, want *sqlExecutor", executor)
	}
	for name, db := range map[string]*sql.DB{"query": sqlExecutor.db, "exec": sqlExecutor.execDB} {
		if db == nil {
			t.Fatalf("%s pool is nil", name)
		}
		if stats := db.Stats(); stats.MaxOpenConnections != 2 {
			t.Errorf("%s MaxOpenConnections = %d, want 2", name, stats.MaxOpenConnections)
		}
	}
}
