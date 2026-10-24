package acctest

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
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
// is configured. When TRINO_EXPECT_VERSION is set it also asserts the server
// reports that version, so a stale or mis-tagged image fails before any
// resource test runs instead of producing a confusing capability failure.
func PreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC not set")
	}
	if os.Getenv("TRINO_ENDPOINT") == "" {
		t.Fatal("TRINO_ENDPOINT must be set for acceptance tests")
	}
	if expected := strings.TrimSpace(os.Getenv("TRINO_EXPECT_VERSION")); expected != "" {
		verifyServerVersion(t, expected)
	}
}

// verifyServerVersion fails when the coordinator does not report the version
// the matrix expects. A mismatch is an environment failure, never a skip.
func verifyServerVersion(t *testing.T, expected string) {
	t.Helper()
	version, err := Executor(t).Version(context.Background())
	if err != nil {
		t.Fatalf("TRINO_EXPECT_VERSION=%s: the version probe failed: %v", expected, err)
	}
	if !versionMatches(expected, version) {
		t.Fatalf("TRINO_EXPECT_VERSION=%s but the server reported %q; check the image tag and the compose project", expected, version)
	}
}

// versionMatches accepts an exact release or a build suffix such as
// "484-SNAPSHOT" or "484.1", and rejects a longer number ("4840") or a
// different release.
func versionMatches(expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if expected == "" || actual == "" {
		return false
	}
	if actual == expected {
		return true
	}
	rest := strings.TrimPrefix(actual, expected)
	return rest != actual && (strings.HasPrefix(rest, "-") || strings.HasPrefix(rest, "."))
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

var ddlPattern = regexp.MustCompile(`(?i)^\s*CREATE\s+(SCHEMA|TABLE|VIEW|ROLE)\s+(?:IF\s+NOT\s+EXISTS\s+)?("(?:[^"]|"")*"|[^\s;]+)`)

// Fixture executes setup statements and returns a cleanup that drops the
// created objects in reverse order. Statements that are not CREATE DDL are
// ignored by cleanup.
func Fixture(t *testing.T, statements ...string) func() {
	t.Helper()
	cleanup, err := fixture(t, Executor(t), statements...)
	if err != nil {
		t.Fatal(err)
	}
	return cleanup
}

func fixture(t *testing.T, executor trino.Executor, statements ...string) (func(), error) {
	t.Helper()
	ctx := context.Background()

	type drop struct {
		kind string
		name string
	}
	drops := make([]drop, 0, len(statements))

	cleanup := func() {
		for i := len(drops) - 1; i >= 0; i-- {
			statement := "DROP " + drops[i].kind + " IF EXISTS " + drops[i].name
			if err := executor.Exec(ctx, statement); err != nil {
				t.Logf("fixture cleanup %q failed: %v", statement, err)
			}
		}
	}

	for _, statement := range statements {
		if err := executor.Exec(ctx, statement); err != nil {
			cleanup()
			return func() {}, fmt.Errorf("fixture statement %q failed: %w", statement, err)
		}
		if match := ddlPattern.FindStringSubmatch(statement); match != nil {
			drops = append(drops, drop{kind: match[1], name: match[2]})
		}
	}

	return cleanup, nil
}
