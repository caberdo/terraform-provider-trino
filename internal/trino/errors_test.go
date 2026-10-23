package trino

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestSourceOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"schema not found", &ErrTrino{ErrorName: "SCHEMA_NOT_FOUND"}, KindNotFound},
		{"catalog not found", &ErrTrino{ErrorName: "CATALOG_NOT_FOUND"}, KindNotFound},
		{"role not found", &ErrTrino{ErrorName: "ROLE_NOT_FOUND"}, KindNotFound},
		{"table not found", &ErrTrino{ErrorName: "TABLE_NOT_FOUND"}, KindNotFound},
		{"permission denied", &ErrTrino{ErrorName: "PERMISSION_DENIED"}, KindPermissionDenied},
		{"access denied", &ErrTrino{ErrorName: "ACCESS_DENIED"}, KindPermissionDenied},
		{"not supported", &ErrTrino{ErrorName: "NOT_SUPPORTED", Message: "This connector does not support grants on schemas"}, KindNotSupported},
		{"wrapped not found", wrap(&ErrTrino{ErrorName: "TABLE_NOT_FOUND"}), KindNotFound},
		{"syntax", &ErrTrino{ErrorName: "SYNTAX_ERROR", SqlState: "42000"}, KindSyntax},
		{"conflict", &ErrQueryFailed{StatusCode: 409, Reason: errors.New("already exists")}, KindConflict},
		{"connection url error", &url.Error{Op: "Post", URL: "https://x", Err: errors.New("dial tcp: refused")}, KindConnection},
		{"nil", nil, KindUnknown},
		{"plain error", errors.New("boom"), KindUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SourceOf(tc.err); got != tc.want {
				t.Fatalf("SourceOf(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsErrorName(t *testing.T) {
	err := wrap(&ErrTrino{ErrorName: "SCHEMA_NOT_FOUND"})
	if !IsErrorName(err, "SCHEMA_NOT_FOUND") {
		t.Error("IsErrorName should match a wrapped error name")
	}
	if !IsErrorName(err, "OTHER", "SCHEMA_NOT_FOUND") {
		t.Error("IsErrorName should match any of the given names")
	}
	if IsErrorName(err, "OTHER") {
		t.Error("IsErrorName should not match a different name")
	}
	if IsErrorName(errors.New("boom"), "SCHEMA_NOT_FOUND") {
		t.Error("IsErrorName should not match a non-Trino error")
	}
	if !IsNotFound(err) {
		t.Error("IsNotFound should be true for a *_NOT_FOUND error")
	}
}

func TestDiagnosticFor(t *testing.T) {
	t.Run("dynamic catalog hint", func(t *testing.T) {
		diags := DiagnosticFor("create catalog", "catalog hive", &ErrTrino{
			ErrorName: "NOT_SUPPORTED",
			Message:   "Dynamic catalog management is not enabled",
		})
		if !diags.HasError() {
			t.Fatal("expected an error diagnostic")
		}
		detail := diags.Errors()[0].Detail()
		if !strings.Contains(detail, "catalog.management=dynamic") {
			t.Fatalf("detail %q should name catalog.management=dynamic", detail)
		}
	})

	t.Run("capitalizes the object in a not-found title", func(t *testing.T) {
		diags := DiagnosticFor("read", "schema", &ErrTrino{
			ErrorName: "SCHEMA_NOT_FOUND",
			Message:   "Schema 'sales' does not exist",
		})
		errorDiag := diags.Errors()[0]
		if errorDiag.Summary() != "Schema not found" {
			t.Errorf("summary = %q, want Schema not found", errorDiag.Summary())
		}
		if !strings.HasPrefix(errorDiag.Detail(), "Schema was not found in Trino: ") {
			t.Errorf("detail = %q", errorDiag.Detail())
		}
	})

	t.Run("redacts credentials from a transport error", func(t *testing.T) {
		diags := DiagnosticFor("read", "catalog", &url.Error{
			Op:  "Get",
			URL: "https://alice:s3cret@trino.example/v1/statement",
			Err: errors.New("connection refused"),
		})
		detail := diags.Errors()[0].Detail()
		if strings.Contains(detail, "s3cret") || strings.Contains(detail, "alice") {
			t.Fatalf("detail %q leaks credentials", detail)
		}
		if !strings.Contains(detail, "https://trino.example/v1/statement") {
			t.Errorf("detail = %q, want the sanitized endpoint", detail)
		}
		if diags.Errors()[0].Summary() != "Connection failed" {
			t.Errorf("summary = %q", diags.Errors()[0].Summary())
		}
	})

	t.Run("defaults an empty object", func(t *testing.T) {
		diags := DiagnosticFor("read", "", errors.New("unexpected"))
		errorDiag := diags.Errors()[0]
		if errorDiag.Summary() != "Trino request failed" {
			t.Errorf("summary = %q", errorDiag.Summary())
		}
		if !strings.Contains(errorDiag.Detail(), "the object") {
			t.Errorf("detail = %q", errorDiag.Detail())
		}
	})

	t.Run("never includes statement text", func(t *testing.T) {
		diags := DiagnosticFor("drop schema", `schema "sales"`, &ErrTrino{
			ErrorName: "PERMISSION_DENIED",
			Message:   "Access denied",
		})
		errorDiag := diags.Errors()[0]
		combined := errorDiag.Summary() + " " + errorDiag.Detail()
		if strings.Contains(combined, "DROP SCHEMA") || strings.Contains(combined, "SELECT") {
			t.Fatalf("diagnostic %q leaks statement text", combined)
		}
		if !strings.Contains(combined, `schema "sales"`) {
			t.Fatalf("diagnostic %q should name the object", combined)
		}
	})
}

func wrap(err error) error {
	return fmt.Errorf("wrapped: %w", err)
}
