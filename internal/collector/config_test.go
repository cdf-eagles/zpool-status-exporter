package collector

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

func validConfig() Config {
	run := runner.NewFake(nil)
	return Config{
		Runner: run, Platform: freebsdPlatform(run), Paths: DefaultPaths(),
		Interval: 10 * time.Second, DatasetInterval: time.Minute, SmartInterval: 10 * time.Minute, Timeout: 30 * time.Second,
		Pool: true, IOStat: true, ARC: true, Kstat: true, Datasets: true, SMART: true,
	}
}

func TestJobsAllEnabled(t *testing.T) {
	jobs, err := Jobs(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ScheduledJob{}
	for _, j := range jobs {
		got[j.Job.Name()] = j
	}
	for name, interval := range map[string]time.Duration{
		"pool": 10 * time.Second, "iostat": 10 * time.Second, "arc": 10 * time.Second, "kstat": 10 * time.Second,
		"datasets": time.Minute, "smart": 10 * time.Minute,
	} {
		if got[name].Interval != interval {
			t.Fatalf("%s interval = %s, want %s", name, got[name].Interval, interval)
		}
	}
	if got["iostat"].Timeout != 40*time.Second {
		t.Fatalf("iostat timeout = %s", got["iostat"].Timeout)
	}
}

func TestJobsDisabled(t *testing.T) {
	cfg := validConfig()
	cfg.SMART, cfg.ARC = false, false
	jobs, err := Jobs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.Job.Name() == "smart" || j.Job.Name() == "arc" {
			t.Fatalf("%s must be disabled", j.Job.Name())
		}
	}
	if len(jobs) != 4 {
		t.Fatalf("jobs = %d", len(jobs))
	}
}

func TestJobsValidation(t *testing.T) {
	cases := map[string]func(*Config){
		"no runner":           func(c *Config) { c.Runner = nil },
		"no platform":         func(c *Config) { c.Platform = nil },
		"short interval":      func(c *Config) { c.Interval = 500 * time.Millisecond },
		"fractional interval": func(c *Config) { c.Interval = 1500 * time.Millisecond },
		"zero timeout":        func(c *Config) { c.Timeout = 0 },
		"all disabled": func(c *Config) {
			c.Pool, c.IOStat, c.ARC, c.Kstat, c.Datasets, c.SMART = false, false, false, false, false, false
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			mutate(&cfg)
			if _, err := Jobs(cfg); err == nil || !strings.HasPrefix(err.Error(), "collector:") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestDefinitions(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Definitions() {
		if seen[d.Name] {
			t.Fatalf("duplicate definition %s", d.Name)
		}
		seen[d.Name] = true
		if !strings.HasPrefix(d.Name, "zfs_") || d.Help == "" {
			t.Fatalf("bad definition %+v", d)
		}
	}
	if len(DynamicFamilies()) == 0 {
		t.Fatal("no dynamic families")
	}
}

func TestFullExporterFreeBSD(t *testing.T) {
	run := freebsdFake(t)
	cfg := validConfig()
	cfg.Runner, cfg.Platform = run, freebsdPlatform(run)
	cfg.now = fixedNow
	jobs, err := Jobs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e := NewExporter(discard(), jobs...)
	e.RunOnce(context.Background())
	got := gather(t, e)
	for _, name := range []string{"pool", "iostat", "arc", "kstat", "datasets", "smart"} {
		want(t, got, `zfs_exporter_collector_up{collector="`+name+`"}`, 1)
	}
	if len(got) < 3000 {
		t.Fatalf("only %d series", len(got))
	}
}
