package loader

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-gost/core/logger"
	"github.com/go-gost/x/config"
)

// plan 137 (QA F13): a gost.json without "log" runs at warn; an explicit level
// in gost.json still wins and survives the agent's config saves.
func TestLoadLogLevelDefaultAndExplicit(t *testing.T) {
	if prev := logger.Default(); prev != nil {
		defer logger.SetDefault(prev)
	}

	if err := Load(&config.Config{}); err != nil {
		t.Fatal(err)
	}
	if l := logger.Default(); !l.IsLevelEnabled(logger.WarnLevel) || l.IsLevelEnabled(logger.InfoLevel) {
		t.Fatalf("no log config: level %s, want warn", l.GetLevel())
	}

	var cfg config.Config
	if err := json.Unmarshal([]byte(`{"services":[],"log":{"level":"info"}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := Load(&cfg); err != nil {
		t.Fatal(err)
	}
	if l := logger.Default(); !l.IsLevelEnabled(logger.InfoLevel) || l.IsLevelEnabled(logger.DebugLevel) {
		t.Fatalf("gost.json log.level=info: level %s", l.GetLevel())
	}
	var buf bytes.Buffer
	if err := cfg.Write(&buf, "json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"level": "info"`) && !strings.Contains(buf.String(), `"level":"info"`) {
		t.Fatalf("saved config lost log.level: %s", buf.String())
	}
}
