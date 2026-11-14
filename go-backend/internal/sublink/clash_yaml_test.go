package sublink

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"go-backend/internal/linechain"
)

// Plan 193: the block-YAML writer must produce exactly what the fork.107
// yaml.v3 encoder (clashYAMLv3, unchanged) produces.

// trickyStrings: inputs around every rule of the scalar port.
var trickyStrings = []string{
	"", " ", "  ", "a", "A b", " a", "a ", "a  b", "\t", "a\tb", "~", "null", "Null", "NULL", "nul",
	"true", "True", "TRUE", "tRue", "false", "yes", "Yes", "no", "NO", "on", "off", "y", "n", "Y", "N",
	"0", "1", "-1", "+1", "007", "0x1F", "0X1f", "0o17", "0O17", "0b101", "-0b101", "-0o7", "0b", "0o", "1_000",
	"_1", "1__0", "1e3", "1E-3", ".5", "5.", "-.5", "+.5", ".e3", "1.2.3", "1.0", "-1.5e+10", ".inf", ".Inf",
	"-.inf", "+.INF", ".nan", ".NaN", "nan", "inf", "Infinity", ".infinity", "0x1p-2", "1:20", "-1:20:30.5",
	"190:20:30", "1:2", "12:60", "2001-01-01", "2001-1-1", "2001-01-01T00:00:00Z", "2001-12-14t21:59:43.10-05:00",
	"2001-12-14 21:59:43.10", "2001-13-01", "20011-01-01", "2001-", "<<", "<", "=", "-", "--", "---", "--- a",
	"...", "....", "..", ".", "- a", "-a", "? a", "?a", ": a", ":a", "a:", "a: b", "a:b", "a :b", "a #b", "a#b",
	"#a", ",a", "a,b", "[a", "a]", "{a", "a}", "&a", "*a", "!a", "|a", ">a", "'a", "a'b", "''", "\"a", "a\"b",
	"%a", "@a", "`a", "a\\b", "\\", "a\x00b", "\x01", "\x1b[0m", "\x7f", "\u0080", "\u009f", "\u00a0", "a\u00a0",
	"\u00a0a", "é", "中文 节点", "沪日440 · 我的落地·东京", "🇺🇸 美国, 洛杉矶 = 1", "😀", "a😀b", "\ufeff", "\ufeffa",
	"a\ufeff", "\ufffe", "\uffff", "\ufffd", "\ue000", "\ud7ff", "\U0010ffff", "x\u200by", "HK #2 ; test // x \"q\"",
	"203.0.113.21", "2001:db8::1", "[2001:db8::1]", "::1", "::", "/ws?ed=2048", "/", "h2", "http/1.1",
	"https://1.1.1.1/dns-query", "+.lan", "p@ss:w/rd", "pa ss", "1 2", "0 0", "- ", " -", "a -", "?", ":", "a?", "a!",
	"%", "a%", "%%", "YES", "ON", "Off", "~a", "a~", "t", "f", "o", "O",
}

func scalarDoc(s string) omap {
	return omap{
		{"k", s},
		{"list", []string{s, "x"}},
		{"items", []omap{{{"name", s}, {"server", s}, {"port", 443}}, {}}},
		{"nested", omap{{"Host", s}, {"empty", omap{}}, {"none", []string{}}}},
	}
}

func compareDoc(t *testing.T, label string, doc omap) {
	t.Helper()
	want := clashYAMLv3(doc)
	got, ok := writeBlockYAML(doc, 64)
	if !ok {
		t.Fatalf("%s: writer refused", label)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s:\n--- yaml.v3\n%s\n--- writer\n%s", label, want, got)
	}
}

func TestYAMLScalarsMatchYAMLv3(t *testing.T) {
	for _, s := range trickyStrings {
		compareDoc(t, strconvQuote(s), scalarDoc(s))
	}
}

// TestYAMLRandomScalars: random strings over the characters that matter.
func TestYAMLRandomScalars(t *testing.T) {
	pool := []string{"a", "Z", "0", "1", "9", " ", "\t", "-", "+", ".", ":", "#", ",", "?", "[", "]", "{", "}", "&",
		"*", "!", "|", ">", "'", "\"", "%", "@", "`", "\\", "_", "e", "x", "o", "b", "~", "/", "=", "\x00", "\x07",
		"\x1f", "\x7f", "\u0085x", "\u00a0", "é", "中", "😀", "\ufeff", "\ufffe", "\ue000", "y", "n", "t", "T"}
	rng := rand.New(rand.NewSource(193))
	for i := 0; i < 20000; i++ {
		var sb strings.Builder
		for n := rng.Intn(7); n >= 0; n-- {
			sb.WriteString(pool[rng.Intn(len(pool))])
		}
		s := strings.ReplaceAll(sb.String(), "\u0085", "")
		compareDoc(t, strconvQuote(s), scalarDoc(s))
	}
}

func FuzzYAMLString(f *testing.F) {
	for _, s := range trickyStrings {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) || yamlHasBreak(s) {
			if _, ok := appendYAMLString(nil, s); ok {
				t.Fatalf("%q must be refused", s)
			}
			return
		}
		compareDoc(t, strconvQuote(s), scalarDoc(s))
	})
}

// TestClashWriterFallback: refused strings give the yaml.v3 bytes.
func TestClashWriterFallback(t *testing.T) {
	for _, name := range []string{"a\nb", "a\r", "x\u2028y", "x\u2029", "x\u0085", "bad\xffutf8"} {
		nodes := []Node{{Name: name, Up: linechain.Upstream{Type: "socks", Server: "203.0.113.9", Port: 1080}}}
		doc, _ := clashDoc(nodes, ModeGlobal, false, nil, "")
		if _, ok := writeBlockYAML(doc, 64); ok {
			t.Fatalf("%q: writer must refuse", name)
		}
		got, _ := Clash(nodes, ModeGlobal, false)
		if !bytes.Equal(got, clashYAMLv3(doc)) {
			t.Fatalf("%q: fallback differs", name)
		}
	}
}

// TestClashWriterMatchesYAMLv3: whole documents, every fixture and protocol,
// tricky names / secrets / paths in every string field.
func TestClashWriterMatchesYAMLv3(t *testing.T) {
	sets := map[string][]Node{"golden": goldenNodes(t), "100": nodes100(t), "empty": nil}
	_, m := matrixNodes(t)
	var tricky []Node
	i := 0
	for _, base := range m {
		for j := 0; j < 6; j++ {
			n := base
			s := trickyStrings[i%len(trickyStrings)]
			i++
			n.Name = s
			if n.Extra != nil {
				e := *n.Extra
				e.Password, e.ObfsHost, e.SNI, e.Server = s, s, s, s
				n.Extra = &e
			} else {
				n.Up.Password, n.Up.Server, n.Up.UUID = s, s, s
				if n.Up.WS != nil {
					ws := *n.Up.WS
					ws.Path, ws.Host = s, s
					n.Up.WS = &ws
				}
				if n.Up.TLS != nil {
					tl := *n.Up.TLS
					tl.ServerName, tl.ALPN = s, []string{s, "h2"}
					n.Up.TLS = &tl
				}
			}
			tricky = append(tricky, n)
		}
	}
	// ws without options → ws-opts: {}
	tricky = append(tricky, Node{Name: "ws-bare", Up: linechain.Upstream{Type: "vless", Server: "203.0.113.5", Port: 80, UUID: "u", Network: "ws"}})
	FinalizeNames(tricky)
	sets["tricky"] = tricky
	for label, nodes := range sets {
		for _, mode := range []string{ModeCNDirect, ModeGlobal} {
			for _, stash := range []bool{false, true} {
				doc, _ := clashDoc(nodes, mode, stash, nil, "")
				compareDoc(t, label+"/"+mode, doc)
				got, _ := Clash(nodes, mode, stash)
				if !bytes.Equal(got, clashYAMLv3(doc)) {
					t.Fatalf("%s/%s: Clash differs", label, mode)
				}
			}
		}
	}
}

func strconvQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			b.WriteString("\\x")
			b.WriteByte("0123456789abcdef"[r>>4])
			b.WriteByte("0123456789abcdef"[r&15])
			continue
		}
		b.WriteRune(r)
	}
	return "«" + b.String() + "»"
}
