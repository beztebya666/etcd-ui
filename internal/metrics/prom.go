// Package metrics parses Prometheus text exposition format. Just enough to
// pull a handful of named gauges/counters out of an etcd /metrics response —
// we don't want client-side dependence on prometheus.io/client_golang.
package metrics

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// Sample is one parsed metric line. Labels are flattened "name=val,name=val".
type Sample struct {
	Name   string  `json:"name"`
	Labels string  `json:"labels,omitempty"`
	Value  float64 `json:"value"`
}

// Parse reads a Prometheus text body and returns one Sample per metric line.
// Comments (# HELP / # TYPE / # …) are skipped, as are non-parseable lines.
func Parse(r io.Reader) []Sample {
	var out []Sample
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		s, ok := parseLine(line)
		if !ok {
			continue
		}
		out = append(out, s)
	}
	return out
}

func parseLine(line string) (Sample, bool) {
	// formats:
	//   metric_name 1.0
	//   metric_name{l="v",l2="v2"} 1.0
	// trailing timestamp is ignored.
	var name, labels, value string
	braceStart := strings.IndexByte(line, '{')
	if braceStart >= 0 {
		braceEnd := strings.IndexByte(line, '}')
		if braceEnd < 0 || braceEnd < braceStart {
			return Sample{}, false
		}
		name = line[:braceStart]
		labels = line[braceStart+1 : braceEnd]
		rest := strings.TrimSpace(line[braceEnd+1:])
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return Sample{}, false
		}
		value = fields[0]
	} else {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return Sample{}, false
		}
		name = fields[0]
		value = fields[1]
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return Sample{}, false
	}
	return Sample{Name: name, Labels: labels, Value: v}, true
}

// Filter returns only samples whose names are in the keep set. Cheap O(n*m)
// but n is "all etcd metrics" and m is a small allowlist, so it's fine.
func Filter(samples []Sample, keep []string) []Sample {
	want := make(map[string]struct{}, len(keep))
	for _, k := range keep {
		want[k] = struct{}{}
	}
	out := samples[:0]
	for _, s := range samples {
		if _, ok := want[s.Name]; ok {
			out = append(out, s)
		}
	}
	return out
}
