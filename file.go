package appconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Decoder func([]byte) (map[string]string, error)

var decoders = map[string]Decoder{
	"":      dotenv,
	".env":  dotenv,
	".json": jsonTree,
}

// RegisterFileDecoder adds support for a format by file extension:
//
//	popit.RegisterFileDecoder(".yaml", popit.DecodeTree(yaml.Unmarshal))
func RegisterFileDecoder(ext string, dec Decoder) {
	decoders[strings.ToLower(ext)] = dec
}

// FromFile reads settings from a file; the format follows the extension.
// Keys are matched against env tags, as in FromEnv, so opts.Prefix is
// usually left empty.
func FromFile(name string, v any, opts *EnvOptions) error {
	kv, err := decodeFile(name)
	if err != nil {
		return err
	}
	return fromKV(kv, v, opts)
}

func decodeFile(name string) (map[string]string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	dec, ok := decoders[ext]
	if !ok {
		return nil, fmt.Errorf("popit: unsupported config format %q", ext)
	}

	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}

	kv, err := dec(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return kv, nil
}

func jsonTree(b []byte) (map[string]string, error) {
	return DecodeTree(func(b []byte, v any) error {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber() // otherwise 1000000 becomes 1e+06
		return d.Decode(v)
	})(b)
}

func dotenv(b []byte) (map[string]string, error) {
	m := map[string]string{}
	for line := range strings.Lines(string(b)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			return nil, fmt.Errorf("invalid line %q", line)
		}

		v = strings.TrimSpace(v)
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		} else if len(v) > 1 && v[0] == '\'' && v[len(v)-1] == '\'' {
			v = v[1 : len(v)-1]
		}
		m[strings.TrimSpace(k)] = v
	}
	return m, nil
}
