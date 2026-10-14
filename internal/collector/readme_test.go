package collector

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	readmePath   = "../../README.md"
	tableStart   = "<!-- metrics-table:start -->"
	tableEnd     = "<!-- metrics-table:end -->"
	testOnlyName = "zfs_test_"
)

func typeName(vt prometheus.ValueType) string {
	switch vt {
	case prometheus.CounterValue:
		return "counter"
	case prometheus.GaugeValue:
		return "gauge"
	case prometheus.UntypedValue:
		return "untyped"
	default:
		return "untyped"
	}
}

func labelList(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	return "`" + strings.Join(labels, "`, `") + "`"
}

func familyType(f DynamicFamily) string {
	switch f.Prefix {
	case poolIOFamily.Prefix:
		return "counter (`_total`)"
	case smartNVMeFamily.Prefix:
		return "gauge"
	default:
		return "untyped"
	}
}

// metricTable renders the README metric table from the metric definitions.
func metricTable() string {
	defs := Definitions()
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	var b strings.Builder
	b.WriteString("| Metric | Type | Labels | Description |\n|---|---|---|---|\n")
	for _, d := range defs {
		if strings.HasPrefix(d.Name, testOnlyName) {
			continue
		}
		b.WriteString("| `" + d.Name + "` | " + typeName(d.ValueType) + " | " + labelList(d.Labels) + " | " + d.Help + " |\n")
	}
	fams := DynamicFamilies()
	sort.Slice(fams, func(i, j int) bool { return fams[i].Prefix < fams[j].Prefix })
	for _, f := range fams {
		b.WriteString("| `" + f.Prefix + "_*` | " + familyType(f) + " | " + labelList(f.Labels) + " | " + f.Help + "; one metric per statistic name. |\n")
	}
	return b.String()
}

// TestReadmeMetricTable keeps the README metric table in step with the code.
// Run with UPDATE_README=1 to rewrite the table.
func TestReadmeMetricTable(t *testing.T) {
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)
	start := strings.Index(readme, tableStart)
	end := strings.Index(readme, tableEnd)
	if start < 0 || end < start {
		t.Fatalf("README lacks the %s and %s markers", tableStart, tableEnd)
	}
	want := tableStart + "\n" + metricTable() + tableEnd
	got := readme[start : end+len(tableEnd)]
	if got == want {
		return
	}
	if os.Getenv("UPDATE_README") == "1" {
		if err := os.WriteFile(readmePath, []byte(readme[:start]+want+readme[end+len(tableEnd):]), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("README metric table is out of date; run: UPDATE_README=1 go test ./internal/collector -run TestReadmeMetricTable")
}
