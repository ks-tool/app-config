package app_config

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/caarlos0/env/v11"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	must(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestDotenv(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"plain", "HOST=example.com\nPORT=8080\n", map[string]string{"HOST": "example.com", "PORT": "8080"}},
		{"comments_and_blanks", "# comment\n\n  # indented\nHOST=h\n", map[string]string{"HOST": "h"}},
		{"export_prefix", "export HOST=h\n", map[string]string{"HOST": "h"}},
		{"spaces_trimmed", "  HOST  =  a b  \n", map[string]string{"HOST": "a b"}},
		{"empty_value", "HOST=\n", map[string]string{"HOST": ""}},
		{"equals_in_value", "DSN=postgres://u:p@h/db?x=1\n", map[string]string{"DSN": "postgres://u:p@h/db?x=1"}},
		{"double_quotes", `HOST="a b"` + "\n", map[string]string{"HOST": "a b"}},
		{"escapes_in_double_quotes", `HOST="a\nb"` + "\n", map[string]string{"HOST": "a\nb"}},
		{"single_quotes", "HOST='a b'\n", map[string]string{"HOST": "a b"}},
		{"crlf", "HOST=h\r\nPORT=80\r\n", map[string]string{"HOST": "h", "PORT": "80"}},
		{"no_trailing_newline", "HOST=h", map[string]string{"HOST": "h"}},
		{"empty_file", "", map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dotenv([]byte(tt.in))
			must(t, err)
			if !maps.Equal(got, tt.want) {
				t.Errorf("dotenv() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDotenvInvalidLine(t *testing.T) {
	_, err := dotenv([]byte("HOST=h\nBROKEN\n"))
	if err == nil || !strings.Contains(err.Error(), "BROKEN") {
		t.Errorf("err = %v, want mention of the bad line", err)
	}
}

func TestFromFileDotenv(t *testing.T) {
	path := write(t, "app.env", `# config
export HOST=example.com
PORT = 8080
TIMEOUT=1500ms
RATIO=0.25
TAGS=a,b,c
DEBUG=true
DB_PASS='s3 cret'
`)

	var c config
	must(t, FromFile(path, &c, nil))

	want := config{Host: "example.com", Port: 8080, Timeout: 1500 * time.Millisecond,
		Ratio: 0.25, Tags: []string{"a", "b", "c"}, Debug: true, Level: "info"}
	want.DB.Pass = "s3 cret"
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got %+v, want %+v", c, want)
	}
}

func TestFromFileJSON(t *testing.T) {
	path := write(t, "app.json", `{
		"host": "h",
		"port": 1000000,
		"timeout": "2s",
		"ratio": 0.5,
		"tags": ["a", "b"],
		"debug": true,
		"db": {"pass": "p"}
	}`)

	var c config
	must(t, FromFile(path, &c, nil))

	if c.Host != "h" || c.Port != 1000000 || c.Timeout != 2*time.Second || c.Ratio != 0.5 ||
		len(c.Tags) != 2 || !c.Debug || c.Level != "info" || c.DB.Pass != "p" {
		t.Errorf("got %+v", c)
	}
}

// File keys may be in any case: matching goes through the env tag.
func TestFromFileLowerCaseKeys(t *testing.T) {
	var c config
	must(t, FromFile(write(t, "app.env", "host=h\ndb_pass=p\n"), &c, nil))

	if c.Host != "h" || c.DB.Pass != "p" {
		t.Errorf("got %+v", c)
	}
}

func TestFromFileNoExtension(t *testing.T) {
	var c config
	must(t, FromFile(write(t, "config", "HOST=h\n"), &c, nil))

	if c.Host != "h" {
		t.Errorf("Host = %q", c.Host)
	}
}

func TestFromFileUnsupportedFormat(t *testing.T) {
	err := FromFile("app.toml", &config{}, nil)
	if err == nil || !strings.Contains(err.Error(), ".toml") {
		t.Errorf("err = %v, want mention of .toml", err)
	}
}

func TestFromFileMissing(t *testing.T) {
	err := FromFile(filepath.Join(t.TempDir(), "absent.env"), &config{}, nil)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

func TestFromFileDecodeErrorNamesFile(t *testing.T) {
	path := write(t, "app.json", "{")
	err := FromFile(path, &config{}, nil)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want mention of %s", err, path)
	}
}

func TestRegisterFileDecoder(t *testing.T) {
	RegisterFileDecoder(".YML", DecodeTree(json.Unmarshal)) // extension case does not matter
	t.Cleanup(func() { delete(decoders, ".yml") })

	var c config
	must(t, FromFile(write(t, "app.yml", `{"host":"h"}`), &c, nil))

	if c.Host != "h" {
		t.Errorf("Host = %q", c.Host)
	}
}

func TestFromFileWithOptions(t *testing.T) {
	var c config
	must(t, FromFile(write(t, "app.env", "APP_HOST=h\n"), &c, &env.Options{Prefix: "APP_"}))

	if c.Host != "h" {
		t.Errorf("Host = %q", c.Host)
	}
}
