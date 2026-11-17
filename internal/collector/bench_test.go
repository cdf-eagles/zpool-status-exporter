package collector

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

// benchRunner answers commands from memory without copying the output.
type benchRunner map[string]runner.Result

func (r benchRunner) Run(_ context.Context, name string, args ...string) (runner.Result, error) {
	res, ok := r[name+" "+strings.Join(args, " ")]
	if !ok {
		return runner.Result{ExitCode: -1}, &runner.ExitError{Name: name, Code: -1}
	}
	if res.ExitCode != 0 {
		return res, &runner.ExitError{Name: name, Code: res.ExitCode}
	}
	return res, nil
}

func freebsdBenchRunner(b *testing.B) benchRunner {
	b.Helper()
	run := benchRunner{}
	for cmdline, r := range freebsdFake(b).Responses {
		run[cmdline] = runner.Result{Stdout: []byte(r.Stdout), ExitCode: r.ExitCode}
	}
	return run
}

func benchJobs(b *testing.B) []ScheduledJob {
	b.Helper()
	run := freebsdBenchRunner(b)
	jobs, err := Jobs(Config{
		Runner: run, Platform: freebsdPlatform(run), Paths: DefaultPaths(),
		Interval: 10 * time.Second, DatasetInterval: time.Minute, SmartInterval: time.Minute, Timeout: time.Second,
		Pool: true, IOStat: true, ARC: true, Kstat: true, Datasets: true, SMART: true,
	})
	if err != nil {
		b.Fatal(err)
	}
	return jobs
}

// BenchmarkCollect runs each collector over the FreeBSD fixtures.
func BenchmarkCollect(b *testing.B) {
	ctx := context.Background()
	for _, j := range benchJobs(b) {
		b.Run(j.Job.Name(), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				metrics, err := j.Job.Collect(ctx)
				if err != nil || len(metrics) == 0 {
					b.Fatalf("%d metrics, err %v", len(metrics), err)
				}
			}
		})
	}
}

// BenchmarkCollectAll runs every collector once per iteration, as the
// background schedules do.
func BenchmarkCollectAll(b *testing.B) {
	ctx := context.Background()
	e := NewExporter(nil, benchJobs(b)...)
	b.ReportAllocs()
	for b.Loop() {
		e.RunOnce(ctx)
	}
}

// BenchmarkScrape gathers the collected metrics through a registry, as one
// scrape of /metrics does before encoding.
func BenchmarkScrape(b *testing.B) {
	e := NewExporter(nil, benchJobs(b)...)
	e.RunOnce(context.Background())
	reg := prometheus.NewRegistry()
	reg.MustRegister(e)
	b.ReportAllocs()
	for b.Loop() {
		families, err := reg.Gather()
		if err != nil || len(families) == 0 {
			b.Fatalf("%d families, err %v", len(families), err)
		}
	}
}

// BenchmarkParse measures the text and JSON parsers on their own.
func BenchmarkParse(b *testing.B) {
	status := []byte(fixture(b, "freebsd/zpool-status.json"))
	list := []byte(fixture(b, "freebsd/zpool-list.json"))
	kstat := []byte(fixture(b, "freebsd/sysctl-kstat-zfs.txt"))
	zfsGet := []byte(fixture(b, "freebsd/zfs-get.txt"))
	geom := []byte(fixture(b, "freebsd/geom-disk-list.txt"))
	procKstat := []byte(fixture(b, "linux/kstat/arcstats"))

	b.Run("status", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := parseStatus(status); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("list", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			ls, err := parseList(list)
			if err != nil {
				b.Fatal(err)
			}
			for _, p := range ls.Pools {
				if err := listVdevFrag(p.Vdevs, map[string]flexNum{}); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("sysctl", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if snap := parseSysctlKstats(kstat); len(snap.Misc) == 0 {
				b.Fatal("no kstats")
			}
		}
	})
	b.Run("zfsget", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if len(parseZfsGet(zfsGet)) == 0 {
				b.Fatal("no datasets")
			}
		}
	})
	b.Run("geom", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if len(parseGeomDiskList(geom)) == 0 {
				b.Fatal("no disks")
			}
		}
	})
	b.Run("prockstat", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := parseProcKstat(procKstat); err != nil {
				b.Fatal(err)
			}
		}
	})
}
