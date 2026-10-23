package trino

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	drivertrino "github.com/trinodb/trino-go-client/trino"
)

// ErrTrino is the server error payload the driver decodes. It is re-exported so
// the rest of the provider can classify errors without importing the driver.
type ErrTrino = drivertrino.ErrTrino

// ErrQueryFailed is the driver's wrapper around a non-2xx response. Its Reason
// may hold an ErrTrino payload.
type ErrQueryFailed = drivertrino.ErrQueryFailed

// ErrorKind classifies a failure so resources can choose the right diagnostic
// and decide whether removal from state is safe.
type ErrorKind int

const (
	KindUnknown ErrorKind = iota
	KindNotFound
	KindPermissionDenied
	KindNotSupported
	KindSyntax
	KindConnection
	KindConflict
)

// String renders the kind for logs and diagnostics.
func (k ErrorKind) String() string {
	switch k {
	case KindNotFound:
		return "not found"
	case KindPermissionDenied:
		return "permission denied"
	case KindNotSupported:
		return "not supported"
	case KindSyntax:
		return "syntax"
	case KindConnection:
		return "connection"
	case KindConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

// SourceOf classifies err by the Trino error name, SQL state, HTTP status and
// transport error type. Wrapped errors are unwrapped with errors.As.
func SourceOf(err error) ErrorKind {
	if err == nil {
		return KindUnknown
	}

	var trinoErr *ErrTrino
	if errors.As(err, &trinoErr) {
		name := strings.ToUpper(trinoErr.ErrorName)
		switch {
		case strings.Contains(name, "_NOT_FOUND"):
			return KindNotFound
		case name == "PERMISSION_DENIED", name == "ACCESS_DENIED":
			return KindPermissionDenied
		case name == "NOT_SUPPORTED":
			return KindNotSupported
		case strings.HasPrefix(trinoErr.SqlState, "42"), name == "SYNTAX_ERROR":
			return KindSyntax
		}
	}

	var queryFailed *ErrQueryFailed
	if errors.As(err, &queryFailed) {
		switch queryFailed.StatusCode {
		case http.StatusConflict:
			return KindConflict
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return KindConnection
		}
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return KindConnection
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return KindConnection
	}

	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return KindConnection
	}
	var authorityErr x509.UnknownAuthorityError
	if errors.As(err, &authorityErr) {
		return KindConnection
	}

	return KindUnknown
}

// IsErrorName reports whether err is a Trino error with one of names. The
// comparison is case-insensitive because the server upper-cases its names.
func IsErrorName(err error, names ...string) bool {
	var trinoErr *ErrTrino
	if !errors.As(err, &trinoErr) {
		return false
	}
	for _, name := range names {
		if strings.EqualFold(trinoErr.ErrorName, name) {
			return true
		}
	}
	return false
}

// IsNotFound reports whether err maps to KindNotFound.
func IsNotFound(err error) bool {
	return SourceOf(err) == KindNotFound
}

// DiagnosticFor builds the single error diagnostic for a failed operation. It
// names the operation and object, and the server message when there is one. It
// never includes the statement text, so SQL literals cannot leak into
// Terraform output.
func DiagnosticFor(op, object string, err error) diag.Diagnostics {
	var diags diag.Diagnostics
	kind := SourceOf(err)
	message := errorMessage(err)
	diags.AddError(diagnosticTitle(kind, object), diagnosticDetail(kind, op, object, message))
	return diags
}

func diagnosticTitle(kind ErrorKind, object string) string {
	if object == "" {
		object = "object"
	}
	switch kind {
	case KindNotFound:
		return capitalizeFirst(object) + " not found"
	case KindPermissionDenied:
		return "Permission denied"
	case KindNotSupported:
		return "Operation not supported"
	case KindSyntax:
		return "Invalid statement"
	case KindConnection:
		return "Connection failed"
	case KindConflict:
		return "Conflict"
	default:
		return "Trino request failed"
	}
}

func diagnosticDetail(kind ErrorKind, op, object, message string) string {
	if object == "" {
		object = "the object"
	}
	switch kind {
	case KindNotFound:
		return fmt.Sprintf("%s was not found in Trino: %s", capitalizeFirst(object), message)
	case KindPermissionDenied:
		return fmt.Sprintf("The provider user is not allowed to %s %s: %s", op, object, message)
	case KindNotSupported:
		if isDynamicCatalogMessage(message) {
			return "Dynamic catalog management is not enabled. Set catalog.management=dynamic in the Trino coordinator configuration and restart it: " + message
		}
		return fmt.Sprintf("The connector does not support %s on %s: %s", op, object, message)
	case KindSyntax:
		return fmt.Sprintf("Trino rejected the request to %s %s: %s", op, object, message)
	case KindConnection:
		return fmt.Sprintf("Could not reach Trino while trying to %s %s: %s", op, object, message)
	case KindConflict:
		return fmt.Sprintf("Trino reported a conflict while trying to %s %s: %s", op, object, message)
	default:
		return fmt.Sprintf("The request to %s %s failed: %s", op, object, message)
	}
}

func isDynamicCatalogMessage(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "dynamic") &&
		(strings.Contains(lower, "catalog") || strings.Contains(lower, "management"))
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// errorMessage extracts the server message without credentials or statement
// text. A URL error would otherwise echo the configured userinfo.
func errorMessage(err error) string {
	if err == nil {
		return "unknown error"
	}
	var trinoErr *ErrTrino
	if errors.As(err, &trinoErr) && trinoErr.Message != "" {
		return trinoErr.Message
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Sprintf("%s %s: %v", urlErr.Op, redactURL(urlErr.URL), urlErr.Err)
	}
	return err.Error()
}

func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}
