package acctest

import (
	"context"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/caberdo/terraform-provider-trino/internal/provider"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

// ProtoV6ProviderFactories wires the provider into terraform-plugin-testing.
func ProtoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"trino": providerserver.NewProtocol6WithError(provider.New("test")()),
	}
}

// PreCheck skips the test unless acceptance testing is enabled and an endpoint
// is configured.
func PreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC not set")
	}
	if os.Getenv("TRINO_ENDPOINT") == "" {
		t.Fatal("TRINO_ENDPOINT must be set for acceptance tests")
	}
}

// Executor opens a client from the TRINO_* environment for fixtures and
// out-of-band assertions.
func Executor(t *testing.T) trino.Executor {
	t.Helper()
	cfg := (provider.Config{}).Resolve()
	if diags := cfg.Validate(); diags.HasError() {
		t.Fatalf("invalid Trino configuration: %s", diags[0].Detail())
	}
	opts, _, diags := cfg.BuildOptions()
	if diags.HasError() {
		t.Fatalf("invalid Trino configuration: %s", diags[0].Detail())
	}
	executor, err := trino.Open(opts)
	if err != nil {
		t.Fatalf("open Trino client: %v", err)
	}
	t.Cleanup(func() {
		_ = executor.Close()
	})
	return executor
}

var ddlPattern = regexp.MustCompile(`(?i)^\s*CREATE\s+(SCHEMA|TABLE|VIEW|ROLE)\s+(?:IF\s+NOT\s+EXISTS\s+)?([^\s;]+)`)

// Fixture executes setup statements and returns a cleanup that drops the
// created objects in reverse order. Statements that are not CREATE DDL are
// ignored by cleanup.
func Fixture(t *testing.T, statements ...string) func() {
	t.Helper()
	executor := Executor(t)
	ctx := context.Background()

	type drop struct {
		kind string
		name string
	}
	drops := make([]drop, 0, len(statements))

	for _, statement := range statements {
		if err := executor.Exec(ctx, statement); err != nil {
			t.Fatalf("fixture statement %q failed: %v", statement, err)
		}
		if match := ddlPattern.FindStringSubmatch(statement); match != nil {
			drops = append(drops, drop{kind: match[1], name: match[2]})
		}
	}

	return func() {
		for i := len(drops) - 1; i >= 0; i-- {
			statement := "DROP " + drops[i].kind + " IF EXISTS " + drops[i].name
			if err := executor.Exec(ctx, statement); err != nil {
				t.Logf("fixture cleanup %q failed: %v", statement, err)
			}
		}
	}
}
