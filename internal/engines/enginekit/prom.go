package enginekit

import (
	"strconv"
	"strings"
	"time"
)

// Prom holds the samples of a Prometheus text exposition.
type Prom struct {
	Plain   map[string]float64            // samples without labels, by name
	Labeled map[string]map[string]float64 // name -> first label's value -> sample
}

// Num is the plain sample name as an integer; 0 when absent.
func (p Prom) Num(name string) int64 { return int64(p.Plain[name]) }

// Seconds is the plain sample name read as seconds.
func (p Prom) Seconds(name string) time.Duration {
	return time.Duration(p.Plain[name] * float64(time.Second))
}

// Millis is the plain sample name read as milliseconds.
func (p Prom) Millis(name string) time.Duration {
	return time.Duration(p.Plain[name] * float64(time.Millisecond))
}

// OneHot returns the label value whose sample is 1 in a one-hot gauge such
// as splash_memory_pressure{state="warning"} 1; "" when none is.
func (p Prom) OneHot(name string) string {
	for v, x := range p.Labeled[name] {
		if x == 1 {
			return v
		}
	}
	return ""
}

// ParseProm reads the text format (version 0.0.4): "name{k=\"v\",...} value
// [timestamp]". Names may contain ':' ("llamacpp:..."). Comments and
// unreadable lines are skipped.
func ParseProm(data []byte) Prom {
	p := Prom{Plain: map[string]float64{}, Labeled: map[string]map[string]float64{}}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		name, labelText, rest := line, "", ""
		hasLabels := false
		if i := strings.IndexAny(line, "{ \t"); i >= 0 {
			name = line[:i]
			if line[i] == '{' {
				end := labelsEnd(line, i)
				if end < 0 {
					continue
				}
				labelText, rest, hasLabels = line[i+1:end], line[end+1:], true
			} else {
				rest = line[i:]
			}
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		if !hasLabels {
			p.Plain[name] = v
			continue
		}
		if p.Labeled[name] == nil {
			p.Labeled[name] = map[string]float64{}
		}
		p.Labeled[name][firstLabelValue(labelText)] = v
	}
	return p
}

// labelsEnd returns the index of the '}' that closes the label set opened at
// start, skipping quoted values (which may contain '}' and escaped quotes).
func labelsEnd(s string, start int) int {
	inQuote := false
	for i := start + 1; i < len(s); i++ {
		switch c := s[i]; {
		case inQuote && c == '\\':
			i++
		case c == '"':
			inQuote = !inQuote
		case !inQuote && c == '}':
			return i
		}
	}
	return -1
}

// firstLabelValue returns the unescaped value of the first label.
func firstLabelValue(labels string) string {
	_, rest, ok := strings.Cut(labels, `="`)
	if !ok {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		switch c := rest[i]; c {
		case '"':
			return b.String()
		case '\\':
			i++
			if i < len(rest) {
				if rest[i] == 'n' {
					b.WriteByte('\n')
				} else {
					b.WriteByte(rest[i])
				}
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
