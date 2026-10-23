package provider

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const (
	authNone     = "none"
	authBasic    = "basic"
	authJWT      = "jwt"
	authOAuth2   = "oauth2"
	authKerberos = "kerberos"

	defaultUser            = "terraform"
	defaultSource          = "terraform-provider-trino"
	defaultQueryTimeout    = "5m"
	defaultKerberosService = "HTTP"
	defaultKerberosPattern = "${SERVICE}@${HOST}"
)

// Config is the provider configuration after it has been read from Terraform.
// Resolve applies environment fallbacks and auth inference; Validate checks the
// cross-field rules that attribute validators cannot express.
type Config struct {
	Endpoint string
	Auth     string
	User     string
	Password string

	AccessToken string

	OAuth2ClientID      string
	OAuth2ClientSecret  string
	OAuth2TokenEndpoint string
	OAuth2Scope         string

	KerberosPrincipal               string
	KerberosRealm                   string
	KerberosKeytab                  string
	KerberosCredentialCache         string
	KerberosConfigFile              string
	KerberosServiceName             string
	KerberosServicePrincipalPattern string
	KerberosUseCanonicalHostname    *bool

	Source            string
	Catalog           string
	Schema            string
	SessionProperties map[string]string
	ExtraCredentials  map[string]string
	Roles             map[string]string
	HTTPHeaders       map[string]string
	QueryTimeout      string

	InsecureSkipVerify bool
	CACert             string
	CACertFile         string
	ClientCert         string
	ClientCertFile     string
	ClientKey          string
	ClientKeyFile      string

	envErrors []envError
}

type envError struct {
	attribute string
	name      string
}

type envFallback struct {
	value *string
	name  string
}

// Resolve applies attribute -> environment -> default precedence and infers the
// auth mode when the attribute does not name one.
func (c Config) Resolve() Config {
	out := c
	out.envErrors = append([]envError(nil), c.envErrors...)

	fallbacks := []envFallback{
		{value: &out.Endpoint, name: "TRINO_ENDPOINT"},
		{value: &out.Auth, name: "TRINO_AUTH"},
		{value: &out.User, name: "TRINO_USER"},
		{value: &out.Password, name: "TRINO_PASSWORD"},
		{value: &out.AccessToken, name: "TRINO_ACCESS_TOKEN"},
		{value: &out.OAuth2ClientID, name: "TRINO_OAUTH2_CLIENT_ID"},
		{value: &out.OAuth2ClientSecret, name: "TRINO_OAUTH2_CLIENT_SECRET"},
		{value: &out.OAuth2TokenEndpoint, name: "TRINO_OAUTH2_TOKEN_ENDPOINT"},
		{value: &out.OAuth2Scope, name: "TRINO_OAUTH2_SCOPE"},
		{value: &out.KerberosPrincipal, name: "TRINO_KERBEROS_PRINCIPAL"},
		{value: &out.KerberosRealm, name: "TRINO_KERBEROS_REALM"},
		{value: &out.KerberosKeytab, name: "TRINO_KERBEROS_KEYTAB"},
		{value: &out.KerberosCredentialCache, name: "TRINO_KERBEROS_CREDENTIAL_CACHE"},
		{value: &out.KerberosConfigFile, name: "TRINO_KERBEROS_CONFIG_FILE"},
		{value: &out.KerberosServiceName, name: "TRINO_KERBEROS_SERVICE_NAME"},
		{value: &out.KerberosServicePrincipalPattern, name: "TRINO_KERBEROS_SERVICE_PRINCIPAL_PATTERN"},
		{value: &out.Source, name: "TRINO_SOURCE"},
		{value: &out.Catalog, name: "TRINO_CATALOG"},
		{value: &out.Schema, name: "TRINO_SCHEMA"},
		{value: &out.QueryTimeout, name: "TRINO_QUERY_TIMEOUT"},
		{value: &out.CACert, name: "TRINO_CA_CERT"},
		{value: &out.CACertFile, name: "TRINO_CA_CERT_FILE"},
		{value: &out.ClientCert, name: "TRINO_CLIENT_CERT"},
		{value: &out.ClientCertFile, name: "TRINO_CLIENT_CERT_FILE"},
		{value: &out.ClientKey, name: "TRINO_CLIENT_KEY"},
		{value: &out.ClientKeyFile, name: "TRINO_CLIENT_KEY_FILE"},
	}
	for _, fb := range fallbacks {
		if *fb.value == "" {
			*fb.value = envNonEmpty(fb.name)
		}
	}

	if out.KerberosUseCanonicalHostname == nil {
		value := true
		if raw := envNonEmpty("TRINO_KERBEROS_USE_CANONICAL_HOSTNAME"); raw != "" {
			if parsed, err := strconv.ParseBool(raw); err != nil {
				out.addEnvError("kerberos_use_canonical_hostname", "TRINO_KERBEROS_USE_CANONICAL_HOSTNAME")
			} else {
				value = parsed
			}
		}
		out.KerberosUseCanonicalHostname = &value
	}
	// A bool cannot distinguish "unset" from "false", so the environment
	// fallback only applies while the attribute is false.
	if !out.InsecureSkipVerify {
		if raw := envNonEmpty("TRINO_INSECURE_SKIP_VERIFY"); raw != "" {
			if parsed, err := strconv.ParseBool(raw); err != nil {
				out.addEnvError("insecure_skip_verify", "TRINO_INSECURE_SKIP_VERIFY")
			} else {
				out.InsecureSkipVerify = parsed
			}
		}
	}

	if out.Auth == "" {
		out.Auth = inferAuth(out)
	}

	if out.User == "" {
		out.User = defaultUser
	}
	if out.Source == "" {
		out.Source = defaultSource
	}
	if out.QueryTimeout == "" {
		out.QueryTimeout = defaultQueryTimeout
	}
	if out.KerberosServiceName == "" {
		out.KerberosServiceName = defaultKerberosService
	}
	if out.KerberosServicePrincipalPattern == "" {
		out.KerberosServicePrincipalPattern = defaultKerberosPattern
	}
	out.SessionProperties = cloneMap(out.SessionProperties)
	out.ExtraCredentials = cloneMap(out.ExtraCredentials)
	out.Roles = cloneMap(out.Roles)
	out.HTTPHeaders = cloneMap(out.HTTPHeaders)

	return out
}

func envNonEmpty(name string) string {
	return os.Getenv(name)
}

func (c *Config) addEnvError(attribute, name string) {
	for _, existing := range c.envErrors {
		if existing.attribute == attribute && existing.name == name {
			return
		}
	}
	c.envErrors = append(c.envErrors, envError{attribute: attribute, name: name})
}

func inferAuth(c Config) string {
	if kerberosConfigured(c) {
		return authKerberos
	}
	if c.Password != "" {
		return authBasic
	}
	if c.AccessToken != "" {
		return authJWT
	}
	if c.OAuth2ClientID != "" {
		return authOAuth2
	}
	return authNone
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func kerberosConfigured(c Config) bool {
	return c.KerberosPrincipal != "" ||
		c.KerberosRealm != "" ||
		c.KerberosKeytab != "" ||
		c.KerberosCredentialCache != "" ||
		c.KerberosConfigFile != "" ||
		(c.KerberosServiceName != "" && c.KerberosServiceName != defaultKerberosService) ||
		(c.KerberosServicePrincipalPattern != "" && c.KerberosServicePrincipalPattern != defaultKerberosPattern) ||
		(c.KerberosUseCanonicalHostname != nil && !*c.KerberosUseCanonicalHostname)
}

// Validate returns attribute-scoped diagnostics for every cross-field rule.
func (c Config) Validate() diag.Diagnostics {
	c = c.Resolve()

	var diags diag.Diagnostics
	endpointOK, endpointScheme := validateEndpoint(&diags, c.Endpoint)

	switch c.Auth {
	case authNone, authBasic, authJWT, authOAuth2, authKerberos:
	default:
		attributeError(&diags, "auth", "Auth mode is not supported",
			"Use one of none, basic, jwt, oauth2 or kerberos.")
	}

	switch c.Auth {
	case authNone:
		forbidCredentials(&diags, c, authNone)
	case authBasic:
		if c.Password == "" {
			attributeError(&diags, "password", "Password is required",
				"Basic auth requires a password.")
		}
		if c.AccessToken != "" {
			forbid(&diags, "access_token", authBasic)
		}
		forbidOAuth2(&diags, c, authBasic)
		forbidKerberos(&diags, c, authBasic)
		if c.Password != "" && endpointOK && endpointScheme == "http" {
			attributeError(&diags, "password", "Password requires TLS",
				"Basic auth sends the password to the server, so the endpoint must use https.")
		}
	case authJWT:
		if c.AccessToken == "" {
			attributeError(&diags, "access_token", "Access token is required",
				"JWT auth requires an access token.")
		}
		if c.Password != "" {
			forbid(&diags, "password", authJWT)
		}
		forbidOAuth2(&diags, c, authJWT)
		forbidKerberos(&diags, c, authJWT)
	case authOAuth2:
		if c.OAuth2ClientID == "" {
			attributeError(&diags, "oauth2_client_id", "OAuth2 client ID is required",
				"OAuth2 auth requires a client ID.")
		}
		if c.OAuth2ClientSecret == "" {
			attributeError(&diags, "oauth2_client_secret", "OAuth2 client secret is required",
				"OAuth2 auth requires a client secret.")
		}
		if c.OAuth2TokenEndpoint == "" {
			attributeError(&diags, "oauth2_token_endpoint", "OAuth2 token endpoint is required",
				"OAuth2 auth requires a token endpoint; Trino 476 has no endpoint discovery.")
		}
		if c.Password != "" {
			forbid(&diags, "password", authOAuth2)
		}
		if c.AccessToken != "" {
			forbid(&diags, "access_token", authOAuth2)
		}
		forbidKerberos(&diags, c, authOAuth2)
	case authKerberos:
		if endpointOK && endpointScheme != "https" {
			attributeError(&diags, "endpoint", "Kerberos requires TLS",
				"Kerberos auth requires an https endpoint.")
		}
		if c.KerberosKeytab != "" && c.KerberosCredentialCache != "" {
			attributeError(&diags, "kerberos_keytab", "Kerberos keytab and credential cache conflict",
				"Set kerberos_keytab or kerberos_credential_cache, not both.")
			attributeError(&diags, "kerberos_credential_cache", "Kerberos keytab and credential cache conflict",
				"Set kerberos_keytab or kerberos_credential_cache, not both.")
		}
		if c.KerberosKeytab != "" {
			if c.KerberosPrincipal == "" {
				attributeError(&diags, "kerberos_principal", "Kerberos principal is required",
					"A keytab requires a principal without a realm.")
			}
			if c.KerberosRealm == "" {
				attributeError(&diags, "kerberos_realm", "Kerberos realm is required",
					"A keytab requires an explicit realm; the driver does not infer it.")
			}
		}
		if patternNamesRealm(c.KerberosServicePrincipalPattern) {
			attributeError(&diags, "kerberos_service_principal_pattern", "Kerberos pattern names a realm",
				"Use the service@host form and map the host to its realm in krb5.conf.")
		}
		if c.Password != "" {
			forbid(&diags, "password", authKerberos)
		}
		if c.AccessToken != "" {
			forbid(&diags, "access_token", authKerberos)
		}
		forbidOAuth2(&diags, c, authKerberos)
	}

	validateTLS(&diags, c)
	validateQueryTimeout(&diags, c.QueryTimeout)
	validateSessionProperties(&diags, c.SessionProperties)
	validateRoles(&diags, c.Roles)
	validateFiles(&diags, c)
	for _, envErr := range c.envErrors {
		attributeError(&diags, envErr.attribute, "Environment variable is invalid",
			envErr.name+" must be true or false.")
	}

	return diags
}

// BuildOptions maps the resolved configuration to trino.Options and builds the
// HTTP client that carries TLS and static headers.
func (c Config) BuildOptions() (trino.Options, *http.Client, diag.Diagnostics) {
	c = c.Resolve()

	var diags diag.Diagnostics
	timeout, err := ParseQueryTimeout(c.QueryTimeout)
	if err != nil {
		attributeError(&diags, "query_timeout", "Query timeout is invalid",
			"Use a positive duration such as 5m or 90s.")
	}

	client, err := trino.NewHTTPClient(trino.TLSOptions{
		CAFile:             c.CACertFile,
		CAPEM:              c.CACert,
		ClientCertFile:     c.ClientCertFile,
		ClientCertPEM:      c.ClientCert,
		ClientKeyFile:      c.ClientKeyFile,
		ClientKeyPEM:       c.ClientKey,
		InsecureSkipVerify: c.InsecureSkipVerify,
		Headers:            c.HTTPHeaders,
	})
	if err != nil {
		attributeError(&diags, tlsErrorAttribute(err), "TLS configuration is invalid",
			"Check the CA certificate and the client certificate and key.")
	}

	canonical := true
	if c.KerberosUseCanonicalHostname != nil {
		canonical = *c.KerberosUseCanonicalHostname
	}

	opts := trino.Options{
		Auth:                            c.Auth,
		ServerURI:                       c.Endpoint,
		User:                            c.User,
		Password:                        c.Password,
		Source:                          c.Source,
		Catalog:                         c.Catalog,
		Schema:                          c.Schema,
		SessionProperties:               c.SessionProperties,
		ExtraCredentials:                c.ExtraCredentials,
		Roles:                           c.Roles,
		AccessToken:                     c.AccessToken,
		OAuth2ClientID:                  c.OAuth2ClientID,
		OAuth2ClientSecret:              c.OAuth2ClientSecret,
		OAuth2TokenEndpoint:             c.OAuth2TokenEndpoint,
		OAuth2Scope:                     c.OAuth2Scope,
		KerberosPrincipal:               c.KerberosPrincipal,
		KerberosRealm:                   c.KerberosRealm,
		KerberosKeytab:                  c.KerberosKeytab,
		KerberosCredentialCache:         c.KerberosCredentialCache,
		KerberosConfigFile:              c.KerberosConfigFile,
		KerberosServiceName:             c.KerberosServiceName,
		KerberosServicePrincipalPattern: c.KerberosServicePrincipalPattern,
		KerberosUseCanonicalHostname:    canonical,
		QueryTimeout:                    timeout,
		HTTPClient:                      client,
	}

	return opts, client, diags
}

// ParseQueryTimeout parses the provider's query_timeout value, defaulting to
// five minutes when it is empty.
func ParseQueryTimeout(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 5 * time.Minute, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("query_timeout is not a duration")
	}
	if duration <= 0 {
		return 0, fmt.Errorf("query_timeout must be positive")
	}
	return duration, nil
}

func validateEndpoint(diags *diag.Diagnostics, endpoint string) (bool, string) {
	if endpoint == "" {
		attributeError(diags, "endpoint", "Endpoint is required",
			"Set endpoint or the TRINO_ENDPOINT environment variable.")
		return false, ""
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		attributeError(diags, "endpoint", "Endpoint is invalid",
			"Use a URL such as https://trino.example:8443.")
		return false, ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		attributeError(diags, "endpoint", "Endpoint scheme is not supported",
			"Use http or https.")
		return false, parsed.Scheme
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		attributeError(diags, "endpoint", "Endpoint must not include a path or query",
			"Use only scheme, host and port, such as https://trino.example:8443.")
		return false, parsed.Scheme
	}
	if parsed.User != nil {
		attributeError(diags, "endpoint", "Endpoint must not include userinfo",
			"Set user and password as provider attributes, not in the endpoint.")
		return false, parsed.Scheme
	}
	return true, parsed.Scheme
}

func forbidCredentials(diags *diag.Diagnostics, c Config, auth string) {
	if c.Password != "" {
		forbid(diags, "password", auth)
	}
	if c.AccessToken != "" {
		forbid(diags, "access_token", auth)
	}
	forbidOAuth2(diags, c, auth)
	forbidKerberos(diags, c, auth)
}

func forbidOAuth2(diags *diag.Diagnostics, c Config, auth string) {
	for _, entry := range []struct {
		attr string
		set  bool
	}{
		{"oauth2_client_id", c.OAuth2ClientID != ""},
		{"oauth2_client_secret", c.OAuth2ClientSecret != ""},
		{"oauth2_token_endpoint", c.OAuth2TokenEndpoint != ""},
		{"oauth2_scope", c.OAuth2Scope != ""},
	} {
		if entry.set {
			forbid(diags, entry.attr, auth)
		}
	}
}

func forbidKerberos(diags *diag.Diagnostics, c Config, auth string) {
	for _, entry := range []struct {
		attr string
		set  bool
	}{
		{"kerberos_principal", c.KerberosPrincipal != ""},
		{"kerberos_realm", c.KerberosRealm != ""},
		{"kerberos_keytab", c.KerberosKeytab != ""},
		{"kerberos_credential_cache", c.KerberosCredentialCache != ""},
		{"kerberos_config_file", c.KerberosConfigFile != ""},
		{"kerberos_service_name", c.KerberosServiceName != "" && c.KerberosServiceName != defaultKerberosService},
		{"kerberos_service_principal_pattern", c.KerberosServicePrincipalPattern != "" && c.KerberosServicePrincipalPattern != defaultKerberosPattern},
		{"kerberos_use_canonical_hostname", c.KerberosUseCanonicalHostname != nil && !*c.KerberosUseCanonicalHostname},
	} {
		if entry.set {
			forbid(diags, entry.attr, auth)
		}
	}
}

func forbid(diags *diag.Diagnostics, attr, auth string) {
	attributeError(diags, attr, "Credential is not allowed",
		fmt.Sprintf("The %s attribute cannot be used with auth %q.", attr, auth))
}

func patternNamesRealm(pattern string) bool {
	service, _, ok := cutLast(pattern, "@")
	return ok && strings.Contains(service, "/")
}

func cutLast(s, sep string) (string, string, bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

func validateTLS(diags *diag.Diagnostics, c Config) {
	if c.CACert != "" && c.CACertFile != "" {
		conflict(diags, "ca_cert", "ca_cert_file", "ca_cert", "ca_cert_file")
	}
	if c.ClientCert != "" && c.ClientCertFile != "" {
		conflict(diags, "client_cert", "client_cert_file", "client_cert", "client_cert_file")
	}
	if c.ClientKey != "" && c.ClientKeyFile != "" {
		conflict(diags, "client_key", "client_key_file", "client_key", "client_key_file")
	}

	hasCert := c.ClientCert != "" || c.ClientCertFile != ""
	hasKey := c.ClientKey != "" || c.ClientKeyFile != ""
	if hasCert && !hasKey {
		attributeError(diags, "client_key", "Client key is required",
			"A client certificate requires a client key.")
	}
	if hasKey && !hasCert {
		attributeError(diags, "client_cert", "Client certificate is required",
			"A client key requires a client certificate.")
	}
}

func conflict(diags *diag.Diagnostics, first, second, firstName, secondName string) {
	detail := fmt.Sprintf("Set only one of %s and %s.", firstName, secondName)
	attributeError(diags, first, "Conflicting certificate settings", detail)
	attributeError(diags, second, "Conflicting certificate settings", detail)
}

func validateQueryTimeout(diags *diag.Diagnostics, value string) {
	if _, err := ParseQueryTimeout(value); err != nil {
		attributeError(diags, "query_timeout", "Query timeout is invalid",
			"Use a positive duration such as 5m or 90s.")
	}
}

func validateSessionProperties(diags *diag.Diagnostics, properties map[string]string) {
	keys := sortedKeys(properties)
	for _, key := range keys {
		if !validMapKey(key) {
			attributeError(diags, "session_properties", "Session property key is invalid",
				"Keys must be non-empty printable ASCII without spaces, equals signs or commas.")
			return
		}
	}
}

func validateRoles(diags *diag.Diagnostics, roles map[string]string) {
	for _, catalog := range sortedKeys(roles) {
		if strings.TrimSpace(catalog) == "" || strings.TrimSpace(roles[catalog]) == "" {
			attributeError(diags, "roles", "Role mapping is invalid",
				"Catalog names and role names must be non-empty.")
			return
		}
	}
}

func validateFiles(diags *diag.Diagnostics, c Config) {
	validateReadable(diags, "ca_cert_file", c.CACertFile, false)
	validateReadable(diags, "client_cert_file", c.ClientCertFile, false)
	validateReadable(diags, "client_key_file", c.ClientKeyFile, false)
	validateReadable(diags, "kerberos_keytab", c.KerberosKeytab, false)
	validateReadable(diags, "kerberos_config_file", c.KerberosConfigFile, false)
	validateReadable(diags, "kerberos_credential_cache", c.KerberosCredentialCache, true)
}

func validateReadable(diags *diag.Diagnostics, attr, value string, credentialCache bool) {
	if value == "" {
		return
	}
	path := strings.TrimPrefix(value, "FILE:")
	if credentialCache && strings.Contains(path, ":") {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		attributeError(diags, attr, "File is not readable",
			"The configured file does not exist or cannot be read. Check the path and its permissions.")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		attributeError(diags, attr, "File is not readable",
			"The configured file does not exist or cannot be read. Check the path and its permissions.")
		return
	}
	_ = file.Close()
}

func tlsErrorAttribute(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "ca_cert_file"):
		return "ca_cert_file"
	case strings.Contains(message, "ca_cert"):
		return "ca_cert"
	case strings.Contains(message, "client_cert"):
		return "client_cert"
	case strings.Contains(message, "client_key"):
		return "client_key"
	default:
		return "client_cert"
	}
}

func validMapKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e || key[i] == '=' || key[i] == ',' {
			return false
		}
	}
	return true
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func attributeError(diags *diag.Diagnostics, attr, summary, detail string) {
	diags.AddAttributeError(path.Root(attr), summary, detail)
}
