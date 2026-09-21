package appconfig

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

type appCfg struct {
	Cfg   string `flag:"config" help:"config file"`
	Host  string `env:"HOST" flag:"host" help:"listen address"`
	Port  int    `env:"PORT" flag:"port" help:"listen port"`
	Level string `env:"LEVEL" flag:"level" envDefault:"info" help:"log level"`
}

func newCmd(cfg *appCfg) *cobra.Command {
	cmd := &cobra.Command{Use: "app", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Load(cfg, File(cfg.Cfg), Env(), Flags(cmd.Flags()))
		}}
	BindFlags(cmd.Flags(), "APP_", cfg) // while building the command
	return cmd
}

func TestCobraHelpListsFlags(t *testing.T) {
	out := new(bytes.Buffer)
	cmd := newCmd(&appCfg{})
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--help"})
	must(t, cmd.Execute())

	t.Logf("--help output:\n%s", out)
	for _, want := range []string{"--host", "--port", "[APP_HOST]", "--config"} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Errorf("--help lacks %q", want)
		}
	}
}

// The config path arrives as a flag, so the file can only be read inside RunE.
func TestCobraConfigPathFromFlag(t *testing.T) {
	path := write(t, "app.env", "HOST=from-file\nPORT=1\nLEVEL=debug\n")
	t.Setenv("PORT", "2")

	var c appCfg
	cmd := newCmd(&c)
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"--config", path, "--port", "9090"})
	must(t, cmd.Execute())

	t.Logf("%+v", c)
	if c.Host != "from-file" || c.Port != 9090 || c.Level != "debug" {
		t.Errorf("got %+v", c)
	}
}
