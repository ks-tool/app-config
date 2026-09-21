package appconfig

import (
	"path/filepath"
	"strings"
	"testing"
)

type loadCfg struct {
	Host  string `env:"HOST" flag:"host"`
	Port  int    `env:"PORT" flag:"port"`
	Level string `env:"LEVEL" flag:"level" envDefault:"info"`
	Token string `env:"TOKEN,required"`
}

// bindParse binds and parses the flags the way a command does it before Load.
func bindParse(t *testing.T, cfg any, args ...string) *FlagSet {
	t.Helper()
	fs := newFlagSet()
	BindFlags(fs, "", cfg)
	must(t, fs.Parse(args))
	return fs
}

// A later source overrides an earlier one, and parsed flags override everything.
func TestLoadPriority(t *testing.T) {
	path := write(t, "app.env", "HOST=from-file\nPORT=1\nLEVEL=debug\nTOKEN=t\n")
	t.Setenv("PORT", "2")

	var c loadCfg
	fs := bindParse(t, &c, "--level", "warn")
	must(t, Load(&c, File(path), Env(), Flags(fs)))

	if c.Host != "from-file" || c.Port != 2 || c.Level != "warn" || c.Token != "t" {
		t.Errorf("got %+v", c)
	}
}

func TestLoadSourceOrder(t *testing.T) {
	first := write(t, "first.env", "HOST=first\nTOKEN=t\n")
	second := write(t, "second.env", "HOST=second\n")

	var c loadCfg
	must(t, Load(&c, File(first), File(second)))
	if c.Host != "second" {
		t.Errorf("Host = %q, want the last source to win", c.Host)
	}

	c = loadCfg{}
	must(t, Load(&c, File(second), File(first)))
	if c.Host != "first" {
		t.Errorf("Host = %q", c.Host)
	}
}

// envDefault must not overwrite a value that an earlier source provided.
func TestLoadDefaultDoesNotOverrideSource(t *testing.T) {
	path := write(t, "app.env", "LEVEL=debug\nTOKEN=t\n")

	var c loadCfg
	must(t, Load(&c, File(path), Env()))

	if c.Level != "debug" {
		t.Errorf("Level = %q, want the file value to survive", c.Level)
	}
}

func TestLoadDefaultAppliedWhenMissing(t *testing.T) {
	var c loadCfg
	must(t, Load(&c, File(write(t, "app.env", "TOKEN=t\n"))))

	if c.Level != "info" {
		t.Errorf("Level = %q, want envDefault", c.Level)
	}
}

// required is checked against the merged set, not against a single source.
func TestLoadRequiredSeesMergedKeys(t *testing.T) {
	path := write(t, "app.env", "TOKEN=t\n")

	var c loadCfg
	must(t, Load(&c, File(path), Env()))
	if c.Token != "t" {
		t.Errorf("Token = %q", c.Token)
	}

	err := Load(&loadCfg{}, Env())
	if err == nil || !strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("err = %v, want TOKEN to be reported as missing", err)
	}
}

func TestLoadEnvReadsProcessEnv(t *testing.T) {
	t.Setenv("HOST", "from-process-env")
	t.Setenv("TOKEN", "t")

	var c loadCfg
	must(t, Load(&c, Env()))

	if c.Host != "from-process-env" {
		t.Errorf("Host = %q", c.Host)
	}
}

// The sources are the only input: without them nothing is read, and
// opts.Environment does not stand in for a source.
func TestLoadWithoutSourcesReadsNothing(t *testing.T) {
	t.Setenv("TOKEN", "from-process-env")

	for _, src := range [][]Source{nil, {Options(EnvOptions{
		Environment: map[string]string{"TOKEN": "t"},
	})}} {
		err := Load(&loadCfg{}, src...)
		if err == nil || !strings.Contains(err.Error(), "TOKEN") {
			t.Errorf("err = %v, want TOKEN to be reported as missing", err)
		}
	}
}

func TestLoadWithPrefix(t *testing.T) {
	path := write(t, "app.env", "APP_HOST=h\nAPP_TOKEN=t\n")

	var c loadCfg
	must(t, Load(&c, File(path), Options(EnvOptions{Prefix: "APP_"})))

	if c.Host != "h" || c.Token != "t" {
		t.Errorf("got %+v", c)
	}
}

// Loading before BindFlags makes --help show the loaded config as the defaults,
// and parsing then writes over it without any Flags source.
func TestLoadBeforeBindFlagsShowsLoadedDefaults(t *testing.T) {
	path := write(t, "app.env", "HOST=from-file\nPORT=8080\nTOKEN=t\n")

	var c loadCfg
	must(t, Load(&c, File(path)))

	fs := newFlagSet()
	BindFlags(fs, "", &c)
	for name, want := range map[string]string{"host": "from-file", "port": "8080"} {
		if got := fs.Lookup(name).DefValue; got != want {
			t.Errorf("--%s DefValue = %q, want %q", name, got, want)
		}
	}

	must(t, fs.Parse([]string{"--host", "from-flag"}))
	if c.Host != "from-flag" || c.Port != 8080 {
		t.Errorf("got %+v", c)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Run("source_error", func(t *testing.T) {
		err := Load(&loadCfg{}, File(filepath.Join(t.TempDir(), "absent.env")))
		if err == nil || !strings.Contains(err.Error(), "absent.env") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("parse_error", func(t *testing.T) {
		err := Load(&loadCfg{}, File(write(t, "app.env", "PORT=nan\nTOKEN=t\n")))
		if err == nil || !strings.Contains(err.Error(), "Port") {
			t.Errorf("err = %v", err)
		}
	})

}

// Parsed flags win wherever Flags stands among the sources.
func TestLoadFlagsWinFromAnyPosition(t *testing.T) {
	path := write(t, "app.env", "HOST=from-file\nTOKEN=t\n")

	for _, order := range []string{"flags first", "flags last"} {
		var c loadCfg
		fs := bindParse(t, &c, "--host", "from-flag")

		src := []Source{Flags(fs), File(path)}
		if order == "flags last" {
			src = []Source{File(path), Flags(fs)}
		}
		must(t, Load(&c, src...))

		if c.Host != "from-flag" {
			t.Errorf("%s: Host = %q, want the flag to win", order, c.Host)
		}
	}
}

// The value of a flag must survive envDefault of the very same field.
func TestLoadFlagBeatsEnvDefault(t *testing.T) {
	var c loadCfg
	fs := bindParse(t, &c, "--level", "warn")

	must(t, Load(&c, File(write(t, "app.env", "TOKEN=t\n")), Flags(fs)))

	if c.Level != "warn" {
		t.Errorf("Level = %q, want the flag to beat envDefault", c.Level)
	}
}

// A flag left out on the command line must not shadow the sources.
func TestLoadUntouchedFlagDoesNotShadowSources(t *testing.T) {
	var c loadCfg
	fs := bindParse(t, &c) // no arguments at all

	must(t, Load(&c, File(write(t, "app.env", "HOST=from-file\nTOKEN=t\n")), Flags(fs)))

	if c.Host != "from-file" {
		t.Errorf("Host = %q, want the file value", c.Host)
	}
}

func TestLoadKV(t *testing.T) {
	var c loadCfg
	must(t, Load(&c,
		KV(map[string]string{"HOST": "low", "TOKEN": "t"}),
		KV(map[string]string{"HOST": "high"}),
	))

	if c.Host != "high" || c.Token != "t" {
		t.Errorf("got %+v", c)
	}
}

func TestLoadSourceErrorStopsPipeline(t *testing.T) {
	c := loadCfg{Host: "untouched"}
	err := Load(&c, File(filepath.Join(t.TempDir(), "absent.env")), Env())

	if err == nil {
		t.Fatal("want error")
	}
	if c.Host != "untouched" {
		t.Errorf("Host = %q, want the config left alone", c.Host)
	}
}

// Sources may spell the same key differently — Vault KV tends to be lower case,
// dotenv upper case. Priority must still follow the order, not map iteration.
func TestLoadKeyCaseAcrossSources(t *testing.T) {
	for range 20 {
		var c loadCfg
		must(t, Load(&c,
			KV(map[string]string{"host": "low", "token": "t"}),
			KV(map[string]string{"HOST": "high"}),
		))
		if c.Host != "high" {
			t.Fatalf("Host = %q, want the later source to win", c.Host)
		}

		c = loadCfg{}
		must(t, Load(&c,
			KV(map[string]string{"HOST": "high", "TOKEN": "t"}),
			KV(map[string]string{"host": "low"}),
		))
		if c.Host != "low" {
			t.Fatalf("Host = %q, want the later source to win", c.Host)
		}
	}
}

type plainCfg struct {
	Host  string `env:"HOST"`
	Level string `env:"LEVEL" envDefault:"info"`
}

// No source at all is not an error: only envDefault applies, and the process
// environment stays unread, as the empty set takes its place.
func TestLoadZeroSources(t *testing.T) {
	t.Setenv("HOST", "from-process-env")

	c := plainCfg{Host: "preset"}
	must(t, Load(&c))
	if c.Host != "preset" || c.Level != "info" {
		t.Errorf("got %+v, want the preset kept and envDefault applied", c)
	}

	var empty []Source // a slice built at run time may well be empty
	c = plainCfg{}
	must(t, Load(&c, empty...))
	if c.Host != "" || c.Level != "info" {
		t.Errorf("got %+v, want the environment left unread", c)
	}
}

func TestLoadSingleSource(t *testing.T) {
	var c plainCfg
	must(t, Load(&c, KV(map[string]string{"HOST": "h"})))

	if c.Host != "h" || c.Level != "info" {
		t.Errorf("got %+v", c)
	}
}

// envDefault overrides a value preset in the struct unless Options says otherwise.
func TestLoadPresetValueVsEnvDefault(t *testing.T) {
	c := plainCfg{Level: "warn"}
	must(t, Load(&c))
	if c.Level != "info" {
		t.Errorf("Level = %q, want envDefault to win by default", c.Level)
	}

	c = plainCfg{Level: "warn"}
	must(t, Load(&c, Options(EnvOptions{SetDefaultsForZeroValuesOnly: true})))
	if c.Level != "warn" {
		t.Errorf("Level = %q, want the preset value kept", c.Level)
	}
}
