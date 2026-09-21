package app_config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/caarlos0/env/v11"
)

type Vault struct {
	addr string
	c    *http.Client
}

// NewVault creates a read-only KV client. Authentication (and, if needed,
// namespace, retries, mTLS) is provided by the rt transport;
// nil means http.DefaultTransport, see VaultToken.
func NewVault(addr string, rt http.RoundTripper) *Vault {
	return &Vault{addr: strings.TrimSuffix(addr, "/"), c: &http.Client{Transport: rt}}
}

// VaultToken authenticates requests with a static token.
type VaultToken struct {
	Token string
	Next  http.RoundTripper
}

func (t VaultToken) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Vault-Token", t.Token)
	if t.Next != nil {
		return t.Next.RoundTrip(r)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// Read reads secrets by API paths (whatever follows /v1/): "secret/data/app"
// for KV v2, "secret/app" for KV v1. Several paths are merged, the last one
// wins. Nested values are flattened as in DecodeTree.
func (v *Vault) Read(ctx context.Context, paths ...string) (map[string]string, error) {
	kv := map[string]string{}
	for _, p := range paths {
		data, err := v.read(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("vault: read %s: %w", p, err)
		}
		for k, val := range data {
			flatten(k, val, kv)
		}
	}
	return kv, nil
}

// Source returns a Source reading paths when Load runs, so that Vault takes its
// priority from where it stands among the other sources.
func (v *Vault) Source(ctx context.Context, paths ...string) Source {
	return func(l *loader) error {
		kv, err := v.Read(ctx, paths...)
		if err != nil {
			return err
		}
		addKV(l.kv, kv)
		return nil
	}
}

// FromVault reads settings from KV and fills them in by env tags, like FromEnv.
func FromVault(ctx context.Context, vlt *Vault, v any, opts *env.Options, paths ...string) error {
	kv, err := vlt.Read(ctx, paths...)
	if err != nil {
		return err
	}
	return fromKV(kv, v, opts)
}

func (v *Vault) read(ctx context.Context, path string) (map[string]any, error) {
	var data map[string]any
	if err := v.do(ctx, http.MethodGet, path, nil, &data); err != nil {
		return nil, err
	}

	if inner, ok := data["data"].(map[string]any); ok && data["metadata"] != nil {
		return inner, nil // KV v2
	}
	return data, nil
}

// do calls the API path (whatever follows /v1/) and decodes the data field of
// the reply into out. A non-nil body is sent as JSON.
func (v *Vault) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, v.addr+"/v1/"+strings.TrimPrefix(path, "/"), rd)
	if err != nil {
		return err
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := v.c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	var reply struct {
		Data   json.RawMessage `json:"data"`
		Errors []string        `json:"errors"`
	}
	derr := json.NewDecoder(resp.Body).Decode(&reply)

	if resp.StatusCode != http.StatusOK {
		if len(reply.Errors) > 0 {
			return fmt.Errorf("%s: %s", resp.Status, strings.Join(reply.Errors, "; "))
		}
		return fmt.Errorf("%s", resp.Status)
	}
	if derr != nil {
		return derr
	}

	dec := json.NewDecoder(bytes.NewReader(reply.Data))
	dec.UseNumber() // otherwise 1000000 becomes 1e+06
	return dec.Decode(out)
}
