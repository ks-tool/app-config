package appconfig

import (
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

type flags struct {
	Host    string            `flag:"host" short:"H" help:"address" env:"HOST"`
	Port    int               `flag:"port" short:"p" help:"port"`
	Debug   bool              `flag:"debug"`
	Timeout time.Duration     `flag:"timeout"`
	Tags    []string          `flag:"tags"`
	Labels  map[string]string `flag:"labels"`
	IP      net.IP            `flag:"ip"`
	Key     []byte            `flag:"key"`
	Level   slog.Level        `flag:"level"` // via encoding.TextUnmarshaler
	Addr    netip.Addr        `flag:"addr"`  // via encoding.TextUnmarshaler
	Skipped string
}

type size int64

func newFlagSet() *FlagSet { return pflag.NewFlagSet("test", pflag.ContinueOnError) }

func TestBindFlagsParse(t *testing.T) {
	var c flags
	fs := newFlagSet()
	BindFlags(fs, "", &c)

	must(t, fs.Parse([]string{
		"-H", "example.com", "-p", "8080", "--debug", "--timeout", "2s",
		"--tags", "a,b", "--labels", "k=v", "--ip", "10.0.0.1", "--key", "aGk=",
		"--level", "debug", "--addr", "192.168.0.1",
	}))

	if c.Host != "example.com" || c.Port != 8080 || !c.Debug || c.Timeout != 2*time.Second {
		t.Errorf("got %+v", c)
	}
	if !slices.Equal(c.Tags, []string{"a", "b"}) || c.Labels["k"] != "v" {
		t.Errorf("tags = %v, labels = %v", c.Tags, c.Labels)
	}
	if !c.IP.Equal(net.ParseIP("10.0.0.1")) || string(c.Key) != "hi" {
		t.Errorf("ip = %v, key = %q", c.IP, c.Key)
	}
	if c.Level != slog.LevelDebug || c.Addr.String() != "192.168.0.1" {
		t.Errorf("level = %v, addr = %v", c.Level, c.Addr)
	}
}

// A flag left out must not overwrite a value that came from another source.
func TestBindFlagsKeepsExistingValues(t *testing.T) {
	c := flags{Host: "from-file", Port: 8080, Debug: true}
	fs := newFlagSet()
	BindFlags(fs, "", &c)

	must(t, fs.Parse([]string{"-p", "9090"}))

	if c.Host != "from-file" || c.Port != 9090 || !c.Debug {
		t.Errorf("got %+v", c)
	}
}

func TestBindFlagsBoolWithoutValue(t *testing.T) {
	var c flags
	fs := newFlagSet()
	BindFlags(fs, "", &c)

	if got := fs.Lookup("debug").NoOptDefVal; got != "true" {
		t.Errorf("NoOptDefVal = %q", got)
	}
	must(t, fs.Parse([]string{"--debug=false"}))
	if c.Debug {
		t.Error("Debug = true, want false")
	}
}

func TestBindFlagsDefaultsFromStruct(t *testing.T) {
	c := flags{Host: "def", Port: 8080}
	fs := newFlagSet()
	BindFlags(fs, "", &c)

	for name, want := range map[string]string{"host": "def", "port": "8080", "level": "INFO"} {
		if got := fs.Lookup(name).DefValue; got != want {
			t.Errorf("--%s DefValue = %q, want %q", name, got, want)
		}
	}
}

func TestBindFlagsUsageMentionsEnv(t *testing.T) {
	fs := newFlagSet()
	BindFlags(fs, "APP_", &flags{})

	if got := fs.Lookup("host").Usage; got != "address [APP_HOST]" {
		t.Errorf("usage = %q", got)
	}
	if got := fs.Lookup("port").Usage; got != "port" { // no env tag, no prefix appended
		t.Errorf("usage = %q", got)
	}
}

func TestBindFlagsSkipsUntaggedFields(t *testing.T) {
	fs := newFlagSet()
	BindFlags(fs, "", &flags{})

	if f := fs.Lookup("skipped"); f != nil {
		t.Error("a field without a flag tag must not produce a flag")
	}
	var n int
	fs.VisitAll(func(*pflag.Flag) { n++ })
	if want := reflect.TypeFor[flags]().NumField() - 1; n != want {
		t.Errorf("flags = %d, want %d", n, want)
	}
}

func TestBindFlagsTextUnmarshalerType(t *testing.T) {
	fs := newFlagSet()
	BindFlags(fs, "", &flags{})

	for name, want := range map[string]string{"level": "level", "addr": "addr", "port": "int"} {
		if got := fs.Lookup(name).Value.Type(); got != want {
			t.Errorf("--%s type = %q, want %q", name, got, want)
		}
	}
}

func TestBindFlagsUnsupportedTypePanics(t *testing.T) {
	defer func() {
		r := recover()
		msg, _ := r.(string)
		if !strings.Contains(msg, "--ch") || !strings.Contains(msg, "chan int") {
			t.Errorf("panic = %v, want mention of --ch and chan int", r)
		}
	}()

	BindFlags(newFlagSet(), "", &struct {
		Ch chan int `flag:"ch"`
	}{})
	t.Error("want panic")
}

func TestRegisterFlagType(t *testing.T) {
	RegisterFlagType("size", func(s string) (size, error) {
		n, err := strconv.ParseInt(strings.TrimSuffix(s, "k"), 10, 64)
		if strings.HasSuffix(s, "k") {
			n *= 1024
		}
		return size(n), err
	})
	t.Cleanup(func() { delete(reg, reflect.TypeFor[size]()) })

	var c struct {
		Size size `flag:"size"`
	}
	fs := newFlagSet()
	BindFlags(fs, "", &c)

	must(t, fs.Parse([]string{"--size", " 4k "})) // the value is trimmed of spaces
	if c.Size != 4096 {
		t.Errorf("Size = %d, want 4096", c.Size)
	}

	f := fs.Lookup("size")
	if f.Value.Type() != "size" || f.Value.String() != "4096" {
		t.Errorf("type = %q, value = %q", f.Value.Type(), f.Value.String())
	}
	if err := fs.Parse([]string{"--size", "abc"}); err == nil {
		t.Error("want parse error")
	}
}

func TestRegisterFlagTypeRepeatedPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("want panic")
		}
	}()

	RegisterFlagType("string", func(s string) (string, error) { return s, nil })
}

func TestFromEnv(t *testing.T) {
	var c config
	must(t, FromEnv(&c, &EnvOptions{Environment: map[string]string{
		"APP_HOST": "h", "APP_PORT": "80", "APP_DB_PASS": "p",
	}, Prefix: "APP_"}))

	if c.Host != "h" || c.Port != 80 || c.DB.Pass != "p" || c.Level != "info" {
		t.Errorf("got %+v", c)
	}
}

func TestFromEnvUsesProcessEnv(t *testing.T) {
	t.Setenv("HOST", "from-process-env")

	var c config
	must(t, FromEnv(&c, nil))

	if c.Host != "from-process-env" {
		t.Errorf("Host = %q", c.Host)
	}
}

func TestFromEnvError(t *testing.T) {
	err := FromEnv(&config{}, &EnvOptions{Environment: map[string]string{"PORT": "not-a-number"}})
	if err == nil || !strings.Contains(err.Error(), "Port") {
		t.Errorf("err = %v, want mention of the Port field", err)
	}
}

// A type whose MarshalText fails: String() must fall back to fmt.
type noText struct{ Val string }

func (n *noText) UnmarshalText(p []byte) error { n.Val = string(p); return nil }
func (n *noText) MarshalText() ([]byte, error) { return nil, errors.New("not supported") }

func TestBindFlagsTextMarshalFallback(t *testing.T) {
	var c struct {
		V noText `flag:"v"`
	}
	fs := newFlagSet()
	BindFlags(fs, "", &c)

	f := fs.Lookup("v")
	if f.DefValue != "{}" || f.Value.Type() != "notext" {
		t.Errorf("DefValue = %q, type = %q", f.DefValue, f.Value.Type())
	}

	must(t, fs.Parse([]string{"--v", "x"}))
	if c.V.Val != "x" || f.Value.String() != "{x}" {
		t.Errorf("Val = %q, String() = %q", c.V.Val, f.Value.String())
	}
}
