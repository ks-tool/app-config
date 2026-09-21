package app_config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/caarlos0/env/v11"
)

// fromKV parses a flat key/value set with the same env tags as FromEnv.
// Keys are looked up as-is and upper-cased: db_host in a file or in Vault KV
// lands in the field tagged env:"DB_HOST".
func fromKV(kv map[string]string, v any, opts *env.Options) error {
	m := make(map[string]string, 2*len(kv))
	addKV(m, kv)
	return parse(m, v, opts)
}

// addKV indexes every key of kv both as-is and upper-cased. Indexing happens
// per set, not over a merged map, or two spellings of one key coming from
// different sources would override each other in map iteration order.
func addKV(m, kv map[string]string) {
	for k, val := range kv {
		m[k] = val
		m[strings.ToUpper(k)] = val
	}
}

func parse(kv map[string]string, v any, opts *env.Options) error {
	o := env.Options{}
	if opts != nil {
		o = *opts // a copy: Environment is replaced for this call only
	}
	o.Environment = kv
	return env.ParseWithOptions(v, o)
}

// DecodeTree turns a yaml.Unmarshal-like function into a Decoder for FromFile.
// Nesting is flattened into keys joined by `_`: {db: {host: x}} → DB_HOST,
// {srv: [{port: 1}]} → SRV_0_PORT (the slice field being tagged envPrefix:"SRV_").
func DecodeTree(unmarshal func([]byte, any) error) Decoder {
	return func(b []byte) (map[string]string, error) {
		var tree map[string]any
		if err := unmarshal(b, &tree); err != nil {
			return nil, err
		}
		m := map[string]string{}
		flatten("", tree, m)
		return m, nil
	}
}

func flatten(prefix string, v any, m map[string]string) {
	key := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + "_" + k
	}

	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			flatten(key(k), val, m)
		}
	case map[any]any: // yaml.v2, toml
		for k, val := range x {
			flatten(key(fmt.Sprint(k)), val, m)
		}
	case []any:
		var list []string
		for i, e := range x {
			switch e.(type) {
			case map[string]any, map[any]any:
				flatten(key(strconv.Itoa(i)), e, m)
			default:
				list = append(list, scalar(e))
			}
		}
		if list != nil {
			m[prefix] = strings.Join(list, ",")
		}
	default:
		m[prefix] = scalar(v)
	}
}

func scalar(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	default:
		return fmt.Sprint(x)
	}
}
