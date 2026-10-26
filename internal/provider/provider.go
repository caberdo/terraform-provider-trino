package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/caberdo/terraform-provider-trino/internal/datasources/catalogs"
	"github.com/caberdo/terraform-provider-trino/internal/datasources/roles"
	"github.com/caberdo/terraform-provider-trino/internal/datasources/schemas"
	"github.com/caberdo/terraform-provider-trino/internal/datasources/tableprivileges"
	"github.com/caberdo/terraform-provider-trino/internal/datasources/tables"
	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/resources/catalog"
	"github.com/caberdo/terraform-provider-trino/internal/resources/function"
	"github.com/caberdo/terraform-provider-trino/internal/resources/grantprivileges"
	"github.com/caberdo/terraform-provider-trino/internal/resources/grantrole"
	"github.com/caberdo/terraform-provider-trino/internal/resources/role"
	trinoschema "github.com/caberdo/terraform-provider-trino/internal/resources/schema"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const minimumTrinoVersion = "476"

var _ provider.Provider = (*TrinoProvider)(nil)

// TrinoProvider is the Terraform provider entry point.
type TrinoProvider struct {
	version string
}

// New returns a provider factory carrying the build version reported to
// Terraform in Metadata.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &TrinoProvider{version: version}
	}
}

func (p *TrinoProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "trino"
	resp.Version = p.version
}

func (p *TrinoProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = providerschema.Schema{
		Description: "Manage Trino catalogs, schemas, roles, grants and catalog user-defined functions.",
		Attributes: map[string]providerschema.Attribute{
			"endpoint": providerschema.StringAttribute{
				Optional:    true,
				Description: "Trino coordinator URL, for example https://trino.example:8443. Falls back to TRINO_ENDPOINT.",
			},
			"auth": providerschema.StringAttribute{
				Optional:    true,
				Description: "Authentication mode: none, basic, jwt, oauth2 or kerberos. Inferred when unset. Falls back to TRINO_AUTH.",
			},
			"user": providerschema.StringAttribute{
				Optional:    true,
				Description: "Trino user name. Defaults to terraform. Falls back to TRINO_USER.",
			},
			"password": providerschema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Password for basic auth. Requires an https endpoint. Falls back to TRINO_PASSWORD.",
			},
			"access_token": providerschema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Static JWT access token. Falls back to TRINO_ACCESS_TOKEN.",
			},
			"oauth2_client_id": providerschema.StringAttribute{
				Optional:    true,
				Description: "OAuth2 client ID for the client-credentials flow. Falls back to TRINO_OAUTH2_CLIENT_ID.",
			},
			"oauth2_client_secret": providerschema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "OAuth2 client secret. Falls back to TRINO_OAUTH2_CLIENT_SECRET.",
			},
			"oauth2_token_endpoint": providerschema.StringAttribute{
				Optional:    true,
				Description: "OAuth2 token endpoint. Required for oauth2 auth because Trino 476 has no discovery. Falls back to TRINO_OAUTH2_TOKEN_ENDPOINT.",
			},
			"oauth2_scope": providerschema.StringAttribute{
				Optional:    true,
				Description: "Optional OAuth2 scope. Falls back to TRINO_OAUTH2_SCOPE.",
			},
			"kerberos_principal": providerschema.StringAttribute{
				Optional:    true,
				Description: "Kerberos principal without a realm. Falls back to TRINO_KERBEROS_PRINCIPAL.",
			},
			"kerberos_realm": providerschema.StringAttribute{
				Optional:    true,
				Description: "Kerberos realm. Required with a keytab. Falls back to TRINO_KERBEROS_REALM.",
			},
			"kerberos_keytab": providerschema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Path to a Kerberos keytab. Mutually exclusive with kerberos_credential_cache. Falls back to TRINO_KERBEROS_KEYTAB.",
			},
			"kerberos_credential_cache": providerschema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Path to a FILE credential cache, for example FILE:/tmp/krb5cc_1000. Falls back to TRINO_KERBEROS_CREDENTIAL_CACHE.",
			},
			"kerberos_config_file": providerschema.StringAttribute{
				Optional:    true,
				Description: "Path to krb5.conf. Falls back to TRINO_KERBEROS_CONFIG_FILE.",
			},
			"kerberos_service_name": providerschema.StringAttribute{
				Optional:    true,
				Description: "Kerberos service name. Defaults to HTTP. Falls back to TRINO_KERBEROS_SERVICE_NAME.",
			},
			"kerberos_service_principal_pattern": providerschema.StringAttribute{
				Optional:    true,
				Description: "Kerberos service principal pattern. Defaults to ${SERVICE}@${HOST}. Falls back to TRINO_KERBEROS_SERVICE_PRINCIPAL_PATTERN.",
			},
			"kerberos_use_canonical_hostname": providerschema.BoolAttribute{
				Optional:    true,
				Description: "Use the canonical hostname for the Kerberos service principal. Defaults to true. Falls back to TRINO_KERBEROS_USE_CANONICAL_HOSTNAME.",
			},
			"source": providerschema.StringAttribute{
				Optional:    true,
				Description: "Trino source name. Defaults to terraform-provider-trino. Falls back to TRINO_SOURCE.",
			},
			"catalog": providerschema.StringAttribute{
				Optional:    true,
				Description: "Default catalog. Falls back to TRINO_CATALOG.",
			},
			"schema": providerschema.StringAttribute{
				Optional:    true,
				Description: "Default schema. Falls back to TRINO_SCHEMA.",
			},
			"session_properties": providerschema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Session properties sent with every query.",
			},
			"extra_credentials": providerschema.MapAttribute{
				Optional:    true,
				Sensitive:   true,
				ElementType: types.StringType,
				Description: "Extra credentials sent as X-Trino-Extra-Credential.",
			},
			"roles": providerschema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Catalog to role mapping, for example { hive = \"admin\" }.",
			},
			"http_headers": providerschema.MapAttribute{
				Optional:    true,
				Sensitive:   true,
				ElementType: types.StringType,
				Description: "Static headers added to every request, for gateway or proxy auth.",
			},
			"query_timeout": providerschema.StringAttribute{
				Optional:    true,
				Description: "Query timeout as a duration, default 5m. Falls back to TRINO_QUERY_TIMEOUT.",
			},
			"insecure_skip_verify": providerschema.BoolAttribute{
				Optional:    true,
				Description: "Skip TLS certificate verification. Use only for testing. Falls back to TRINO_INSECURE_SKIP_VERIFY.",
			},
			"ca_cert": providerschema.StringAttribute{
				Optional:    true,
				Description: "CA certificate PEM content. Conflicts with ca_cert_file. Falls back to TRINO_CA_CERT.",
			},
			"ca_cert_file": providerschema.StringAttribute{
				Optional:    true,
				Description: "Path to a CA certificate PEM file. Falls back to TRINO_CA_CERT_FILE.",
			},
			"client_cert": providerschema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Client certificate PEM content for mTLS. Conflicts with client_cert_file. Falls back to TRINO_CLIENT_CERT.",
			},
			"client_cert_file": providerschema.StringAttribute{
				Optional:    true,
				Description: "Path to a client certificate PEM file. Falls back to TRINO_CLIENT_CERT_FILE.",
			},
			"client_key": providerschema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Client private key PEM content for mTLS. Conflicts with client_key_file. Falls back to TRINO_CLIENT_KEY.",
			},
			"client_key_file": providerschema.StringAttribute{
				Optional:    true,
				Description: "Path to a client private key PEM file. Falls back to TRINO_CLIENT_KEY_FILE.",
			},
		},
	}
}

func (p *TrinoProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var model providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cfg := Config{
		Endpoint:                        stringOrEmpty(model.Endpoint),
		Auth:                            stringOrEmpty(model.Auth),
		User:                            stringOrEmpty(model.User),
		Password:                        stringOrEmpty(model.Password),
		AccessToken:                     stringOrEmpty(model.AccessToken),
		OAuth2ClientID:                  stringOrEmpty(model.OAuth2ClientID),
		OAuth2ClientSecret:              stringOrEmpty(model.OAuth2ClientSecret),
		OAuth2TokenEndpoint:             stringOrEmpty(model.OAuth2TokenEndpoint),
		OAuth2Scope:                     stringOrEmpty(model.OAuth2Scope),
		KerberosPrincipal:               stringOrEmpty(model.KerberosPrincipal),
		KerberosRealm:                   stringOrEmpty(model.KerberosRealm),
		KerberosKeytab:                  stringOrEmpty(model.KerberosKeytab),
		KerberosCredentialCache:         stringOrEmpty(model.KerberosCredentialCache),
		KerberosConfigFile:              stringOrEmpty(model.KerberosConfigFile),
		KerberosServiceName:             stringOrEmpty(model.KerberosServiceName),
		KerberosServicePrincipalPattern: stringOrEmpty(model.KerberosServicePrincipalPattern),
		KerberosUseCanonicalHostname:    boolPointer(model.KerberosUseCanonicalHostname),
		Source:                          stringOrEmpty(model.Source),
		Catalog:                         stringOrEmpty(model.Catalog),
		Schema:                          stringOrEmpty(model.Schema),
		SessionProperties:               stringMap(ctx, model.SessionProperties, &resp.Diagnostics),
		ExtraCredentials:                stringMap(ctx, model.ExtraCredentials, &resp.Diagnostics),
		Roles:                           stringMap(ctx, model.Roles, &resp.Diagnostics),
		HTTPHeaders:                     stringMap(ctx, model.HTTPHeaders, &resp.Diagnostics),
		QueryTimeout:                    stringOrEmpty(model.QueryTimeout),
		InsecureSkipVerify:              boolOrFalse(model.InsecureSkipVerify),
		CACert:                          stringOrEmpty(model.CACert),
		CACertFile:                      stringOrEmpty(model.CACertFile),
		ClientCert:                      stringOrEmpty(model.ClientCert),
		ClientCertFile:                  stringOrEmpty(model.ClientCertFile),
		ClientKey:                       stringOrEmpty(model.ClientKey),
		ClientKeyFile:                   stringOrEmpty(model.ClientKeyFile),
	}.Resolve()
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(cfg.Validate()...)
	if resp.Diagnostics.HasError() {
		return
	}

	opts, _, diags := cfg.BuildOptions()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	executor, err := trino.Open(opts)
	if err != nil {
		resp.Diagnostics.AddError("Trino client configuration failed",
			"The provider could not construct a Trino client. Check the endpoint, auth mode and TLS settings.")
		return
	}

	version, err := executor.Version(ctx)
	if err != nil {
		tflog.Warn(ctx, "Trino version probe failed; continuing without a detected version")
		version = ""
	}
	if version != "" && !trino.AtLeast(version, minimumTrinoVersion) {
		tflog.Warn(ctx, "Trino version is older than the supported minimum", map[string]any{
			"version": version,
			"minimum": minimumTrinoVersion,
		})
	}

	data := &providerdata.Data{
		Executor: executor,
		Timeout:  opts.QueryTimeout,
	}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *TrinoProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		catalog.New,
		trinoschema.New,
		role.New,
		grantrole.New,
		grantprivileges.New,
		function.New,
	}
}

func (p *TrinoProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		catalogs.New,
		schemas.New,
		roles.New,
		tableprivileges.New,
		tables.New,
	}
}

type providerModel struct {
	Endpoint                        types.String `tfsdk:"endpoint"`
	Auth                            types.String `tfsdk:"auth"`
	User                            types.String `tfsdk:"user"`
	Password                        types.String `tfsdk:"password"`
	AccessToken                     types.String `tfsdk:"access_token"`
	OAuth2ClientID                  types.String `tfsdk:"oauth2_client_id"`
	OAuth2ClientSecret              types.String `tfsdk:"oauth2_client_secret"`
	OAuth2TokenEndpoint             types.String `tfsdk:"oauth2_token_endpoint"`
	OAuth2Scope                     types.String `tfsdk:"oauth2_scope"`
	KerberosPrincipal               types.String `tfsdk:"kerberos_principal"`
	KerberosRealm                   types.String `tfsdk:"kerberos_realm"`
	KerberosKeytab                  types.String `tfsdk:"kerberos_keytab"`
	KerberosCredentialCache         types.String `tfsdk:"kerberos_credential_cache"`
	KerberosConfigFile              types.String `tfsdk:"kerberos_config_file"`
	KerberosServiceName             types.String `tfsdk:"kerberos_service_name"`
	KerberosServicePrincipalPattern types.String `tfsdk:"kerberos_service_principal_pattern"`
	KerberosUseCanonicalHostname    types.Bool   `tfsdk:"kerberos_use_canonical_hostname"`
	Source                          types.String `tfsdk:"source"`
	Catalog                         types.String `tfsdk:"catalog"`
	Schema                          types.String `tfsdk:"schema"`
	SessionProperties               types.Map    `tfsdk:"session_properties"`
	ExtraCredentials                types.Map    `tfsdk:"extra_credentials"`
	Roles                           types.Map    `tfsdk:"roles"`
	HTTPHeaders                     types.Map    `tfsdk:"http_headers"`
	QueryTimeout                    types.String `tfsdk:"query_timeout"`
	InsecureSkipVerify              types.Bool   `tfsdk:"insecure_skip_verify"`
	CACert                          types.String `tfsdk:"ca_cert"`
	CACertFile                      types.String `tfsdk:"ca_cert_file"`
	ClientCert                      types.String `tfsdk:"client_cert"`
	ClientCertFile                  types.String `tfsdk:"client_cert_file"`
	ClientKey                       types.String `tfsdk:"client_key"`
	ClientKeyFile                   types.String `tfsdk:"client_key_file"`
}

func stringOrEmpty(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return value.ValueString()
}

func boolOrFalse(value types.Bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return false
	}
	return value.ValueBool()
}

func boolPointer(value types.Bool) *bool {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	result := value.ValueBool()
	return &result
}

func stringMap(ctx context.Context, value types.Map, diags *diag.Diagnostics) map[string]string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	result := map[string]string{}
	diags.Append(value.ElementsAs(ctx, &result, false)...)
	return result
}
