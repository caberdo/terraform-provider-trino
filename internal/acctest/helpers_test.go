package acctest

import (
	"testing"
)

func TestProtoV6ProviderFactoriesBuildsTheProvider(t *testing.T) {
	factories := ProtoV6ProviderFactories()
	factory, ok := factories["trino"]
	if !ok {
		t.Fatalf("no factory for trino: %v", factories)
	}
	if _, err := factory(); err != nil {
		t.Fatalf("factory failed: %v", err)
	}
}

func TestPreCheckSkipsWhenAcceptanceIsDisabled(t *testing.T) {
	var child *testing.T
	t.Run("without TF_ACC", func(t *testing.T) {
		child = t
		t.Setenv("TF_ACC", "")
		t.Setenv("TRINO_ENDPOINT", "")
		PreCheck(t)
	})
	if child == nil || !child.Skipped() {
		t.Fatal("PreCheck should skip when TF_ACC is unset")
	}
}

func TestPreCheckPassesWithEndpoint(t *testing.T) {
	var child *testing.T
	if !t.Run("configured", func(t *testing.T) {
		child = t
		t.Setenv("TF_ACC", "1")
		t.Setenv("TRINO_ENDPOINT", "http://127.0.0.1:1")
		// CI exports TRINO_EXPECT_VERSION job-wide for the acceptance
		// container, and PreCheck probes the server when it is set. Clear it
		// so this test covers the endpoint guard alone.
		t.Setenv("TRINO_EXPECT_VERSION", "")
		PreCheck(t)
	}) {
		t.Fatal("PreCheck should pass when TF_ACC and TRINO_ENDPOINT are set")
	}
	if child == nil || child.Skipped() || child.Failed() {
		t.Fatal("PreCheck reported a skip or failure for a configured environment")
	}
}

// TestExecutorBuildsAClientWithoutDialling covers the fixture helper: Open does
// not dial, so no coordinator is needed to construct and close the client.
func TestExecutorBuildsAClientWithoutDialling(t *testing.T) {
	t.Setenv("TRINO_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("TRINO_AUTH", "none")
	t.Setenv("TRINO_USER", "admin")

	executor := Executor(t)
	if executor == nil {
		t.Fatal("Executor returned nil")
	}
}

// TestFixtureWithNoStatements proves the public Fixture wrapper builds an
// executor and returns a no-op cleanup when there is nothing to create.
func TestFixtureWithNoStatements(t *testing.T) {
	t.Setenv("TRINO_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("TRINO_AUTH", "none")
	t.Setenv("TRINO_USER", "admin")

	cleanup := Fixture(t)
	if cleanup == nil {
		t.Fatal("Fixture returned a nil cleanup")
	}
	cleanup()
}
