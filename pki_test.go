package appconfig

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// selfSigned returns a usable certificate/key PEM pair to stand in for the one
// the PKI engine would issue.
func selfSigned(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)

	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "app.internal"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"app.internal"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	must(t, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	must(t, err)

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func TestVaultCertIssue(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)

	var gotMethod, gotPath, gotType string
	var gotBody map[string]any
	srv := vaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotType = r.Header.Get("Content-Type")
		must(t, json.NewDecoder(r.Body).Decode(&gotBody))

		must(t, json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"certificate":   certPEM,
			"private_key":   keyPEM,
			"issuing_ca":    certPEM,
			"ca_chain":      []string{certPEM},
			"serial_number": "3a:1b",
			"expiration":    1893456000,
		}}))
	})

	crt, err := NewVault(srv.URL, VaultToken{Token: "tkn"}).Cert(context.Background(),
		"pki/issue/web", map[string]any{"common_name": "app.internal", "ttl": "24h"})
	must(t, err)

	if gotMethod != http.MethodPost || gotPath != "/v1/pki/issue/web" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q", gotType)
	}
	if gotBody["common_name"] != "app.internal" || gotBody["ttl"] != "24h" {
		t.Errorf("body = %v", gotBody)
	}
	if crt.SerialNumber != "3a:1b" || crt.Expiration != 1893456000 {
		t.Errorf("got %+v", crt)
	}

	pair, err := crt.TLS()
	must(t, err)
	if len(pair.Certificate) != 2 { // leaf plus the chain
		t.Errorf("chain length = %d", len(pair.Certificate))
	}
	if _, err := crt.Pool(); err != nil {
		t.Errorf("Pool() = %v", err)
	}
}

// A nil request body reads the path instead of issuing anything.
func TestVaultCertReadCA(t *testing.T) {
	certPEM, _ := selfSigned(t)

	var gotMethod string
	srv := vaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		must(t, json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"certificate": certPEM},
		}))
	})

	crt, err := NewVault(srv.URL, nil).Cert(context.Background(), "pki/cert/ca", nil)
	must(t, err)

	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	if _, err := crt.Pool(); err != nil { // its own issuer
		t.Errorf("Pool() = %v", err)
	}
}

func TestVaultCertErrors(t *testing.T) {
	t.Run("vault_error", func(t *testing.T) {
		srv := vaultServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"errors":["permission denied"]}`)
		})

		_, err := NewVault(srv.URL, nil).Cert(context.Background(), "pki/issue/web",
			map[string]any{"common_name": "app.internal"})
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("unencodable_body", func(t *testing.T) {
		_, err := NewVault("https://vault.invalid", nil).Cert(context.Background(),
			"pki/issue/web", map[string]any{"bad": func() {}})
		if err == nil || !strings.Contains(err.Error(), "json") {
			t.Errorf("err = %v, want a marshalling error", err)
		}
	})
}

func TestCertPEMAndPool(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)

	t.Run("pem_skips_blanks", func(t *testing.T) {
		crt := &Cert{Certificate: certPEM, CAChain: []string{"", "  "}}
		if got := crt.PEM(); strings.Count(got, "BEGIN CERTIFICATE") != 1 {
			t.Errorf("PEM() = %q", got)
		}
	})

	t.Run("pool_without_ca", func(t *testing.T) {
		if _, err := (&Cert{}).Pool(); err == nil {
			t.Error("want error when there is nothing to trust")
		}
	})

	t.Run("tls_with_broken_key", func(t *testing.T) {
		if _, err := (&Cert{Certificate: certPEM, PrivateKey: "not a key"}).TLS(); err == nil {
			t.Error("want error for a broken key")
		}
	})

	t.Run("tls_pair_is_usable", func(t *testing.T) {
		crt := &Cert{Certificate: certPEM, PrivateKey: keyPEM, IssuingCA: certPEM}
		pair, err := crt.TLS()
		must(t, err)

		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		must(t, err)
		if leaf.Subject.CommonName != "app.internal" {
			t.Errorf("CommonName = %q", leaf.Subject.CommonName)
		}
	})
}

// The issued pair and the pool it carries must work together over real TLS.
func TestCertServesTLS(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)
	crt := &Cert{Certificate: certPEM, PrivateKey: keyPEM, IssuingCA: certPEM}

	pair, err := crt.TLS()
	must(t, err)
	pool, err := crt.Pool()
	must(t, err)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	srv.StartTLS()
	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}
	resp, err := client.Get(srv.URL)
	must(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	must(t, err)
	if string(body) != "ok" {
		t.Errorf("body = %q", body)
	}
}
