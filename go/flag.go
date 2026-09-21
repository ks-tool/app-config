package appconfig

import (
	"encoding"
	"fmt"
	"reflect"
	"strings"

	"github.com/caarlos0/env/v11"
	"github.com/spf13/pflag"
)

type FlagSet = pflag.FlagSet

var reg = map[reflect.Type]func(reflect.Value) pflag.Value{}

func init() {
	registerVar((*FlagSet).StringVar)
	registerVar((*FlagSet).BoolVar)
	registerVar((*FlagSet).DurationVar)

	registerVar((*FlagSet).IntVar)
	registerVar((*FlagSet).Int8Var)
	registerVar((*FlagSet).Int16Var)
	registerVar((*FlagSet).Int32Var)
	registerVar((*FlagSet).Int64Var)
	registerVar((*FlagSet).UintVar)
	registerVar((*FlagSet).Uint8Var)
	registerVar((*FlagSet).Uint16Var)
	registerVar((*FlagSet).Uint32Var)
	registerVar((*FlagSet).Uint64Var)
	registerVar((*FlagSet).Float32Var)
	registerVar((*FlagSet).Float64Var)

	registerVar((*FlagSet).StringSliceVar)
	registerVar((*FlagSet).IntSliceVar)
	registerVar((*FlagSet).Int32SliceVar)
	registerVar((*FlagSet).Int64SliceVar)
	registerVar((*FlagSet).UintSliceVar)
	registerVar((*FlagSet).BoolSliceVar)
	registerVar((*FlagSet).Float32SliceVar)
	registerVar((*FlagSet).Float64SliceVar)
	registerVar((*FlagSet).DurationSliceVar)

	registerVar((*FlagSet).StringToStringVar)
	registerVar((*FlagSet).StringToIntVar)
	registerVar((*FlagSet).StringToInt64Var)

	registerVar((*FlagSet).IPVar)
	registerVar((*FlagSet).IPNetVar)
	registerVar((*FlagSet).IPMaskVar)
	registerVar((*FlagSet).IPSliceVar)
	registerVar((*FlagSet).IPNetSliceVar)

	registerVar((*FlagSet).BytesBase64Var)
}

type value[T any] struct {
	p     *T
	parse func(string) (T, error)
	kind  string
}

func (v value[T]) Set(s string) error {
	x, err := v.parse(strings.TrimSpace(s))
	if err != nil {
		return err
	}
	*v.p = x
	return nil
}

func (v value[T]) Type() string   { return v.kind }
func (v value[T]) String() string { return fmt.Sprint(*v.p) }

type text struct {
	u encoding.TextUnmarshaler
	f reflect.Value
}

func (t text) Set(s string) error { return t.u.UnmarshalText([]byte(s)) }
func (t text) Type() string       { return strings.ToLower(t.f.Type().Name()) }

func (t text) String() string {
	if m, ok := t.u.(encoding.TextMarshaler); ok {
		if b, err := m.MarshalText(); err == nil {
			return string(b)
		}
	}
	return fmt.Sprint(t.f.Interface())
}

func registerVar[T any](bind func(*FlagSet, *T, string, T, string)) {
	reg[reflect.TypeFor[T]()] = func(f reflect.Value) pflag.Value {
		p := f.Addr().Interface().(*T)
		scratch := pflag.NewFlagSet("", pflag.ContinueOnError)
		bind(scratch, p, "v", *p, "")
		return scratch.Lookup("v").Value
	}
}

func RegisterFlagType[T any](kind string, parse func(string) (T, error)) {
	if _, ok := reg[reflect.TypeFor[T]()]; ok {
		panic("repeated registration for type " + kind)
	}

	reg[reflect.TypeFor[T]()] = func(f reflect.Value) pflag.Value {
		return value[T]{p: f.Addr().Interface().(*T), parse: parse, kind: kind}
	}
}

func FromEnv(v any, opts *EnvOptions) error {
	if opts == nil {
		return env.Parse(v)
	}
	return env.ParseWithOptions(v, *opts)
}

func BindFlags(fs *FlagSet, prefix string, cfg any) {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		name := sf.Tag.Get("flag")
		if name == "" {
			continue
		}

		help := sf.Tag.Get("help")
		if e := sf.Tag.Get("env"); e != "" {
			help += " [" + prefix + e + "]"
		}

		pv := newValue(v.Field(i))
		if pv == nil {
			panic(fmt.Sprintf("flag --%s: field %s has unsupported type %s", name, sf.Name, sf.Type))
		}

		f := fs.VarPF(pv, name, sf.Tag.Get("short"), help)
		if pv.Type() == "bool" {
			f.NoOptDefVal = "true" // otherwise --verbose without a value is an error
		}
	}
}

func newValue(f reflect.Value) pflag.Value {
	if val, ok := reg[f.Type()]; ok {
		return val(f)
	}

	// net.IP, netip.Addr, time.Time, slog.Level, uuid.UUID ...
	if tu, ok := f.Addr().Interface().(encoding.TextUnmarshaler); ok {
		return text{tu, f}
	}
	return nil
}
