package appconfig

import (
	"os"
	"reflect"
	"strings"

	"github.com/caarlos0/env/v11"
)

type EnvOptions = env.Options

type loader struct {
	kv   map[string]string
	opts *EnvOptions
	fs   *FlagSet
}

// Source is one step of Load: a key/value set to merge, flags to parse or the
// env options to parse with. Every step is optional.
type Source func(*loader) error

// File reads name; the format follows the extension.
func File(name string) Source {
	return func(l *loader) error {
		kv, err := decodeFile(name)
		if err != nil {
			return err
		}
		addKV(l.kv, kv)
		return nil
	}
}

// Env reads the process environment.
func Env() Source {
	return func(l *loader) error {
		vars := os.Environ()
		kv := make(map[string]string, len(vars))
		for _, e := range vars {
			if k, v, ok := strings.Cut(e, "="); ok {
				kv[k] = v
			}
		}
		addKV(l.kv, kv)
		return nil
	}
}

// KV merges a ready key/value set and is the way to plug in a source of your
// own; Vault has (*Vault).Source.
func KV(kv map[string]string) Source {
	return func(l *loader) error {
		addKV(l.kv, kv)
		return nil
	}
}

// Flags protects what fs has already parsed into v: the fields whose flags were
// given on the command line are restored after the sources are applied, so the
// command line wins over them, and over envDefault.
//
// Bind and parse the flags before Load, or --help would list none of them:
//
//	popit.BindFlags(fs, "APP_", &cfg) // while building the command
//	fs.Parse(os.Args[1:])             // or cobra does it for you
//	popit.Load(&cfg, popit.File("app.yaml"), popit.Env(), popit.Flags(fs))
//
// The other order needs no Flags at all: load the sources first and bind
// afterwards, and the flags start off the loaded config, which --help shows as
// their defaults, while parsing writes over it.
func Flags(fs *FlagSet) Source {
	return func(l *loader) error {
		l.fs = fs
		return nil
	}
}

// Options sets the env options of the whole load: prefix, tag names and so on.
// Its Environment is ignored, as the merged sources take its place.
func Options(opts EnvOptions) Source {
	return func(l *loader) error {
		l.opts = &opts
		return nil
	}
}

// Load fills v from src in order of growing priority: a later source overrides
// an earlier one. The merged keys are parsed in a single pass, so envDefault and
// required see the whole set rather than one source at a time.
//
// A single source is the ordinary case, and no source at all is not an error:
// the merged set is then empty, so only envDefault applies and the process
// environment stays unread until Env asks for it. Note that envDefault also
// overrides a value preset in v; pass Options with SetDefaultsForZeroValuesOnly
// to keep such a value.
func Load(v any, src ...Source) error {
	l := &loader{kv: map[string]string{}}
	for _, s := range src {
		if err := s(l); err != nil {
			return err
		}
	}

	restore := saveFlags(l.fs, v)
	if err := parse(l.kv, v, l.opts); err != nil {
		return err
	}
	restore()
	return nil
}

// saveFlags copies the fields whose flags were set on the command line and
// returns a function putting them back.
func saveFlags(fs *FlagSet, cfg any) func() {
	if fs == nil {
		return func() {}
	}

	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	var fields, saved []reflect.Value
	for i := range t.NumField() {
		if name := t.Field(i).Tag.Get("flag"); name == "" || !fs.Changed(name) {
			continue
		}

		f := v.Field(i)
		cp := reflect.New(f.Type()).Elem()
		cp.Set(f)
		fields, saved = append(fields, f), append(saved, cp)
	}

	return func() {
		for i, f := range fields {
			f.Set(saved[i])
		}
	}
}
