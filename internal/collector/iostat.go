package collector

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

var (
	poolReadOps    = gauge("pool_read_operations_per_second", "Read operations per second over the last interval.", "pool")
	poolWriteOps   = gauge("pool_write_operations_per_second", "Write operations per second over the last interval.", "pool")
	poolReadBytes  = gauge("pool_read_bytes_per_second", "Bytes read per second over the last interval.", "pool")
	poolWriteBytes = gauge("pool_write_bytes_per_second", "Bytes written per second over the last interval.", "pool")
	poolLatency    = gauge("pool_latency_seconds",
		"Average I/O wait over the last interval, by operation and queue.", "pool", "op", "queue")

	vdevReadOps    = gauge("vdev_read_operations_per_second", "Read operations per second over the last interval.", vdevLabels...)
	vdevWriteOps   = gauge("vdev_write_operations_per_second", "Write operations per second over the last interval.", vdevLabels...)
	vdevReadBytes  = gauge("vdev_read_bytes_per_second", "Bytes read per second over the last interval.", vdevLabels...)
	vdevWriteBytes = gauge("vdev_write_bytes_per_second", "Bytes written per second over the last interval.", vdevLabels...)
	vdevLatency    = gauge("vdev_latency_seconds",
		"Average I/O wait over the last interval, by operation and queue.", append(slices.Clone(vdevLabels), "op", "queue")...)

	// The rate metrics follow the operations and bandwidth columns.
	poolRates = [...]*MetricDef{poolReadOps, poolWriteOps, poolReadBytes, poolWriteBytes}
	vdevRates = [...]*MetricDef{vdevReadOps, vdevWriteOps, vdevReadBytes, vdevWriteBytes}
)

// latencyColumns maps zpool iostat -l columns (after the name, capacity,
// operations, and bandwidth columns) to their operation and queue labels.
var latencyColumns = []struct{ op, queue string }{
	{"read", "total"}, {"write", "total"},
	{"read", "disk"}, {"write", "disk"},
	{"read", "syncq"}, {"write", "syncq"},
	{"read", "asyncq"}, {"write", "asyncq"},
	{"any", "scrub"}, {"any", "trim"}, {"any", "rebuild"},
}

const (
	iostatFirstRateColumn    = 3
	iostatFirstLatencyColumn = 7
)

type iostatRow struct {
	pool   string
	name   string
	isPool bool
	values []string
}

// errUnknownRow reports an iostat row the topology does not know, such as a
// vdev of a pool imported after the topology was read.
var errUnknownRow = errors.New("zpool iostat: row not in the pool topology")

// parseIostat splits zpool iostat -Hpvl output into rows tagged with their
// pool. A row starts a new pool when its name is a known pool and is not a
// vdev of the pool being read; any other row must be a known vdev of the
// current pool.
func parseIostat(data []byte, topo topology) ([]iostatRow, error) {
	var rows []iostatRow
	current := ""
	for line := range lines(data) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < iostatFirstLatencyColumn {
			return nil, fmt.Errorf("zpool iostat: short line %q", line)
		}
		name := f[0]
		_, isPool := topo[name]
		_, inCurrent := topo[current][name]
		if isPool && (current == "" || !inCurrent) {
			current = name
			rows = append(rows, iostatRow{pool: name, name: name, isPool: true, values: f[1:]})
			continue
		}
		if current == "" {
			return nil, fmt.Errorf("zpool iostat: row %q before any known pool", name)
		}
		if !inCurrent {
			return nil, fmt.Errorf("%w: %q after pool %q", errUnknownRow, name, current)
		}
		rows = append(rows, iostatRow{pool: current, name: name, values: f[1:]})
	}
	return rows, nil
}

func iostatValue(values []string, column int) (float64, bool) {
	idx := column - 1
	if idx < 0 || idx >= len(values) {
		return 0, false
	}
	v, err := strconv.ParseFloat(values[idx], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// iostatJob samples zpool iostat over one interval per run, so consecutive
// runs cover time continuously.
type iostatJob struct {
	run      runner.Runner
	zpool    string
	interval time.Duration
	cache    *topologyCache
}

func (j *iostatJob) Name() string { return "iostat" }

func (j *iostatJob) seconds() string {
	s := int(j.interval / time.Second)
	if s < 1 {
		s = 1
	}
	return strconv.Itoa(s)
}

func (j *iostatJob) Collect(ctx context.Context) ([]prometheus.Metric, error) {
	topo, err := j.cache.get(ctx)
	if err != nil {
		return nil, fmt.Errorf("pool topology: %w", err)
	}
	res, err := j.run.Run(ctx, j.zpool, "iostat", "-Hpvl", "-y", j.seconds(), "1")
	if err != nil {
		return nil, err
	}
	rows, err := parseIostat(res.Stdout, topo)
	if errors.Is(err, errUnknownRow) {
		if topo, err = j.cache.forceRefresh(ctx); err != nil {
			return nil, fmt.Errorf("pool topology: %w", err)
		}
		rows, err = parseIostat(res.Stdout, topo)
	}
	if err != nil {
		return nil, err
	}
	var b batch
	b.grow(len(rows) * (len(poolRates) + len(latencyColumns)))
	for _, r := range rows {
		rates, lat := poolRates, poolLatency
		// The capacity leaves room for the op and queue labels of a latency.
		labels := make([]string, 1, len(vdevLabels)+2)
		labels[0] = r.pool
		if !r.isPool {
			info := topo[r.pool][r.name]
			rates, lat = vdevRates, vdevLatency
			labels = append(labels, r.name, info.VdevType, info.Class, info.Device, info.Disk)
		}
		for i, d := range rates {
			if v, ok := iostatValue(r.values, iostatFirstRateColumn+i); ok {
				b.add(d, v, labels...)
			}
		}
		latLabels := labels[:len(labels)+2]
		for i, c := range latencyColumns {
			if v, ok := iostatValue(r.values, iostatFirstLatencyColumn+i); ok {
				latLabels[len(labels)], latLabels[len(labels)+1] = c.op, c.queue
				b.add(lat, v/1e9, latLabels...)
			}
		}
	}
	return b.metrics, b.err()
}
