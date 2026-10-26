package trino

import (
	"database/sql"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"time"

	drivertrino "github.com/trinodb/trino-go-client/trino"
)

// Options is the resolved provider configuration handed to Open. Auth is
// already resolved to one of none, basic, jwt, oauth2 or kerberos.
type Options struct {
	Auth                            string
	ServerURI                       string
	User                            string
	Password                        string
	Source                          string
	Catalog                         string
	Schema                          string
	SessionProperties               map[string]string
	ExtraCredentials                map[string]string
	Roles                           map[string]string
	AccessToken                     string
	OAuth2ClientID                  string
	OAuth2ClientSecret              string
	OAuth2TokenEndpoint             string
	OAuth2Scope                     string
	KerberosPrincipal               string
	KerberosRealm                   string
	KerberosKeytab                  string
	KerberosCredentialCache         string
	KerberosConfigFile              string
	KerberosServiceName             string
	KerberosServicePrincipalPattern string
	KerberosUseCanonicalHostname    bool
	QueryTimeout                    time.Duration
	HTTPClient                      *http.Client
}

// HTTP retry policy. Queries are idempotent, so the driver may re-send them
// after a transient failure. A mutating statement is not: a request that
// reached the coordinator but timed out client-side would fail on a retry with
// ALREADY_EXISTS, so exec statements are sent exactly once and Terraform's own
// apply/refresh cycle is the retry boundary.
const (
	requestRetryTimeout   = 2 * time.Minute
	queryRetryAttempts    = 5
	mutatingRetryAttempts = 1
)

// Open builds the driver configuration and returns the Executor the provider
// hands to resources. It does not dial: the first statement opens a connection.
func Open(opts Options) (Executor, error) {
	cfg, err := buildDriverConfig(opts)
	if err != nil {
		return nil, err
	}
	db, err := openPool(cfg)
	if err != nil {
		return nil, err
	}
	execDB, err := openPool(buildExecDriverConfig(cfg))
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &sqlExecutor{db: db, execDB: execDB}, nil
}

// openPool wraps one connector in a database/sql pool.
func openPool(cfg *drivertrino.Config) (*sql.DB, error) {
	connector, err := drivertrino.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("create Trino connector: %w", err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)
	return db, nil
}

// buildExecDriverConfig derives the configuration for mutating statements from
// the query configuration: same endpoint, auth and session settings, but no
// HTTP retries. NewConnector copies the configuration before use, so sharing
// the maps and pointers with cfg is safe.
func buildExecDriverConfig(cfg *drivertrino.Config) *drivertrino.Config {
	execCfg := *cfg
	attempts := mutatingRetryAttempts
	execCfg.RequestRetryMaxAttempts = &attempts
	return &execCfg
}

// buildDriverConfig is the pure auth-to-driver mapping, split out so it can be
// tested without a server or a KDC.
func buildDriverConfig(opts Options) (*drivertrino.Config, error) {
	serverURL, err := url.Parse(opts.ServerURI)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint %q: %w", opts.ServerURI, err)
	}
	if serverURL.Scheme != "http" && serverURL.Scheme != "https" {
		return nil, fmt.Errorf("endpoint must use http or https, got %q", serverURL.Scheme)
	}
	if serverURL.Host == "" {
		return nil, fmt.Errorf("endpoint %q has no host", opts.ServerURI)
	}
	if serverURL.Path != "" && serverURL.Path != "/" {
		return nil, fmt.Errorf("endpoint must not have a path, got %q", serverURL.Path)
	}
	if serverURL.RawQuery != "" || serverURL.Fragment != "" {
		return nil, fmt.Errorf("endpoint must not have a query or fragment, got %q", opts.ServerURI)
	}
	if serverURL.User != nil {
		return nil, fmt.Errorf("endpoint must not contain userinfo, got %q", opts.ServerURI)
	}

	switch opts.Auth {
	case "", "none":
	case "basic":
		if opts.User == "" {
			return nil, fmt.Errorf("basic auth requires a user")
		}
		if opts.Password == "" {
			return nil, fmt.Errorf("basic auth requires a password")
		}
		if serverURL.Scheme != "https" {
			return nil, fmt.Errorf("basic auth requires an https endpoint")
		}
	case "jwt", "oauth2", "kerberos":
	default:
		return nil, fmt.Errorf("unsupported auth mode %q", opts.Auth)
	}

	if opts.Password != "" {
		serverURL.User = url.UserPassword(opts.User, opts.Password)
	} else if opts.User != "" {
		serverURL.User = url.User(opts.User)
	}

	cfg := &drivertrino.Config{
		ServerURI:         serverURL.String(),
		Source:            opts.Source,
		Catalog:           opts.Catalog,
		Schema:            opts.Schema,
		SessionProperties: maps.Clone(opts.SessionProperties),
		ExtraCredentials:  maps.Clone(opts.ExtraCredentials),
		Roles:             maps.Clone(opts.Roles),
		HTTPClient:        opts.HTTPClient,
	}
	if opts.QueryTimeout > 0 {
		timeout := opts.QueryTimeout
		cfg.QueryTimeout = &timeout
	}
	retryTimeout := requestRetryTimeout
	cfg.RequestRetryTimeout = &retryTimeout
	retryAttempts := queryRetryAttempts
	cfg.RequestRetryMaxAttempts = &retryAttempts

	switch opts.Auth {
	case "basic":
		// The password lives in ServerURI userinfo; the driver derives basic
		// auth from it without ever putting it in a header we build.
	case "jwt":
		cfg.AccessToken = opts.AccessToken
	case "oauth2":
		cfg.OAuth2ClientID = opts.OAuth2ClientID
		cfg.OAuth2ClientSecret = opts.OAuth2ClientSecret
		cfg.OAuth2TokenEndpoint = opts.OAuth2TokenEndpoint
		cfg.OAuth2Scope = opts.OAuth2Scope
	case "kerberos":
		cfg.KerberosEnabled = true
		cfg.KerberosPrincipal = opts.KerberosPrincipal
		cfg.KerberosRealm = opts.KerberosRealm
		cfg.KerberosKeytabPath = opts.KerberosKeytab
		cfg.KerberosCredentialCachePath = opts.KerberosCredentialCache
		cfg.KerberosConfigPath = opts.KerberosConfigFile
		cfg.KerberosRemoteServiceName = opts.KerberosServiceName
		if cfg.KerberosRemoteServiceName == "" {
			cfg.KerberosRemoteServiceName = "HTTP"
		}
		cfg.KerberosServicePrincipalPattern = opts.KerberosServicePrincipalPattern
		cfg.KerberosDisableCanonicalHostname = !opts.KerberosUseCanonicalHostname
	}

	return cfg, nil
}
