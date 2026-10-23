package trino

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"maps"
	"net/http"
	"os"
)

// TLSOptions is the TLS and header material from the provider configuration.
// PEM values and file paths are interchangeable; the PEM value wins when both
// are set.
type TLSOptions struct {
	CAFile             string
	CAPEM              string
	ClientCertFile     string
	ClientCertPEM      string
	ClientKeyFile      string
	ClientKeyPEM       string
	InsecureSkipVerify bool
	Headers            map[string]string
}

// NewHTTPClient builds the client the driver uses for every request. It always
// returns a fresh transport so a provider instance cannot leak state into
// another. A nil error means the material parsed; it does not dial.
func NewHTTPClient(opts TLSOptions) (*http.Client, error) {
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if opts.CAPEM != "" {
		if !rootCAs.AppendCertsFromPEM([]byte(opts.CAPEM)) {
			return nil, fmt.Errorf("ca_cert: no certificates found")
		}
	}
	if opts.CAFile != "" {
		pemBytes, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, fmt.Errorf("ca_cert_file: %w", err)
		}
		if !rootCAs.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("ca_cert_file %q: no certificates found", opts.CAFile)
		}
	}

	config := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		RootCAs:            rootCAs,
		InsecureSkipVerify: opts.InsecureSkipVerify,
	}

	certPEM, err := readMaterial(opts.ClientCertPEM, opts.ClientCertFile, "client_cert")
	if err != nil {
		return nil, err
	}
	keyPEM, err := readMaterial(opts.ClientKeyPEM, opts.ClientKeyFile, "client_key")
	if err != nil {
		return nil, err
	}
	switch {
	case certPEM != "" && keyPEM == "":
		return nil, fmt.Errorf("client_cert requires client_key")
	case certPEM == "" && keyPEM != "":
		return nil, fmt.Errorf("client_key requires client_cert")
	case certPEM != "" && keyPEM != "":
		certificate, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			return nil, fmt.Errorf("client certificate: %w", err)
		}
		config.Certificates = []tls.Certificate{certificate}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = config

	var roundTripper http.RoundTripper = transport
	if len(opts.Headers) > 0 {
		roundTripper = &headerRoundTripper{base: transport, headers: maps.Clone(opts.Headers)}
	}
	return &http.Client{Transport: roundTripper}, nil
}

func readMaterial(inline, file, name string) (string, error) {
	if inline != "" {
		return inline, nil
	}
	if file == "" {
		return "", nil
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("%s_file: %w", name, err)
	}
	return string(content), nil
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	for name, value := range h.headers {
		clone.Header.Set(name, value)
	}
	return h.base.RoundTrip(clone)
}
