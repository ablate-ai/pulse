package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func EnsureSelfSignedKeyPair(certFile, keyFile, commonName string) error {
	if certFile == "" || keyFile == "" {
		return fmt.Errorf("cert file and key file are required")
	}

	certInfo, certErr := os.Stat(certFile)
	keyInfo, keyErr := os.Stat(keyFile)
	if certErr == nil && keyErr == nil && !certInfo.IsDir() && !keyInfo.IsDir() {
		return nil
	}
	if (certErr == nil) != (keyErr == nil) {
		return fmt.Errorf("cert file and key file must exist together")
	}
	if certErr != nil && !os.IsNotExist(certErr) {
		return fmt.Errorf("stat cert file: %w", certErr)
	}
	if keyErr != nil && !os.IsNotExist(keyErr) {
		return fmt.Errorf("stat key file: %w", keyErr)
	}

	certPEM, keyPEM, err := GenerateSelfSignedKeyPair(commonName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0o755); err != nil {
		return fmt.Errorf("create cert dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o755); err != nil {
		return fmt.Errorf("create key dir: %w", err)
	}
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		return fmt.Errorf("write cert file: %w", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	return nil
}

func GenerateSelfSignedKeyPair(commonName string) ([]byte, []byte, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generate private key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"pulse"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	return certPEM, keyPEM, nil
}

func ReadCertificatePEM(certFile string) (string, error) {
	content, err := os.ReadFile(certFile)
	if err != nil {
		return "", fmt.Errorf("read cert file: %w", err)
	}
	return string(content), nil
}
