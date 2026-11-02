package sublink

// Plan 193: a small block-YAML writer for the Clash document. fork.107
// encoded every value through yaml.v3 Node.Encode (encode → text → parse)
// inside omap.MarshalYAML, nested, which cost ~18 MB and ~16 ms for 100
// nodes. This writer emits the same bytes directly.
//
// The scalar rules below are a port of what gopkg.in/yaml.v3 v3.0.1 does for
// a string value in block context with an unlimited line width (MIT /
// Apache-2.0, see its emitterc.go yaml_emitter_analyze_scalar /
// yaml_emitter_select_scalar_style / yaml_emitter_write_*_scalar, resolve.go
// and encode.go stringv):
//   - a value that would resolve to another type when plain (null, bools,
//     numbers, timestamps, YAML 1.1 bools, base-60 floats) is double-quoted;
//   - otherwise plain when allowed in block context, else single-quoted when
//     allowed, else double-quoted (with yaml.v3's escapes).
// Strings with line breaks (literal blocks depend on the indentation) or
// invalid UTF-8 (!!binary) are refused; Clash then falls back to yaml.v3.
// TestClashWriterMatchesYAMLv3 compares both encoders on many inputs.

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// writeBlockYAML writes doc as yaml.v3 (SetIndent(2)) would; ok=false when a
// value is not supported (the caller falls back to yaml.v3).
func writeBlockYAML(doc omap, sizeHint int) ([]byte, bool) {
	w := yamlWriter{b: make([]byte, 0, sizeHint)}
	w.mapping(doc, 0, false)
	if w.bad {
		return nil, false
	}
	return w.b, true
}

type yamlWriter struct {
	b   []byte
	bad bool
}

func (w *yamlWriter) indent(n int) {
	for i := 0; i < n; i++ {
		w.b = append(w.b, ' ')
	}
}

func (w *yamlWriter) str(s string) {
	var ok bool
	if w.b, ok = appendYAMLString(w.b, s); !ok {
		w.bad = true
	}
}

// mapping writes m with its keys at column ind. inItem: the first key goes
// right after an already written "- ".
func (w *yamlWriter) mapping(m omap, ind int, inItem bool) {
	for i, e := range m {
		if i > 0 || !inItem {
			w.indent(ind)
		}
		if !yamlPlainKey(e.k) {
			w.bad = true
			return
		}
		w.b = append(w.b, e.k...)
		w.b = append(w.b, ':')
		w.value(e.v, ind)
	}
}

// value writes the part after "key:" for a key at column ind.
func (w *yamlWriter) value(v interface{}, ind int) {
	switch x := v.(type) {
	case string:
		w.b = append(w.b, ' ')
		w.str(x)
		w.b = append(w.b, '\n')
	case int:
		w.b = append(w.b, ' ')
		w.b = strconv.AppendInt(w.b, int64(x), 10)
		w.b = append(w.b, '\n')
	case bool:
		w.b = append(w.b, ' ')
		w.b = strconv.AppendBool(w.b, x)
		w.b = append(w.b, '\n')
	case omap:
		if len(x) == 0 {
			w.b = append(w.b, " {}\n"...)
			return
		}
		w.b = append(w.b, '\n')
		w.mapping(x, ind+2, false)
	case []string:
		if len(x) == 0 {
			w.b = append(w.b, " []\n"...)
			return
		}
		w.b = append(w.b, '\n')
		for _, s := range x {
			w.indent(ind + 2)
			w.b = append(w.b, "- "...)
			w.str(s)
			w.b = append(w.b, '\n')
		}
	case []omap:
		if len(x) == 0 {
			w.b = append(w.b, " []\n"...)
			return
		}
		w.b = append(w.b, '\n')
		for _, m := range x {
			w.indent(ind + 2)
			if len(m) == 0 {
				w.b = append(w.b, "- {}\n"...)
				continue
			}
			w.b = append(w.b, "- "...)
			w.mapping(m, ind+4, true)
		}
	default:
		w.bad = true
	}
}

// yamlPlainKey: keys are constants of this package; anything that would not
// be written plain as a simple key is refused (yaml.v3 fallback).
func yamlPlainKey(k string) bool {
	if k == "" || len(k) > 128 {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == ':' || c == ',' || c == '.') {
			return false
		}
	}
	plain, _ := yamlAnalyze(k)
	return plain && !yamlResolvesNonString(k)
}

// appendYAMLString appends s as yaml.v3 writes a string value; ok=false for
// line breaks or invalid UTF-8.
func appendYAMLString(b []byte, s string) ([]byte, bool) {
	if !utf8.ValidString(s) || yamlHasBreak(s) {
		return b, false
	}
	if yamlResolvesNonString(s) || yamlBase60Float(s) || yamlOldBool(s) {
		return appendYAMLDouble(b, s), true
	}
	if s == "<<" {
		// fork.107 quirk: Node.Encode re-parses a plain "<<" as a merge key and
		// the outer encoder then writes it with its tag.
		return append(b, "!!merge <<"...), true
	}
	plain, single := yamlAnalyze(s)
	switch {
	case plain:
		return append(b, s...), true
	case single:
		b = append(b, '\'')
		for i := 0; i < len(s); i++ {
			if s[i] == '\'' {
				b = append(b, '\'')
			}
			b = append(b, s[i])
		}
		return append(b, '\''), true
	}
	return appendYAMLDouble(b, s), true
}

// yamlHasBreak: \r \n NEL LS PS (libyaml is_break).
func yamlHasBreak(s string) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\r' || c == '\n':
			return true
		case c == 0xC2 && i+1 < len(s) && s[i+1] == 0x85:
			return true
		case c == 0xE2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9):
			return true
		}
	}
	return false
}

func yamlWidth(c byte) int {
	switch {
	case c&0x80 == 0x00:
		return 1
	case c&0xE0 == 0xC0:
		return 2
	case c&0xF0 == 0xE0:
		return 3
	case c&0xF8 == 0xF0:
		return 4
	}
	return 0
}

// yamlPrintable is libyaml is_printable (s is valid UTF-8, so the
// continuation bytes exist).
func yamlPrintable(s string, i int) bool {
	c := s[i]
	return c == 0x0A ||
		c >= 0x20 && c <= 0x7E ||
		c == 0xC2 && s[i+1] >= 0xA0 ||
		c > 0xC2 && c < 0xED ||
		c == 0xED && s[i+1] < 0xA0 ||
		c == 0xEE ||
		c == 0xEF && !(s[i+1] == 0xBB && s[i+2] == 0xBF) && !(s[i+1] == 0xBF && (s[i+2] == 0xBE || s[i+2] == 0xBF))
}

// yamlAnalyze is yaml_emitter_analyze_scalar for a non-empty string without
// line breaks: whether plain (block context) and single-quoted styles are
// allowed.
func yamlAnalyze(s string) (blockPlain, single bool) {
	if s == "" {
		return true, true
	}
	blockInd := len(s) >= 3 && (s[:3] == "---" || s[:3] == "...")
	var leadingSpace, trailingSpace, tabs, special bool
	preceded := true
	for i, w := 0, 0; i < len(s); i += w {
		c := s[i]
		w = yamlWidth(c)
		if w == 0 {
			return false, false
		}
		followed := i+w >= len(s) || s[i+w] == ' ' || s[i+w] == '\t'
		if i == 0 {
			switch c {
			case '#', ',', '[', ']', '{', '}', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
				blockInd = true
			case '?', ':', '-':
				if followed {
					blockInd = true
				}
			}
		} else {
			switch c {
			case ':':
				if followed {
					blockInd = true
				}
			case '#':
				if preceded {
					blockInd = true
				}
			}
		}
		if c == '\t' {
			tabs = true
		} else if !yamlPrintable(s, i) {
			special = true
		}
		if c == ' ' {
			if i == 0 {
				leadingSpace = true
			}
			if i+w == len(s) {
				trailingSpace = true
			}
		}
		preceded = c == ' ' || c == '\t' || c == 0
	}
	blockPlain, single = true, true
	if leadingSpace || trailingSpace {
		blockPlain = false
	}
	if tabs || special {
		blockPlain, single = false, false
	}
	if blockInd {
		blockPlain = false
	}
	return blockPlain, single
}

// appendYAMLDouble is yaml_emitter_write_double_quoted_scalar without line
// folding (yaml.v3 encoders have an unlimited width).
func appendYAMLDouble(b []byte, s string) []byte {
	b = append(b, '"')
	bom := len(s) >= 3 && s[0] == 0xEF && s[1] == 0xBB && s[2] == 0xBF
	for i := 0; i < len(s); {
		c := s[i]
		w := yamlWidth(c)
		if !bom && c != '"' && c != '\\' && yamlPrintable(s, i) {
			b = append(b, s[i:i+w]...)
			i += w
			continue
		}
		r, _ := utf8.DecodeRuneInString(s[i:])
		i += w
		b = append(b, '\\')
		switch r {
		case 0x00:
			b = append(b, '0')
		case 0x07:
			b = append(b, 'a')
		case 0x08:
			b = append(b, 'b')
		case 0x09:
			b = append(b, 't')
		case 0x0A:
			b = append(b, 'n')
		case 0x0B:
			b = append(b, 'v')
		case 0x0C:
			b = append(b, 'f')
		case 0x0D:
			b = append(b, 'r')
		case 0x1B:
			b = append(b, 'e')
		case 0x22:
			b = append(b, '"')
		case 0x5C:
			b = append(b, '\\')
		case 0x85:
			b = append(b, 'N')
		case 0xA0:
			b = append(b, '_')
		case 0x2028:
			b = append(b, 'L')
		case 0x2029:
			b = append(b, 'P')
		default:
			digits := 8
			switch {
			case r <= 0xFF:
				b, digits = append(b, 'x'), 2
			case r <= 0xFFFF:
				b, digits = append(b, 'u'), 4
			default:
				b = append(b, 'U')
			}
			for k := (digits - 1) * 4; k >= 0; k -= 4 {
				d := byte((r >> uint(k)) & 0x0F)
				if d < 10 {
					b = append(b, d+'0')
				} else {
					b = append(b, d+'A'-10)
				}
			}
		}
	}
	return append(b, '"')
}

// ------------------------------------------------ yaml.v3 resolve (tag "")

// yamlHint is yaml.v3's resolveTable: S sign, D digit, M map word, . float.
func yamlHint(c byte) byte {
	switch {
	case c == '+' || c == '-':
		return 'S'
	case c >= '0' && c <= '9':
		return 'D'
	case c == '.':
		return '.'
	}
	switch c {
	case 'y', 'Y', 'n', 'N', 't', 'T', 'f', 'F', 'o', 'O', '~':
		return 'M'
	}
	return 0
}

// yamlResolveWords: yaml.v3 resolveMap keys (all non-string; "<<" has no
// hint and is never looked up for untagged values).
var yamlResolveWords = map[string]bool{
	"true": true, "True": true, "TRUE": true, "false": true, "False": true, "FALSE": true,
	"": true, "~": true, "null": true, "Null": true, "NULL": true,
	".nan": true, ".NaN": true, ".NAN": true, ".inf": true, ".Inf": true, ".INF": true,
	"+.inf": true, "+.Inf": true, "+.INF": true, "-.inf": true, "-.Inf": true, "-.INF": true,
}

var yamlStyleFloat = regexp.MustCompile(`^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$`)

var yamlTimestampFormats = []string{
	"2006-1-2T15:4:5.999999999Z07:00",
	"2006-1-2t15:4:5.999999999Z07:00",
	"2006-1-2 15:4:5.999999999",
	"2006-1-2",
}

func yamlTimestamp(s string) bool {
	i := 0
	for ; i < len(s); i++ {
		if c := s[i]; c < '0' || c > '9' {
			break
		}
	}
	if i != 4 || i == len(s) || s[i] != '-' {
		return false
	}
	for _, f := range yamlTimestampFormats {
		if _, err := time.Parse(f, s); err == nil {
			return true
		}
	}
	return false
}

// yamlResolvesNonString: yaml.v3 resolve("", s) != !!str.
func yamlResolvesNonString(s string) bool {
	hint := byte('N')
	if s != "" {
		hint = yamlHint(s[0])
	}
	if hint == 0 {
		return false
	}
	if yamlResolveWords[s] {
		return true
	}
	switch hint {
	case '.':
		_, err := strconv.ParseFloat(s, 64)
		return err == nil
	case 'D', 'S':
		if yamlTimestamp(s) {
			return true
		}
		plain := strings.Replace(s, "_", "", -1)
		if _, err := strconv.ParseInt(plain, 0, 64); err == nil {
			return true
		}
		if _, err := strconv.ParseUint(plain, 0, 64); err == nil {
			return true
		}
		if yamlStyleFloat.MatchString(plain) {
			if _, err := strconv.ParseFloat(plain, 64); err == nil {
				return true
			}
		}
		if strings.HasPrefix(plain, "0b") {
			if _, err := strconv.ParseInt(plain[2:], 2, 64); err == nil {
				return true
			}
			if _, err := strconv.ParseUint(plain[2:], 2, 64); err == nil {
				return true
			}
		} else if strings.HasPrefix(plain, "-0b") {
			if _, err := strconv.ParseInt("-"+plain[3:], 2, 64); err == nil {
				return true
			}
		}
		if strings.HasPrefix(plain, "0o") {
			if _, err := strconv.ParseInt(plain[2:], 8, 64); err == nil {
				return true
			}
			if _, err := strconv.ParseUint(plain[2:], 8, 64); err == nil {
				return true
			}
		} else if strings.HasPrefix(plain, "-0o") {
			if _, err := strconv.ParseInt("-"+plain[3:], 8, 64); err == nil {
				return true
			}
		}
	}
	return false
}

var yamlBase60 = regexp.MustCompile(`^[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+(?:\.[0-9_]*)?$`)

func yamlBase60Float(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	if !(c == '+' || c == '-' || c >= '0' && c <= '9') || strings.IndexByte(s, ':') < 0 {
		return false
	}
	return yamlBase60.MatchString(s)
}

func yamlOldBool(s string) bool {
	switch s {
	case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON",
		"n", "N", "no", "No", "NO", "off", "Off", "OFF":
		return true
	}
	return false
}
