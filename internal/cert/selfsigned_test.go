package cert

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSelfSignedKeyPair(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "node_cert.pem")
	keyFile := filepath.Join(dir, "node_key.pem")

	if err := EnsureSelfSignedKeyPair(certFile, keyFile, "pulse-node"); err != nil {
		t.Fatalf("EnsureSelfSignedKeyPair() error = %v", err)
	}

	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("ReadFile(cert) error = %v", err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("ReadFile(key) error = %v", err)
	}

	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}
	if len(pair.Certificate) == 0 {
		t.Fatalf("expected certificate chain")
	}

	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	if cert.Subject.CommonName != "pulse-node" {
		t.Fatalf("unexpected common name: %s", cert.Subject.CommonName)
	}
}
