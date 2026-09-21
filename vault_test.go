package app_config

import (
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caarlos0/env/v11"
)

// vaultServer starts a test server that is shut down together with the test.
func vaultServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func json200(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }
}

func TestVaultKV2(t *testing.T) {
	var req *http.Request
	srv := vaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		req = r
		_, _ = io.WriteString(w, `{"data":{"data":{"host":"vault.local","port":9000,"tags":["a","b"],
			"db":{"pass":"top"}},"metadata":{"version":1}}}`)
	})

	var c config
	v := NewVault(srv.URL+"/", VaultToken{Token: "tkn"}) // the trailing slash must not double
	must(t, FromVault(context.Background(), v, &c, nil, "/secret/data/app"))

	if req.Method != http.MethodGet || req.URL.Path != "/v1/secret/data/app" {
		t.Errorf("request = %s %s", req.Method, req.URL.Path)
	}
	if got := req.Header.Get("X-Vault-Token"); got != "tkn" {
		t.Errorf("X-Vault-Token = %q", got)
	}
	if c.Host != "vault.local" || c.Port != 9000 || c.DB.Pass != "top" || len(c.Tags) != 2 {
		t.Errorf("got %+v", c)
	}
}

func TestVaultKV1(t *testing.T) {
	srv := vaultServer(t, json200(`{"data":{"host":"h","port":80}}`))

	var c config
	must(t, FromVault(context.Background(), NewVault(srv.URL, nil), &c, nil, "secret/app"))

	if c.Host != "h" || c.Port != 80 {
		t.Errorf("got %+v", c)
	}
}

// In KV v1 "data" is an ordinary secret key and must not be unwrapped;
// what marks v2 is the metadata sibling.
func TestVaultKV1KeyNamedData(t *testing.T) {
	srv := vaultServer(t, json200(`{"data":{"data":{"nested":"v"},"other":"o"}}`))

	got, err := NewVault(srv.URL, nil).Read(context.Background(), "secret/app")
	must(t, err)

	want := map[string]string{"data_nested": "v", "other": "o"}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestVaultMergesPaths(t *testing.T) {
	var paths []string
	srv := vaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/common") {
			_, _ = io.WriteString(w, `{"data":{"host":"common","port":1}}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"port":2}}`)
	})

	var c config
	must(t, FromVault(context.Background(), NewVault(srv.URL, nil), &c, nil,
		"kv/common", "kv/app"))

	if len(paths) != 2 {
		t.Errorf("paths = %v", paths)
	}
	if c.Host != "common" || c.Port != 2 { // the last path wins
		t.Errorf("got %+v", c)
	}
}

func TestVaultReadNoPaths(t *testing.T) {
	srv := vaultServer(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })

	got, err := NewVault(srv.URL, nil).Read(context.Background())
	must(t, err)
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestVaultErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"forbidden", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"errors":["permission denied"]}`)
		}, "permission denied"},
		{"not_found_empty_errors", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"errors":[]}`)
		}, "404"},
		{"non_json_body", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "<html>gateway</html>")
		}, "502"},
		{"broken_json_on_200", json200(`{"data":`), "unexpected EOF"},
		{"empty_body_on_200", func(http.ResponseWriter, *http.Request) {}, "EOF"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := vaultServer(t, tt.handler)

			_, err := NewVault(srv.URL, nil).Read(context.Background(), "secret/app")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want mention of %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), "secret/app") {
				t.Errorf("err = %v, want mention of the path", err)
			}
		})
	}
}

func TestVaultCanceledContext(t *testing.T) {
	srv := vaultServer(t, json200(`{"data":{"host":"h"}}`))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := NewVault(srv.URL, nil).Read(ctx, "secret/app"); err == nil {
		t.Error("want error for canceled context")
	}
}

type recordRT struct {
	got  *http.Request
	resp string
}

func (r *recordRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.got = req
	body := r.resp
	if body == "" {
		body = `{"data":{"host":"from-transport"}}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}, nil
}

// Authentication can be replaced wholesale: the client only goes through the given transport.
func TestVaultCustomRoundTripper(t *testing.T) {
	rt := &recordRT{}

	var c config
	must(t, FromVault(context.Background(), NewVault("https://vault.invalid", rt), &c,
		nil, "secret/app"))

	if rt.got.URL.String() != "https://vault.invalid/v1/secret/app" {
		t.Errorf("url = %s", rt.got.URL)
	}
	if c.Host != "from-transport" {
		t.Errorf("Host = %q", c.Host)
	}
}

func TestVaultTokenUsesNext(t *testing.T) {
	rt := &recordRT{}
	req, err := http.NewRequest(http.MethodGet, "https://vault.invalid/v1/secret/app", nil)
	must(t, err)

	resp, err := VaultToken{Token: "tkn", Next: rt}.RoundTrip(req)
	must(t, err)
	defer resp.Body.Close()

	if got := rt.got.Header.Get("X-Vault-Token"); got != "tkn" {
		t.Errorf("X-Vault-Token = %q", got)
	}
	if got := req.Header.Get("X-Vault-Token"); got != "" {
		t.Errorf("the original request must not be mutated, got %q", got)
	}
}

func TestFromVaultReadError(t *testing.T) {
	srv := vaultServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"errors":["permission denied"]}`)
	})

	err := FromVault(context.Background(), NewVault(srv.URL, nil), &config{}, nil, "secret/app")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("err = %v", err)
	}
}

func TestVaultInvalidAddr(t *testing.T) {
	_, err := NewVault("http://vault\x7f.invalid", nil).Read(context.Background(), "secret/app")
	if err == nil || !strings.Contains(err.Error(), "secret/app") {
		t.Errorf("err = %v, want request build error", err)
	}
}

func TestFromVaultWithOptions(t *testing.T) {
	srv := vaultServer(t, json200(`{"data":{"app_host":"h"}}`))

	var c config
	must(t, FromVault(context.Background(), NewVault(srv.URL, nil), &c,
		&env.Options{Prefix: "APP_"}, "secret/app"))

	if c.Host != "h" {
		t.Errorf("Host = %q", c.Host)
	}
}

// The given opts must not be mutated: Environment is replaced inside the call only.
func TestFromVaultDoesNotMutateOptions(t *testing.T) {
	srv := vaultServer(t, json200(`{"data":{"host":"h"}}`))
	opts := &env.Options{}

	must(t, FromVault(context.Background(), NewVault(srv.URL, nil), &config{}, opts, "secret/app"))

	if opts.Environment != nil {
		t.Errorf("Environment = %v, want nil", opts.Environment)
	}
}

func TestVaultSourceInLoad(t *testing.T) {
	var reads int
	srv := vaultServer(t, func(w http.ResponseWriter, _ *http.Request) {
		reads++
		_, _ = io.WriteString(w, `{"data":{"data":{"host":"from-vault","token":"t",
			"level":"debug"},"metadata":{"version":1}}}`)
	})

	file := write(t, "app.env", "HOST=from-file\nPORT=1\n")
	src := NewVault(srv.URL, nil).Source(context.Background(), "secret/data/app")
	if reads != 0 {
		t.Error("the secret must be read by Load, not before it")
	}

	var c loadCfg
	must(t, Load(&c, File(file), src))

	if c.Host != "from-vault" || c.Port != 1 || c.Level != "debug" || reads != 1 {
		t.Errorf("got %+v, reads = %d", c, reads)
	}
}

// Priority follows the position: a later source overrides Vault.
func TestVaultSourcePriority(t *testing.T) {
	srv := vaultServer(t, json200(`{"data":{"host":"from-vault","token":"t"}}`))
	file := write(t, "app.env", "HOST=from-file\n")

	var c loadCfg
	must(t, Load(&c,
		NewVault(srv.URL, nil).Source(context.Background(), "secret/app"),
		File(file),
	))

	if c.Host != "from-file" {
		t.Errorf("Host = %q, want the later source to win", c.Host)
	}
}

func TestVaultSourceError(t *testing.T) {
	srv := vaultServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"errors":["permission denied"]}`)
	})

	c := loadCfg{Host: "untouched"}
	err := Load(&c, NewVault(srv.URL, nil).Source(context.Background(), "secret/app"))

	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v", err)
	}
	if c.Host != "untouched" {
		t.Errorf("Host = %q, want the config left alone", c.Host)
	}
}
