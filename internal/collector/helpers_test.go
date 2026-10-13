package collector

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return string(data)
}

const (
	cmdStatus  = "zpool status -j --json-int -p"
	cmdList    = "zpool list -j --json-int -v"
	cmdIostat  = "zpool iostat -Hpvl -y 10 1"
	cmdGeom    = "geom disk list"
	cmdGlabel  = "glabel status"
	cmdARC     = "sysctl -q kstat.zfs.misc.arcstats"
	cmdKstat   = "sysctl -q kstat.zfs"
	cmdScan    = "smartctl --scan-open"
	cmdSmartDa = "smartctl -j -x -n standby -d sat /dev/da29"
	cmdSmartAd = "smartctl -j -x -n standby -d atacam /dev/ada0"
	cmdSmartNv = "smartctl -j -x -n standby -d nvme /dev/nvme0"
)

func cmdZfsGet() string {
	return "zfs get -Hp -o name,property,value -t filesystem,volume " + datasetPropertyList()
}

const smallScan = "/dev/ses0 -d atacam # /dev/ses0, ATA device\n" +
	"/dev/ada0 -d atacam # /dev/ada0, ATA device\n" +
	"/dev/da29 -d sat # /dev/da29 [SAT], ATA device\n" +
	"# /dev/nda0 -d scsi # /dev/nda0, SCSI device open failed: INQUIRY failed\n" +
	"/dev/nvme0 -d nvme # /dev/nvme0, NVMe device\n"

// freebsdFake answers every command the collectors run with output captured
// on a FreeBSD 15.1 host (serial numbers, WWNs, and GUIDs replaced).
func freebsdFake(t *testing.T) *runner.Fake {
	t.Helper()
	return runner.NewFake(map[string]runner.Response{
		cmdStatus:   {Stdout: fixture(t, "freebsd/zpool-status.json")},
		cmdList:     {Stdout: fixture(t, "freebsd/zpool-list.json")},
		cmdIostat:   {Stdout: fixture(t, "freebsd/zpool-iostat.txt")},
		cmdGeom:     {Stdout: fixture(t, "freebsd/geom-disk-list.txt")},
		cmdGlabel:   {Stdout: fixture(t, "freebsd/glabel-status.txt")},
		cmdARC:      {Stdout: fixture(t, "freebsd/sysctl-arcstats.txt")},
		cmdKstat:    {Stdout: fixture(t, "freebsd/sysctl-kstat-zfs.txt")},
		cmdZfsGet(): {Stdout: fixture(t, "freebsd/zfs-get.txt")},
		cmdScan:     {Stdout: smallScan},
		cmdSmartDa:  {Stdout: fixture(t, "freebsd/smartctl-x-da29.json"), ExitCode: 64},
		cmdSmartAd:  {Stdout: fixture(t, "freebsd/smartctl-x-ada0.json")},
		cmdSmartNv:  {Stdout: fixture(t, "freebsd/smartctl-x-nvme0.json")},
	})
}

func freebsdPlatform(run runner.Runner) *FreeBSDPlatform {
	return &FreeBSDPlatform{Run: run, Paths: DefaultPaths()}
}

type staticCollector []prometheus.Metric

func (staticCollector) Describe(chan<- *prometheus.Desc) {}

func (s staticCollector) Collect(ch chan<- prometheus.Metric) {
	for _, m := range s {
		ch <- m
	}
}

// gather runs metrics through a registry, which rejects duplicates and
// inconsistent label sets, and returns them keyed as name{label="value",...}.
func gather(t *testing.T, c prometheus.Collector) map[string]float64 {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := map[string]float64{}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			out[metricKey(f.GetName(), m.GetLabel())] = metricValue(m)
		}
	}
	return out
}

func gatherMetrics(t *testing.T, metrics []prometheus.Metric) map[string]float64 {
	t.Helper()
	return gather(t, staticCollector(metrics))
}

func metricKey(name string, labels []*dto.LabelPair) string {
	parts := make([]string, 0, len(labels))
	for _, l := range labels {
		parts = append(parts, l.GetName()+`="`+l.GetValue()+`"`)
	}
	sort.Strings(parts)
	return name + "{" + strings.Join(parts, ",") + "}"
}

func metricValue(m *dto.Metric) float64 {
	switch {
	case m.GetGauge() != nil:
		return m.GetGauge().GetValue()
	case m.GetCounter() != nil:
		return m.GetCounter().GetValue()
	default:
		return m.GetUntyped().GetValue()
	}
}

func want(t *testing.T, got map[string]float64, key string, value float64) {
	t.Helper()
	v, ok := got[key]
	if !ok {
		var near []string
		name := key[:strings.IndexByte(key, '{')]
		for k := range got {
			if strings.HasPrefix(k, name+"{") {
				near = append(near, k)
			}
		}
		sort.Strings(near)
		if len(near) > 5 {
			near = near[:5]
		}
		t.Fatalf("missing %s; same name: %v", key, near)
	}
	if v != value {
		t.Fatalf("%s = %v, want %v", key, v, value)
	}
}

func absent(t *testing.T, got map[string]float64, prefix string) {
	t.Helper()
	for k := range got {
		if strings.HasPrefix(k, prefix) {
			t.Fatalf("unexpected metric %s", k)
		}
	}
}

func count(got map[string]float64, prefix string) int {
	n := 0
	for k := range got {
		if strings.HasPrefix(k, prefix) {
			n++
		}
	}
	return n
}

func collect(t *testing.T, j Job) map[string]float64 {
	t.Helper()
	metrics, err := j.Collect(context.Background())
	if err != nil {
		t.Fatalf("%s: %v", j.Name(), err)
	}
	return gatherMetrics(t, metrics)
}

func newPoolJob(run runner.Runner) *poolJob {
	return &poolJob{run: run, zpool: "zpool", platform: freebsdPlatform(run), logger: discard()}
}
