package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

var fixedTime = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func newIostatJob(t *testing.T, run runner.Runner) *iostatJob {
	t.Helper()
	pool := newPoolJob(run)
	return &iostatJob{run: run, zpool: "zpool", interval: 10 * time.Second,
		cache: &topologyCache{maxAge: time.Minute, now: fixedNow, refresh: pool.topology}}
}

func TestIostatCollectorFreeBSD(t *testing.T) {
	run := freebsdFake(t)
	got := collect(t, newIostatJob(t, run))

	want(t, got, `zfs_pool_read_operations_per_second{pool="datapool"}`, 10)
	want(t, got, `zfs_pool_write_operations_per_second{pool="datapool"}`, 65)
	want(t, got, `zfs_pool_read_bytes_per_second{pool="datapool"}`, 97203)
	want(t, got, `zfs_pool_write_bytes_per_second{pool="datapool"}`, 6682755)
	want(t, got, `zfs_pool_latency_seconds{op="read",pool="datapool",queue="total"}`, 0.002109067)
	want(t, got, `zfs_pool_latency_seconds{op="write",pool="datapool",queue="syncq"}`, 0.000208898)
	absent(t, got, `zfs_pool_latency_seconds{op="any",pool="datapool"`)
	absent(t, got, `zfs_pool_latency_seconds{op="read",pool="rpool"`)

	want(t, got, vdevKey("zfs_vdev_read_operations_per_second", "datapool", "mirror-0", "mirror", "normal", "", ""), 10)
	want(t, got, vdevKey("zfs_vdev_read_operations_per_second", "rpool", "mirror-0", "mirror", "normal", "", ""), 0)
	want(t, got, vdevKey("zfs_vdev_read_bytes_per_second", "datapool", "gpt/chassis02", "disk", "normal", "ada2p1", "ada2"), 60752)
	want(t, got, vdevKey("zfs_vdev_latency_seconds", "datapool", "gpt/chassis10", "disk", "log", "da10p1", "da10", `op="write"`, `queue="disk"`), 0.001574353)
	absent(t, got, `zfs_vdev_latency_seconds{class="log",device="da10p1",disk="da10",op="read"`)
	want(t, got, vdevKey("zfs_vdev_read_operations_per_second", "nvme-podman03", "gpt/nvme-podman03", "disk", "normal", "nda0p2", "nda0"), 0)

	if n := count(got, "zfs_pool_read_operations_per_second{"); n != 6 {
		t.Fatalf("pool rows = %d, want 6", n)
	}
	calls := strings.Join(run.Calls(), "\n")
	if !strings.Contains(calls, cmdIostat) {
		t.Fatalf("iostat command not run: %s", calls)
	}
}

func TestIostatSeconds(t *testing.T) {
	j := &iostatJob{interval: 500 * time.Millisecond}
	if j.seconds() != "1" {
		t.Fatalf("seconds = %s", j.seconds())
	}
	j.interval = 30 * time.Second
	if j.seconds() != "30" {
		t.Fatalf("seconds = %s", j.seconds())
	}
}

func TestParseIostatErrors(t *testing.T) {
	topo := topology{"tank": {"mirror-0": {}}}
	if _, err := parseIostat([]byte("tank\t1\t2\n"), topo); err == nil || !strings.Contains(err.Error(), "short line") {
		t.Fatalf("short line: %v", err)
	}
	if _, err := parseIostat([]byte("mirror-0\t1\t2\t3\t4\t5\t6\n"), topo); err == nil || !strings.Contains(err.Error(), "before any known pool") {
		t.Fatalf("orphan row: %v", err)
	}
	rows, err := parseIostat([]byte("tank\t1\t2\t3\t4\t5\t6\n\nmirror-0\t1\t2\t3\t4\t5\t6\n"), topo)
	if err != nil || len(rows) != 2 || !rows[0].isPool || rows[1].pool != "tank" {
		t.Fatalf("rows = %+v, err %v", rows, err)
	}
	if _, err := parseIostat([]byte("tank\t1\t2\t3\t4\t5\t6\nnew-vdev\t1\t2\t3\t4\t5\t6\n"), topo); !errors.Is(err, errUnknownRow) {
		t.Fatalf("unknown vdev: %v", err)
	}
	if _, ok := iostatValue([]string{"1"}, 0); ok {
		t.Fatal("column 0 must be invalid")
	}
}

func TestIostatCollectorErrors(t *testing.T) {
	f := freebsdFake(t)
	f.Set(cmdIostat, runner.Response{ExitCode: 1, Stderr: "cannot open"})
	if _, err := newIostatJob(t, f).Collect(context.Background()); err == nil {
		t.Fatal("want iostat error")
	}
	f = freebsdFake(t)
	f.Set(cmdIostat, runner.Response{Stdout: "short\n"})
	if _, err := newIostatJob(t, f).Collect(context.Background()); err == nil {
		t.Fatal("want parse error")
	}
	f = freebsdFake(t)
	f.Set(cmdStatus, runner.Response{Err: errors.New("exec failed")})
	_, err := newIostatJob(t, f).Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "pool topology") {
		t.Fatalf("want topology error, got %v", err)
	}
}

// A pool imported after the topology was cached must not have its rows
// filed under the previous pool.
func TestIostatRefreshesStaleTopology(t *testing.T) {
	out := "tank\t1\t2\t3\t4\t5\t6\nmirror-0\t1\t2\t7\t4\t5\t6\n" +
		"backup\t1\t2\t3\t4\t5\t6\nmirror-0\t1\t2\t9\t4\t5\t6\n"
	f := runner.NewFake(map[string]runner.Response{cmdIostat: {Stdout: out}})
	stale := topology{"tank": {"mirror-0": {VdevType: "mirror", Class: "normal"}}}
	full := topology{"tank": stale["tank"], "backup": {"mirror-0": {VdevType: "mirror", Class: "normal"}}}
	refreshes := 0
	cache := &topologyCache{maxAge: time.Hour, now: fixedNow, refresh: func(context.Context) (topology, error) {
		refreshes++
		return full, nil
	}}
	cache.set(stale)
	j := &iostatJob{run: f, zpool: "zpool", interval: 10 * time.Second, cache: cache}
	got := collect(t, j)
	if refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", refreshes)
	}
	want(t, got, vdevKey("zfs_vdev_read_operations_per_second", "backup", "mirror-0", "mirror", "normal", "", ""), 9)
	want(t, got, vdevKey("zfs_vdev_read_operations_per_second", "tank", "mirror-0", "mirror", "normal", "", ""), 7)

	cache.refresh = func(context.Context) (topology, error) { return stale, nil }
	cache.set(stale)
	if _, err := j.Collect(context.Background()); !errors.Is(err, errUnknownRow) {
		t.Fatalf("a row still unknown after a refresh must fail the run: %v", err)
	}
	cache.refresh = func(context.Context) (topology, error) { return nil, errors.New("down") }
	if _, err := j.Collect(context.Background()); err == nil || !strings.Contains(err.Error(), "pool topology") {
		t.Fatalf("refresh failure: %v", err)
	}
}

func TestTopologyCacheAge(t *testing.T) {
	now := fixedTime
	refreshes := 0
	c := &topologyCache{maxAge: time.Minute, now: func() time.Time { return now },
		refresh: func(context.Context) (topology, error) {
			refreshes++
			return topology{"p": {}}, nil
		}}
	ctx := context.Background()
	if _, err := c.get(ctx); err != nil || refreshes != 1 {
		t.Fatalf("first get: refreshes %d err %v", refreshes, err)
	}
	if _, err := c.get(ctx); err != nil || refreshes != 1 {
		t.Fatalf("cached get: refreshes %d", refreshes)
	}
	now = now.Add(2 * time.Minute)
	if _, err := c.get(ctx); err != nil || refreshes != 2 {
		t.Fatalf("stale get: refreshes %d", refreshes)
	}
	c.refresh = func(context.Context) (topology, error) { return nil, errors.New("down") }
	now = now.Add(2 * time.Minute)
	topo, err := c.get(ctx)
	if err == nil || topo == nil {
		t.Fatalf("failed refresh must return the stale topology and the error: %v %v", topo, err)
	}
}
