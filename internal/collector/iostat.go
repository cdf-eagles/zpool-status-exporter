package collector

import (
	"bufio"
	"bytes"
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

const iostatFirstLatencyColumn = 7

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
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
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
	for _, r := range rows {
		var labels []string
		ops, bw := [2]*MetricDef{vdevReadOps, vdevWriteOps}, [2]*MetricDef{vdevReadBytes, vdevWriteBytes}
		lat := vdevLatency
		if r.isPool {
			labels = []string{r.pool}
			ops, bw = [2]*MetricDef{poolReadOps, poolWriteOps}, [2]*MetricDef{poolReadBytes, poolWriteBytes}
			lat = poolLatency
		} else {
			info := topo[r.pool][r.name]
			labels = []string{r.pool, r.name, info.VdevType, info.Class, info.Device, info.Disk}
		}
		for i, d := range []*MetricDef{ops[0], ops[1], bw[0], bw[1]} {
			if v, ok := iostatValue(r.values, 3+i); ok {
				b.add(d, v, labels...)
			}
		}
		for i, c := range latencyColumns {
			if v, ok := iostatValue(r.values, iostatFirstLatencyColumn+i); ok {
				b.add(lat, v/1e9, append(slices.Clone(labels), c.op, c.queue)...)
			}
		}
	}
	return b.metrics, b.err()
}
