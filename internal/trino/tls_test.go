package trino_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

func TestTLSClientRejectsUnknownCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := trino.NewHTTPClient(trino.TLSOptions{})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected the self-signed certificate to be rejected")
	}
}

func TestTLSClientAcceptsConfiguredCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	for name, opts := range map[string]trino.TLSOptions{
		"inline": {CAPEM: string(caPEM)},
		"file":   {CAFile: caFile},
	} {
		t.Run(name, func(t *testing.T) {
			client, err := trino.NewHTTPClient(opts)
			if err != nil {
				t.Fatalf("NewHTTPClient: %v", err)
			}
			body, err := getBody(client, server.URL)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			if body != "ok" {
				t.Errorf("body = %q", body)
			}
		})
	}
}

func TestTLSClientPresentsClientCert(t *testing.T) {
	pki := newTestPKI(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "no client certificate", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, r.TLS.PeerCertificates[0].Subject.CommonName)
	}))
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{pki.serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pki.caPool,
	}
	server.StartTLS()
	defer server.Close()

	certFile := pki.write(t, "client.pem", pki.clientCertPEM)
	keyFile := pki.write(t, "client-key.pem", pki.clientKeyPEM)

	for name, opts := range map[string]trino.TLSOptions{
		"inline": {CAPEM: string(pki.caPEM), ClientCertPEM: string(pki.clientCertPEM), ClientKeyPEM: string(pki.clientKeyPEM)},
		"file":   {CAPEM: string(pki.caPEM), ClientCertFile: certFile, ClientKeyFile: keyFile},
	} {
		t.Run(name, func(t *testing.T) {
			client, err := trino.NewHTTPClient(opts)
			if err != nil {
				t.Fatalf("NewHTTPClient: %v", err)
			}
			body, err := getBody(client, server.URL)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			if body != "test-client" {
				t.Errorf("server saw client %q, want test-client", body)
			}
		})
	}
}

func TestTLSHeaderRoundTripper(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Header.Get("X-Custom")+"/"+r.Header.Get("X-Existing"))
	}))
	defer server.Close()

	client, err := trino.NewHTTPClient(trino.TLSOptions{
		Headers: map[string]string{"X-Custom": "value", "X-Existing": "overridden"},
	})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Existing", "original")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "value/overridden" {
		t.Errorf("handler saw %q, want value/overridden", body)
	}
}

func TestTLSClientInsecureSkipVerify(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	client, err := trino.NewHTTPClient(trino.TLSOptions{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	body, err := getBody(client, server.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if body != "ok" {
		t.Errorf("body = %q", body)
	}
}

func TestNewHTTPClientRejectsBadInput(t *testing.T) {
	for name, opts := range map[string]trino.TLSOptions{
		"bad ca pem":              {CAPEM: "not a certificate"},
		"missing ca file":         {CAFile: filepath.Join(t.TempDir(), "absent.pem")},
		"client cert no key":      {ClientCertPEM: "cert"},
		"client key no cert":      {ClientKeyPEM: "key"},
		"bad client cert pair":    {ClientCertPEM: "cert", ClientKeyPEM: "key"},
		"missing client cert":     {ClientCertFile: filepath.Join(t.TempDir(), "absent.pem"), ClientKeyPEM: "key"},
		"missing client key file": {ClientCertPEM: "cert", ClientKeyFile: filepath.Join(t.TempDir(), "absent.pem")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := trino.NewHTTPClient(opts); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func getBody(client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

type testPKI struct {
	caPEM         []byte
	caPool        *x509.CertPool
	serverCert    tls.Certificate
	clientCertPEM []byte
	clientKeyPEM  []byte
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	serverCertPEM, serverKeyPEM := newLeaf(t, caCert, caKey, "127.0.0.1", nil, []net.IP{net.ParseIP("127.0.0.1")}, x509.ExtKeyUsageServerAuth)
	serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("server key pair: %v", err)
	}
	clientCertPEM, clientKeyPEM := newLeaf(t, caCert, caKey, "test-client", nil, nil, x509.ExtKeyUsageClientAuth)

	return testPKI{
		caPEM:         caPEM,
		caPool:        caPool,
		serverCert:    serverCert,
		clientCertPEM: clientCertPEM,
		clientKeyPEM:  clientKeyPEM,
	}
}

func newLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, dns []string, ips []net.IP, usage x509.ExtKeyUsage) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     dns,
		IPAddresses:  ips,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func (p testPKI) write(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
