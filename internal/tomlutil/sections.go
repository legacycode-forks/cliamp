package tomlutil

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Decode decodes a complete TOML document using the canonical TOML decoder.
func Decode(data []byte, v any) error { return toml.Unmarshal(data, v) }

// RemoveRepeatedKeys removes earlier assignments with the same key in the
// same TOML table. This preserves cliamp's historical last-value-wins policy
// while leaving array-table instances independent and in document order.
func RemoveRepeatedKeys(data []byte) ([]byte, error) {
	parser := new(unstable.Parser)
	parser.Reset(data)
	section := ""
	arrayTableCount := make(map[string]int)
	seen := make(map[string][2]int)
	var remove [][2]int
	for parser.NextExpression() {
		expr := parser.Expression()
		switch expr.Kind {
		case unstable.Table:
			section = strings.Join(astKey(expr), ".")
		case unstable.ArrayTable:
			name := strings.Join(astKey(expr), ".")
			arrayTableCount[name]++
			section = fmt.Sprintf("%s#%d", name, arrayTableCount[name])
		case unstable.KeyValue:
			key := strings.Join(astKey(expr), ".")
			name := key
			if section != "" {
				name = section + "." + key
			}
			start := int(expr.Raw.Offset)
			for start > 0 && data[start-1] != '\n' {
				start--
			}
			end := int(expr.Raw.Offset + expr.Raw.Length)
			for end < len(data) && data[end] != '\n' {
				end++
			}
			if previous, ok := seen[name]; ok {
				remove = append(remove, previous)
			}
			seen[name] = [2]int{start, end}
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	if len(remove) == 0 {
		return append([]byte(nil), data...), nil
	}
	var out strings.Builder
	last := 0
	for _, span := range remove {
		if span[0] < last {
			continue
		}
		out.WriteString(string(data[last:span[0]]))
		last = span[1]
	}
	out.WriteString(string(data[last:]))
	return []byte(out.String()), nil
}

func astKey(expr *unstable.Node) []string {
	var key []string
	it := expr.Key()
	for it.Next() {
		key = append(key, string(it.Node().Data))
	}
	return key
}

// ArrayTableNames returns top-level array-table names in document order. It
// uses go-toml's syntax parser so callers can combine typed decoding with the
// ordering information that decoding into separate slices cannot retain.
func ArrayTableNames(data []byte) ([]string, error) {
	var p unstable.Parser
	p.Reset(data)
	var names []string
	for p.NextExpression() {
		n := p.Expression()
		if n.Kind != unstable.ArrayTable {
			continue
		}
		it := n.Key()
		var parts []string
		for it.Next() {
			parts = append(parts, string(it.Node().Data))
		}
		if len(parts) != 1 {
			return nil, fmt.Errorf("unsupported array-table key %q", strings.Join(parts, "."))
		}
		names = append(names, parts[0])
	}
	if err := p.Error(); err != nil {
		return nil, err
	}
	return names, nil
}

// FlattenStringMap converts dotted TOML tables into the legacy flat provider
// metadata keys (for example navidrome.id).
func FlattenStringMap(src map[string]any) map[string]string {
	var out map[string]string
	var walk func(string, any)
	walk = func(prefix string, value any) {
		switch v := value.(type) {
		case map[string]any:
			for k, child := range v {
				key := k
				if prefix != "" {
					key = prefix + "." + k
				}
				walk(key, child)
			}
		case string:
			if out == nil {
				out = make(map[string]string)
			}
			out[prefix] = v
		default:
			if out == nil {
				out = make(map[string]string)
			}
			out[prefix] = fmt.Sprint(v)
		}
	}
	for key, value := range src {
		walk(key, value)
	}
	return out
}

// StringValue formats scalar TOML values for the legacy section callback.
func StringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.(bool); ok {
		return strconv.FormatBool(b)
	}
	return fmt.Sprint(v)
}

// ParseSections parses a minimal TOML document made up of repeated
// [[<section>]] blocks of `key = "value"` lines. For each section header it
// calls emit once with the accumulated fields, with values unquoted via
// Unquote. Blank lines, comments (#), and lines outside any section are
// ignored. An empty section still triggers emit, so callers apply their own
// validation. When a key repeats within a section, the last value wins.
func ParseSections(data []byte, section string, emit func(fields map[string]string)) {
	ParseNamedSections(data, []string{section}, func(_ string, fields map[string]string) {
		emit(fields)
	})
}

// ParseNamedSections is like ParseSections but accepts several section names
// and passes the matched section name to emit. Sections may be interleaved;
// emit follows document order.
func ParseNamedSections(data []byte, sections []string, emit func(section string, fields map[string]string)) {
	headers := make(map[string]string, len(sections))
	for _, s := range sections {
		headers["[["+s+"]]"] = s
	}
	var fields map[string]string
	cur := ""
	flush := func() {
		if fields != nil {
			emit(cur, fields)
		}
	}
	for rawLine := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if name, ok := headers[line]; ok {
			flush()
			fields = make(map[string]string)
			cur = name
			continue
		}
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			// Unrecognized array-table header: flush the section we were in so
			// its fields cannot leak into the next recognized section.
			flush()
			fields = nil
			cur = ""
			continue
		}
		if fields == nil {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = Unquote(strings.TrimSpace(val))
	}
	flush()
}
