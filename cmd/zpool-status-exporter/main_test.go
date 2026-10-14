package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/promslog"

	"github.com/cdf-eagles/zpool-status-exporter/internal/collector"
)

func TestDefaults(t *testing.T) {
	app, o := newApp(io.Discard, io.Discard)
	if _, err := app.Parse(nil); err != nil {
		t.Fatal(err)
	}
	c := o.collect
	if !c.Pool || !c.IOStat || !c.ARC || !c.Kstat || !c.Datasets || !c.SMART {
		t.Fatalf("collectors must default on: %+v", c)
	}
	if c.Interval != 10*time.Second || c.DatasetInterval != time.Minute || c.SmartInterval != 10*time.Minute || c.Timeout != 30*time.Second {
		t.Fatalf("intervals: %+v", c)
	}
	if got := (*o.web.WebListenAddresses)[0]; got != ":10058" {
		t.Fatalf("listen address = %s", got)
	}
	if o.telemetryPath != "/metrics" || c.Paths.Zpool != "zpool" || c.Paths.Kstat != "/proc/spl/kstat/zfs" {
		t.Fatalf("paths: %+v %s", c.Paths, o.telemetryPath)
	}
}

func TestFlags(t *testing.T) {
	app, o := newApp(io.Discard, io.Discard)
	_, err := app.Parse([]string{
		"--no-collector.smart", "--no-collector.arc", "--web.listen-address=:9999",
		"--collect.interval=5s", "--path.zpool=/sbin/zpool", "--web.telemetry-path=/m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.collect.SMART || o.collect.ARC || !o.collect.Pool {
		t.Fatalf("collector flags: %+v", o.collect)
	}
	if (*o.web.WebListenAddresses)[0] != ":9999" || o.collect.Interval != 5*time.Second ||
		o.collect.Paths.Zpool != "/sbin/zpool" || o.telemetryPath != "/m" {
		t.Fatalf("flags not applied: %+v", o)
	}
}

func TestHelpListsFlags(t *testing.T) {
	var out bytes.Buffer
	app, _ := newApp(&out, io.Discard)
	app.Terminate(nil)
	_, _ = app.Parse([]string{"--help"})
	for _, f := range []string{"--[no-]collector.smart", "--[no-]collector.arc", "--web.listen-address", "--collect.smart-interval"} {
		if !strings.Contains(out.String(), f) {
			t.Fatalf("help lacks %s:\n%s", f, out.String())
		}
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"--bogus"}, io.Discard, &stderr); code != 2 {
		t.Fatalf("unknown flag: code %d", code)
	}
	if !strings.Contains(stderr.String(), "bogus") {
		t.Fatalf("stderr: %s", stderr.String())
	}
	if code := run(context.Background(), []string{"--collect.interval=1500ms"}, io.Discard, io.Discard); code != 2 {
		t.Fatalf("fractional interval: code %d", code)
	}
	for _, p := range []string{"metrics", "/healthz"} {
		if code := run(context.Background(), []string{"--web.telemetry-path=" + p}, io.Discard, io.Discard); code != 2 {
			t.Fatalf("telemetry path %q: code %d", p, code)
		}
	}
}

func TestRunServesAndShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"--web.listen-address=127.0.0.1:0", "--path.zpool=/nonexistent/zpool",
			"--path.zfs=/nonexistent/zfs", "--path.smartctl=/nonexistent/smartctl", "--path.lsblk=/nonexistent/lsblk",
			"--path.sysctl=/nonexistent/sysctl", "--path.kstat=/nonexistent"}, io.Discard, io.Discard)
	}()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not stop")
	}
}

func TestRunListenFailure(t *testing.T) {
	code := run(context.Background(), []string{"--web.listen-address=256.0.0.1:1", "--no-collector.smart"}, io.Discard, io.Discard)
	if code != 1 {
		t.Fatalf("exit code %d", code)
	}
}

type oneMetric struct{}

var testDesc = prometheus.NewDesc("zfs_test_up", "test", nil, nil)

func (oneMetric) Describe(ch chan<- *prometheus.Desc) { ch <- testDesc }
func (oneMetric) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(testDesc, prometheus.GaugeValue, 1)
}

func TestHandlers(t *testing.T) {
	reg, err := newRegistry(oneMetric{})
	if err != nil {
		t.Fatal(err)
	}
	mux, err := newMux(reg, "/metrics", promslog.NewNopLogger())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := get(t, srv.URL+"/metrics")
	for _, s := range []string{"zfs_test_up 1", "zpool_status_exporter_build_info", "go_goroutines"} {
		if !strings.Contains(body, s) {
			t.Fatalf("/metrics lacks %s", s)
		}
	}
	if get(t, srv.URL+"/healthz") != "ok\n" {
		t.Fatal("/healthz")
	}
	if !strings.Contains(get(t, srv.URL+"/"), "/metrics") {
		t.Fatal("landing page lacks the metrics link")
	}

	if _, err := newRegistry(oneMetric{}); err != nil {
		t.Fatal(err)
	}
	reg2 := prometheus.NewRegistry()
	reg2.MustRegister(oneMetric{})
	if _, err := newMux(reg2, "/", promslog.NewNopLogger()); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryRejectsDuplicate(t *testing.T) {
	if _, err := newRegistry(prometheus.NewGoCollector()); err == nil { //nolint:staticcheck // A duplicate Go collector is the point of the test.
		t.Fatal("duplicate collector must fail")
	}
}

func TestExporterWiring(t *testing.T) {
	e := collector.NewExporter(nil)
	if _, err := newRegistry(e); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d", url, resp.StatusCode)
	}
	return string(b)
}
