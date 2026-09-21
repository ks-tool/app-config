package appconfig

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"strings"
)

// Cert is what the PKI engine returns: the leaf, its key and the issuer chain.
type Cert struct {
	Certificate  string   `json:"certificate"`
	PrivateKey   string   `json:"private_key"`
	IssuingCA    string   `json:"issuing_ca"`
	CAChain      []string `json:"ca_chain"`
	SerialNumber string   `json:"serial_number"`
	Expiration   int64    `json:"expiration"`
}

// Cert asks the PKI engine for a certificate. The path is the API path after
// /v1/ — "pki/issue/<role>" for a fresh key pair, "pki/sign/<role>" to sign a
// CSR — and req is the request body: common_name, alt_names, ip_sans, ttl and
// so on. With a nil req the path is read instead, as in "pki/cert/ca".
func (v *Vault) Cert(ctx context.Context, path string, req map[string]any) (*Cert, error) {
	method := http.MethodPost
	if req == nil {
		method = http.MethodGet
	}

	crt := &Cert{}
	if err := v.do(ctx, method, path, req, crt); err != nil {
		return nil, err
	}
	return crt, nil
}

// PEM returns the certificate followed by its issuer chain.
func (c *Cert) PEM() string {
	return join(append([]string{c.Certificate}, c.CAChain...))
}

// TLS returns the certificate and its key as a TLS key pair.
func (c *Cert) TLS() (tls.Certificate, error) {
	return tls.X509KeyPair([]byte(c.PEM()), []byte(c.PrivateKey))
}

// Pool returns the issuer chain as a pool for verifying peers. A certificate
// read from pki/cert/ca is its own issuer, so it is trusted as it is.
func (c *Cert) Pool() (*x509.CertPool, error) {
	chain := append([]string{c.IssuingCA}, c.CAChain...)
	if c.IssuingCA == "" && len(c.CAChain) == 0 {
		chain = []string{c.Certificate}
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(join(chain))) {
		return nil, errors.New("popit: no CA certificate to trust")
	}
	return pool, nil
}

func join(pem []string) string {
	var b strings.Builder
	for _, p := range pem {
		if p = strings.TrimSpace(p); p != "" {
			b.WriteString(p)
			b.WriteString("\n")
		}
	}
	return b.String()
}
