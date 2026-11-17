package collector

import (
	"bufio"
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// The regular expressions the hand-written sysctl and name parsers replaced;
// the tests below hold the parsers to them.
var (
	refSysctlMisc    = regexp.MustCompile(`^kstat\.zfs\.misc\.([A-Za-z0-9_]+)\.([A-Za-z0-9_-]+): (.*)$`)
	refSysctlIOStats = regexp.MustCompile(`^kstat\.zfs\.(.+)\.misc\.iostats\.([A-Za-z0-9_-]+): (.*)$`)
	refSysctlObjset  = regexp.MustCompile(`^kstat\.zfs\.(.+)\.dataset\.(objset-0x[0-9a-fA-F]+)\.([A-Za-z0-9_-]+): (.*)$`)
	refInvalidName   = regexp.MustCompile(`[^a-zA-Z0-9_]`)
)

func checkSysctlLine(t *testing.T, line string) {
	t.Helper()
	var got []string
	if group, stat, value, ok := sysctlMisc(line); ok {
		got = []string{line, group, stat, value}
	}
	if want := refSysctlMisc.FindStringSubmatch(line); !slices.Equal(got, want) {
		t.Fatalf("sysctlMisc(%q) = %q, want %q", line, got, want)
	}
	got = nil
	if pool, stat, value, ok := sysctlIOStats(line); ok {
		got = []string{line, pool, stat, value}
	}
	if want := refSysctlIOStats.FindStringSubmatch(line); !slices.Equal(got, want) {
		t.Fatalf("sysctlIOStats(%q) = %q, want %q", line, got, want)
	}
	got = nil
	if pool, objset, stat, value, ok := sysctlObjset(line); ok {
		got = []string{line, pool, objset, stat, value}
	}
	if want := refSysctlObjset.FindStringSubmatch(line); !slices.Equal(got, want) {
		t.Fatalf("sysctlObjset(%q) = %q, want %q", line, got, want)
	}
}

var sysctlLineSeeds = []string{
	"",
	"kstat.zfs.",
	"kstat.zfs.misc.arcstats.hits: 12",
	"kstat.zfs.misc.arcstats.hits:12",
	"kstat.zfs.misc.arcstats.hits: ",
	"kstat.zfs.misc.arc-stats.hits: 1",
	"kstat.zfs.misc.fm.erpt-dropped: 0",
	"kstat.zfs.misc.a.b.c: 1",
	"kstat.zfs.misc.a.b:c: 1",
	"kstat.zfs.misc..b: 1",
	"kstat.zfs.misc.iostats.x: 1",
	"kstat.zfs.tank.misc.iostats.trim_bytes_failed: 0",
	"kstat.zfs.my.pool.misc.iostats.x: 1: 2",
	"kstat.zfs..misc.iostats.x: 1",
	"kstat.zfs.misc.iostats.misc.iostats.x: 1",
	"kstat.zfs.a.misc.iostats.misc.iostats.x: 1",
	"kstat.zfs.a.misc.iostats.b.misc.iostats.c d: 1",
	"kstat.zfs.a.misc.iostats.x: 1.misc.iostats.y: 2",
	"kstat.zfs.a.misc.iostats.x: 1.misc.iostats.y z",
	"kstat.zfs.tank.dataset.objset-0x36.dataset_name: tank/home",
	"kstat.zfs.tank.dataset.objset-0x36.nread: 5",
	"kstat.zfs.tank.dataset.objset-0x.nread: 5",
	"kstat.zfs.tank.dataset.objset-0xZZ.nread: 5",
	"kstat.zfs.tank.dataset.objset-0xaF09.n-read: 5",
	"kstat.zfs.a.dataset.b.dataset.objset-0x1.dataset_name: a/b.dataset.objset-0x2.c: d",
	"kstat.zfs.a.dataset.objset-0x1.dataset_name: x.dataset.objset-0x2.c d",
	"kstat.zfs.a.dataset.dataset.objset-0x1.s: v",
	"kstat.zfs.\xff.misc.iostats.x: \xfe",
	" kstat.zfs.misc.arcstats.hits: 12",
	"vfs.zfs.arc_max: 0",
}

func TestSysctlLineParsersMatchRegexps(t *testing.T) {
	for _, line := range sysctlLineSeeds {
		checkSysctlLine(t, line)
	}
	for _, name := range []string{"freebsd/sysctl-kstat-zfs.txt", "freebsd/sysctl-arcstats.txt"} {
		n := 0
		for line := range lines([]byte(fixture(t, name))) {
			checkSysctlLine(t, line)
			n++
		}
		if n < 100 {
			t.Fatalf("%s: only %d lines checked", name, n)
		}
	}
}

func FuzzSysctlLine(f *testing.F) {
	for _, line := range sysctlLineSeeds {
		f.Add(line)
	}
	f.Fuzz(func(t *testing.T, line string) {
		if strings.ContainsAny(line, "\n") {
			t.Skip()
		}
		checkSysctlLine(t, line)
	})
}

func checkSanitizeName(t *testing.T, s string) {
	t.Helper()
	if got, want := sanitizeName(s), strings.Trim(refInvalidName.ReplaceAllString(s, "_"), "_"); got != want {
		t.Fatalf("sanitizeName(%q) = %q, want %q", s, got, want)
	}
}

func FuzzSanitizeName(f *testing.F) {
	for _, s := range []string{"", "hits", "erpt-dropped", "_a.b_", "é", "a\xffb", "__", "A9_z"} {
		f.Add(s)
	}
	f.Fuzz(checkSanitizeName)
}

func checkLines(t *testing.T, data []byte) {
	t.Helper()
	var want []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, len(data)+1)
	for sc.Scan() {
		want = append(want, sc.Text())
	}
	if got := slices.Collect(lines(data)); !slices.Equal(got, want) {
		t.Fatalf("lines(%q) = %q, want %q", data, got, want)
	}
}

func FuzzLines(f *testing.F) {
	for _, s := range []string{"", "\n", "a", "a\n", "a\r\n", "a\r", "\r\n\r\n", "a\n\nb", "a\r\r\nb\n", "\r"} {
		f.Add([]byte(s))
	}
	f.Fuzz(checkLines)
}

func TestLinesStopsEarly(t *testing.T) {
	for line := range lines([]byte("a\nb\n")) {
		if line != "a" {
			t.Fatalf("line = %q", line)
		}
		break
	}
}

func TestDynamicDescCache(t *testing.T) {
	fam := newDynamic("test_cache", "Test family")
	first := fam.desc("hits", prometheus.UntypedValue)
	if fam.desc("hits", prometheus.UntypedValue) != first {
		t.Fatal("the descriptor of a statistic was built twice")
	}
	if fam.desc("hits", prometheus.CounterValue) == first {
		t.Fatal("a counter shares the descriptor of an untyped statistic")
	}
	if got := fam.name("ops_count", prometheus.CounterValue); got != "zfs_test_cache_ops_total" {
		t.Fatalf("counter name = %s", got)
	}

	for i := range maxCachedDescs + 10 {
		fam.desc(strings.Repeat("x", i+1), prometheus.UntypedValue)
	}
	if n := len(fam.descs.m); n != maxCachedDescs {
		t.Fatalf("cache holds %d descriptors, want %d", n, maxCachedDescs)
	}

	var b batch
	b.addDynamic(DynamicFamily{Prefix: "zfs_test_plain", Help: "No cache"}, "a", prometheus.GaugeValue, 1)
	if len(b.metrics) != 1 || b.err() != nil {
		t.Fatalf("family without a cache: %d metrics, err %v", len(b.metrics), b.err())
	}
}

func TestParseZfsGetRows(t *testing.T) {
	got := parseZfsGet([]byte("tank\ttype\tfilesystem\n" +
		"tank\tused\t10\textra\n" +
		"tank/a\tused\t1\n" +
		"short\tline\n" +
		"tank\tused\t20\n" +
		"tank\tunknown\t5\n" +
		"tank/a\ttype\tvolume\r\n" +
		"tank/a\tquota\t-\n"))
	if len(got) != 2 || got[0].name != "tank" || got[1].name != "tank/a" {
		t.Fatalf("datasets = %+v", got)
	}
	used, quota := datasetPropertyIndex["used"], datasetPropertyIndex["quota"]
	if got[0].typ != "filesystem" || got[0].values[used] != (flexNum{Value: 20, Valid: true}) {
		t.Fatalf("tank = %+v", got[0])
	}
	if got[1].typ != "volume" || got[1].values[used] != (flexNum{Value: 1, Valid: true}) || got[1].values[quota].Valid {
		t.Fatalf("tank/a = %+v", got[1])
	}
}
