package collector

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type fakeJob struct {
	name  string
	runs  atomic.Int32
	fail  atomic.Bool
	delay time.Duration
}

var fakeMetric = gauge("test_fake_metric", "Test metric.", "job")

func (j *fakeJob) Name() string { return j.name }

func (j *fakeJob) Collect(ctx context.Context) ([]prometheus.Metric, error) {
	j.runs.Add(1)
	if j.delay > 0 {
		select {
		case <-time.After(j.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if j.fail.Load() {
		return nil, errors.New("boom")
	}
	var b batch
	b.add(fakeMetric, 1, j.name)
	return b.metrics, b.err()
}

func TestExporterRunOnceAndCollect(t *testing.T) {
	good, bad := &fakeJob{name: "good"}, &fakeJob{name: "bad"}
	bad.fail.Store(true)
	e := NewExporter(nil, ScheduledJob{Job: good, Interval: time.Second}, ScheduledJob{Job: bad, Interval: time.Second, Timeout: time.Second})

	if got := gather(t, e); len(got) != 0 {
		t.Fatalf("metrics before the first run: %v", got)
	}
	e.RunOnce(context.Background())
	got := gather(t, e)
	want(t, got, `zfs_test_fake_metric{job="good"}`, 1)
	absent(t, got, `zfs_test_fake_metric{job="bad"}`)
	want(t, got, `zfs_exporter_collector_up{collector="good"}`, 1)
	want(t, got, `zfs_exporter_collector_up{collector="bad"}`, 0)
	absent(t, got, `zfs_exporter_collector_last_success_timestamp_seconds{collector="bad"}`)
	if _, ok := got[`zfs_exporter_collector_last_success_timestamp_seconds{collector="good"}`]; !ok {
		t.Fatal("missing last success timestamp")
	}

	good.fail.Store(true)
	e.RunOnce(context.Background())
	got = gather(t, e)
	absent(t, got, `zfs_test_fake_metric{`)
	want(t, got, `zfs_exporter_collector_up{collector="good"}`, 0)
	if _, ok := got[`zfs_exporter_collector_last_success_timestamp_seconds{collector="good"}`]; !ok {
		t.Fatal("last success timestamp must survive a failed run")
	}
}

func TestExporterTimeout(t *testing.T) {
	slow := &fakeJob{name: "slow", delay: time.Minute}
	e := NewExporter(discard(), ScheduledJob{Job: slow, Interval: time.Second, Timeout: 50 * time.Millisecond})
	start := time.Now()
	e.RunOnce(context.Background())
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout not applied")
	}
	want(t, gather(t, e), `zfs_exporter_collector_up{collector="slow"}`, 0)
}

func TestExporterStartStop(t *testing.T) {
	fast := &fakeJob{name: "fast"}
	busy := &fakeJob{name: "busy", delay: 30 * time.Millisecond}
	e := NewExporter(discard(),
		ScheduledJob{Job: fast, Interval: 10 * time.Millisecond, Timeout: time.Second},
		ScheduledJob{Job: busy, Interval: time.Millisecond, Timeout: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	wg := e.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for (fast.runs.Load() < 3 || busy.runs.Load() < 3) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("jobs did not stop after cancel")
	}
	if fast.runs.Load() < 3 || busy.runs.Load() < 3 {
		t.Fatalf("runs: fast %d busy %d", fast.runs.Load(), busy.runs.Load())
	}
	n := fast.runs.Load()
	e.RunOnce(ctx)
	if fast.runs.Load() != n {
		t.Fatal("RunOnce with a cancelled context must not run jobs")
	}
}

func TestBatchLabelError(t *testing.T) {
	var b batch
	b.add(fakeMetric, 1)
	b.addDynamic(arcFamily, "x", prometheus.GaugeValue, 1, "extra")
	if b.err() == nil || len(b.metrics) != 0 {
		t.Fatalf("label mismatch must be an error: %v", b.err())
	}
	if sanitizeName("erpt-dropped") != "erpt_dropped" || sanitizeName("_a.b_") != "a_b" {
		t.Fatal("sanitizeName")
	}
}
