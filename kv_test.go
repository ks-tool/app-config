package app_config

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/caarlos0/env/v11"
)

type config struct {
	Host    string        `env:"HOST"`
	Port    int           `env:"PORT"`
	Timeout time.Duration `env:"TIMEOUT"`
	Ratio   float64       `env:"RATIO"`
	Tags    []string      `env:"TAGS"`
	Debug   bool          `env:"DEBUG"`
	Level   string        `env:"LEVEL" envDefault:"info"`
	DB      struct {
		Pass string `env:"PASS"`
	} `envPrefix:"DB_"`
}

type node struct {
	Addr string `env:"ADDR"`
	Port int    `env:"PORT"`
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestFlatten(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want map[string]string
	}{
		{"scalars", map[string]any{"a": "x", "b": true, "c": 42}, map[string]string{"a": "x", "b": "true", "c": "42"}},
		{"nested", map[string]any{"db": map[string]any{"host": "h", "opt": map[string]any{"tls": true}}},
			map[string]string{"db_host": "h", "db_opt_tls": "true"}},
		{"yaml_v2_map", map[string]any{"db": map[any]any{1: "one"}}, map[string]string{"db_1": "one"}},
		{"slice", map[string]any{"tags": []any{"a", "b", 3}}, map[string]string{"tags": "a,b,3"}},
		{"slice_of_maps", map[string]any{"nodes": []any{
			map[string]any{"addr": "a1"}, map[string]any{"addr": "a2"}}},
			map[string]string{"nodes_0_addr": "a1", "nodes_1_addr": "a2"}},
		{"nil_and_empty", map[string]any{"a": nil, "b": []any{}}, map[string]string{"a": ""}},
		{"floats", map[string]any{"a": 1000000.0, "b": 1.5, "c": float32(2.5), "d": json.Number("1000000")},
			map[string]string{"a": "1000000", "b": "1.5", "c": "2.5", "d": "1000000"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := map[string]string{}
			flatten("", tt.in, got)
			if !maps.Equal(got, tt.want) {
				t.Errorf("flatten() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFromKVUpperCaseKeys(t *testing.T) {
	var c config
	must(t, fromKV(map[string]string{"host": "h", "PORT": "80", "db_pass": "p"}, &c, nil))

	if c.Host != "h" || c.Port != 80 || c.DB.Pass != "p" {
		t.Errorf("got %+v", c)
	}
}

// A source replaces the process environment entirely: os.Environ is not mixed in.
func TestFromKVIgnoresProcessEnv(t *testing.T) {
	t.Setenv("HOST", "from-process-env")

	var c config
	must(t, fromKV(map[string]string{"PORT": "80"}, &c, nil))

	if c.Host != "" {
		t.Errorf("Host = %q, want empty", c.Host)
	}
}

func TestFromKVRespectsOptions(t *testing.T) {
	var c config
	must(t, fromKV(map[string]string{"APP_HOST": "h"}, &c, &env.Options{Prefix: "APP_"}))
	if c.Host != "h" {
		t.Errorf("Host = %q", c.Host)
	}

	err := fromKV(map[string]string{}, &struct {
		Host string `env:"HOST,required"`
	}{}, nil)
	if err == nil {
		t.Error("want error for missing required key")
	}
}

func TestFromKVDefaults(t *testing.T) {
	var c config
	must(t, fromKV(map[string]string{"LEVEL": "debug"}, &c, nil))
	if c.Level != "debug" {
		t.Errorf("Level = %q", c.Level)
	}

	c = config{}
	must(t, fromKV(map[string]string{}, &c, nil))
	if c.Level != "info" {
		t.Errorf("Level = %q, want envDefault", c.Level)
	}
}

func TestDecodeTree(t *testing.T) {
	dec := DecodeTree(json.Unmarshal)
	got, err := dec([]byte(`{"host":"h","db":{"pass":"p"},"tags":["a","b"]}`))
	must(t, err)

	want := map[string]string{"host": "h", "db_pass": "p", "tags": "a,b"}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	if _, err := dec([]byte(`{`)); err == nil {
		t.Error("want error for broken input")
	}
}

func TestDecodeTreeSliceOfStructs(t *testing.T) {
	kv, err := DecodeTree(json.Unmarshal)([]byte(`{"nodes":[{"addr":"a1","port":1},{"addr":"a2","port":2}]}`))
	must(t, err)

	var c struct {
		Nodes []node `envPrefix:"NODES_"`
	}
	must(t, fromKV(kv, &c, nil))

	want := []node{{Addr: "a1", Port: 1}, {Addr: "a2", Port: 2}}
	if !slices.Equal(c.Nodes, want) {
		t.Errorf("Nodes = %v, want %v", c.Nodes, want)
	}
}
