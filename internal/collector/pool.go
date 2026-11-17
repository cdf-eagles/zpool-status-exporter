package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

var vdevLabels = []string{"pool", "vdev", "vdev_type", "class", "device", "disk"}

var (
	poolState = gauge("pool_state",
		"Pool state; 1 for the current state, 0 for the others.", "pool", "state")
	poolSize      = gauge("pool_size_bytes", "Pool size.", "pool")
	poolAllocated = gauge("pool_allocated_bytes", "Space allocated in the pool.", "pool")
	poolFree      = gauge("pool_free_bytes", "Free space in the pool.", "pool")
	poolFrag      = gauge("pool_fragmentation_ratio", "Free-space fragmentation of the pool (0 to 1).", "pool")
	poolCapacity  = gauge("pool_capacity_ratio", "Fraction of the pool allocated (0 to 1).", "pool")
	poolDedup     = gauge("pool_dedup_ratio", "Deduplication ratio of the pool.", "pool")
	poolExpand    = gauge("pool_expand_size_bytes", "Unused space the pool could grow into.", "pool")
	poolCheckpt   = gauge("pool_checkpoint_bytes", "Space held by the pool checkpoint.", "pool")
	poolErrors    = gauge("pool_data_errors", "Number of known data errors in the pool.", "pool")
	poolAttention = gauge("pool_needs_attention",
		"Whether zpool status prints a status message for the pool (1) or not (0).", "pool")

	scanState = gauge("pool_scan_state",
		"Current or last scan (scrub, resilver, or error scrub) of the pool; always 1.", "pool", "function", "state")
	scanStart     = gauge("pool_scan_start_time_seconds", "Unix time the current or last scan started.", "pool")
	scanEnd       = gauge("pool_scan_end_time_seconds", "Unix time the last scan finished; absent unless the last scan finished.", "pool")
	scanToExamine = gauge("pool_scan_to_examine_bytes", "Bytes the current or last scan has to examine.", "pool")
	scanExamined  = gauge("pool_scan_examined_bytes", "Bytes examined by the current or last scan.", "pool")
	scanIssued    = gauge("pool_scan_issued_bytes", "Bytes issued by the current or last scan.", "pool")
	scanProcessed = gauge("pool_scan_repaired_bytes", "Bytes repaired by the current or last scan.", "pool")
	scanErrors    = gauge("pool_scan_errors", "Errors found by the current or last scan.", "pool")
	scanPaused    = gauge("pool_scan_paused", "Whether the scan is paused (1) or not (0).", "pool")

	vdevState = gauge("vdev_state",
		"Virtual device (vdev) state; 1 for the current state, 0 for the others.", append(slices.Clone(vdevLabels), "state")...)
	vdevReadErrors     = gauge("vdev_read_errors", "Read errors on the vdev since the last zpool clear.", vdevLabels...)
	vdevWriteErrors    = gauge("vdev_write_errors", "Write errors on the vdev since the last zpool clear.", vdevLabels...)
	vdevChecksumErrors = gauge("vdev_checksum_errors", "Checksum errors on the vdev since the last zpool clear.", vdevLabels...)
	vdevSlowIOs        = gauge("vdev_slow_ios", "Slow I/O operations on a leaf vdev since the last zpool clear.", vdevLabels...)
	vdevAllocated      = gauge("vdev_allocated_bytes", "Space allocated on the vdev.", vdevLabels...)
	vdevSize           = gauge("vdev_size_bytes", "Size of the vdev (physical size for a leaf vdev).", vdevLabels...)
	vdevFrag           = gauge("vdev_fragmentation_ratio", "Free-space fragmentation of a top-level vdev (0 to 1).", vdevLabels...)

	diskInfo = gauge("disk_info",
		"Disk identity from the operating system; always 1.", "disk", "model", "serial", "wwn")
)

var poolStates = []string{"ONLINE", "DEGRADED", "FAULTED", "OFFLINE", "UNAVAIL", "REMOVED", "SUSPENDED"}

type statusOutput struct {
	Pools map[string]statusPool `json:"pools"`
}

type statusPool struct {
	Name       string                 `json:"name"`
	State      string                 `json:"state"`
	ErrorCount flexNum                `json:"error_count"`
	Status     string                 `json:"status"`
	ScanStats  *scanStats             `json:"scan_stats"`
	Vdevs      map[string]*statusVdev `json:"vdevs"`
	Logs       map[string]*statusVdev `json:"logs"`
	L2Cache    map[string]*statusVdev `json:"l2cache"`
	Spares     map[string]*statusVdev `json:"spares"`
	Special    map[string]*statusVdev `json:"special"`
	Dedup      map[string]*statusVdev `json:"dedup"`
}

type scanStats struct {
	Function   string  `json:"function"`
	State      string  `json:"state"`
	StartTime  flexNum `json:"start_time"`
	EndTime    flexNum `json:"end_time"`
	ToExamine  flexNum `json:"to_examine"`
	Examined   flexNum `json:"examined"`
	Issued     flexNum `json:"issued"`
	Processed  flexNum `json:"processed"`
	Errors     flexNum `json:"errors"`
	ScrubPause flexNum `json:"scrub_pause"`
}

type statusVdev struct {
	Name           string                 `json:"name"`
	VdevType       string                 `json:"vdev_type"`
	Path           string                 `json:"path"`
	Class          string                 `json:"class"`
	State          string                 `json:"state"`
	AllocSpace     flexNum                `json:"alloc_space"`
	TotalSpace     flexNum                `json:"total_space"`
	PhysSpace      flexNum                `json:"phys_space"`
	ReadErrors     flexNum                `json:"read_errors"`
	WriteErrors    flexNum                `json:"write_errors"`
	ChecksumErrors flexNum                `json:"checksum_errors"`
	SlowIOs        flexNum                `json:"slow_ios"`
	Vdevs          map[string]*statusVdev `json:"vdevs"`
}

type listOutput struct {
	Pools map[string]listPool `json:"pools"`
}

type listPool struct {
	Name       string                     `json:"name"`
	Properties listPoolProps              `json:"properties"`
	Vdevs      map[string]json.RawMessage `json:"vdevs"`
}

// listPoolProps holds the pool properties the exporter reads.
type listPoolProps struct {
	Size          listProperty `json:"size"`
	Allocated     listProperty `json:"allocated"`
	Free          listProperty `json:"free"`
	Fragmentation listProperty `json:"fragmentation"`
	Capacity      listProperty `json:"capacity"`
	DedupRatio    listProperty `json:"dedupratio"`
	ExpandSize    listProperty `json:"expandsize"`
	Checkpoint    listProperty `json:"checkpoint"`
}

type listProperty struct {
	Value flexNum `json:"value"`
}

type listVdev struct {
	Name       string                     `json:"name"`
	Properties listVdevProps              `json:"properties"`
	Vdevs      map[string]json.RawMessage `json:"vdevs"`
}

// listVdevProps holds the vdev property the exporter reads.
type listVdevProps struct {
	Fragmentation listProperty `json:"fragmentation"`
}

func parseStatus(data []byte) (statusOutput, error) {
	var out statusOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("parse zpool status JSON: %w", err)
	}
	if out.Pools == nil {
		return out, fmt.Errorf("parse zpool status JSON: no %q object", "pools")
	}
	return out, nil
}

// parseList parses zpool list -j. OpenZFS 2.4 prints nothing at all, with
// exit status 0, when no pools are imported; that is an empty list.
func parseList(data []byte) (listOutput, error) {
	var out listOutput
	if len(bytes.TrimSpace(data)) == 0 {
		out.Pools = map[string]listPool{}
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("parse zpool list JSON: %w", err)
	}
	if out.Pools == nil {
		return out, fmt.Errorf("parse zpool list JSON: no %q object", "pools")
	}
	return out, nil
}

// listVdevFrag flattens the vdev tree of zpool list -j into fragmentation by
// vdev name. Allocation-class groups (logs, l2cache, ...) have no name and
// hold their vdevs directly.
func listVdevFrag(raw map[string]json.RawMessage, into map[string]flexNum) error {
	for key, msg := range raw {
		var v listVdev
		if err := json.Unmarshal(msg, &v); err != nil {
			return fmt.Errorf("vdev %q: %w", key, err)
		}
		if v.Name == "" {
			var group map[string]json.RawMessage
			if err := json.Unmarshal(msg, &group); err != nil {
				return fmt.Errorf("vdev group %q: %w", key, err)
			}
			if err := listVdevFrag(group, into); err != nil {
				return err
			}
			continue
		}
		into[v.Name] = v.Properties.Fragmentation.Value
		if err := listVdevFrag(v.Vdevs, into); err != nil {
			return err
		}
	}
	return nil
}

// vdevInfo is the label set of one vdev, shared with the iostat collector.
type vdevInfo struct {
	VdevType string
	Class    string
	Device   string
	Disk     string
}

// topology maps pool and vdev names to their labels.
type topology map[string]map[string]vdevInfo

type flatVdev struct {
	*statusVdev
	class string
}

// flattenPool returns every vdev below the root, depth first, with its
// allocation class.
func flattenPool(p statusPool) []flatVdev {
	var out []flatVdev
	var walk func(m map[string]*statusVdev, class string)
	walk = func(m map[string]*statusVdev, class string) {
		for _, name := range sortedKeys(m) {
			v := m[name]
			if v == nil {
				continue
			}
			c := v.Class
			if c == "" {
				c = class
			}
			if v.VdevType != "root" {
				out = append(out, flatVdev{statusVdev: v, class: c})
			}
			walk(v.Vdevs, c)
		}
	}
	walk(p.Vdevs, "normal")
	walk(p.Logs, "log")
	walk(p.Special, "special")
	walk(p.Dedup, "dedup")
	walk(p.L2Cache, "l2cache")
	walk(p.Spares, "spare")
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func buildTopology(st statusOutput, devices DeviceMap) topology {
	t := topology{}
	for name, p := range st.Pools {
		pv := map[string]vdevInfo{}
		for _, v := range flattenPool(p) {
			if _, seen := pv[v.Name]; seen {
				continue
			}
			info := vdevInfo{VdevType: v.VdevType, Class: v.class}
			if v.Path != "" {
				info.Device, info.Disk = devices.Resolve(v.Path)
			}
			pv[v.Name] = info
		}
		t[name] = pv
	}
	return t
}

// topologyCache holds the latest vdev labels so the iostat collector can
// label its series without running zpool status itself.
type topologyCache struct {
	mu      sync.Mutex
	topo    topology
	updated time.Time
	maxAge  time.Duration
	now     func() time.Time
	refresh func(ctx context.Context) (topology, error)
}

func (c *topologyCache) set(t topology) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.topo = t
	c.updated = c.now()
}

// forceRefresh reads the topology now and caches it.
func (c *topologyCache) forceRefresh(ctx context.Context) (topology, error) {
	fresh, err := c.refresh(ctx)
	if err != nil {
		return nil, err
	}
	c.set(fresh)
	return fresh, nil
}

// get returns the cached topology, refreshing it when it is older than
// maxAge (for example when the pool collector is disabled).
func (c *topologyCache) get(ctx context.Context) (topology, error) {
	c.mu.Lock()
	t, updated := c.topo, c.updated
	c.mu.Unlock()
	if t != nil && c.now().Sub(updated) <= c.maxAge {
		return t, nil
	}
	fresh, err := c.refresh(ctx)
	if err != nil {
		return t, err
	}
	c.set(fresh)
	return fresh, nil
}

// poolJob collects pool, vdev, and scan metrics from zpool status and
// zpool list.
type poolJob struct {
	run      runner.Runner
	zpool    string
	platform Platform
	cache    *topologyCache
	logger   *slog.Logger
}

func (j *poolJob) Name() string { return "pool" }

func (j *poolJob) status(ctx context.Context) (statusOutput, error) {
	res, err := j.run.Run(ctx, j.zpool, "status", "-j", "--json-int", "-p")
	if err != nil {
		return statusOutput{}, err
	}
	return parseStatus(res.Stdout)
}

func (j *poolJob) devices(ctx context.Context) DeviceMap {
	devs, err := j.platform.Devices(ctx)
	if err != nil {
		j.logger.Warn("device inventory failed; device and disk labels will be empty", "err", err)
	}
	return devs
}

func (j *poolJob) topology(ctx context.Context) (topology, error) {
	st, err := j.status(ctx)
	if err != nil {
		return nil, err
	}
	return buildTopology(st, j.devices(ctx)), nil
}

func (j *poolJob) Collect(ctx context.Context) ([]prometheus.Metric, error) {
	st, err := j.status(ctx)
	if err != nil {
		return nil, err
	}
	res, err := j.run.Run(ctx, j.zpool, "list", "-j", "--json-int", "-v")
	if err != nil {
		return nil, err
	}
	ls, err := parseList(res.Stdout)
	if err != nil {
		return nil, err
	}
	if len(ls.Pools) == 0 && len(st.Pools) > 0 {
		return nil, fmt.Errorf("zpool list printed no pools, but zpool status reported %d", len(st.Pools))
	}
	devices := j.devices(ctx)
	topo := buildTopology(st, devices)
	if j.cache != nil {
		j.cache.set(topo)
	}

	var b batch
	b.grow(len(devices.Disks))
	for _, name := range sortedKeys(devices.Disks) {
		d := devices.Disks[name]
		b.add(diskInfo, 1, name, d.Model, d.Serial, d.WWN)
	}
	for _, name := range sortedKeys(st.Pools) {
		p := st.Pools[name]
		lp := ls.Pools[name]
		emitPool(&b, name, p, lp)
		frag := map[string]flexNum{}
		if err := listVdevFrag(lp.Vdevs, frag); err != nil {
			return nil, fmt.Errorf("pool %s: %w", name, err)
		}
		for _, v := range flattenPool(p) {
			emitVdev(&b, name, v, topo[name][v.Name], frag[v.Name])
		}
	}
	return b.metrics, b.err()
}

// statesWith returns the known states, with state added when it is not one
// of them.
func statesWith(state string) []string {
	if state == "" || slices.Contains(poolStates, state) {
		return poolStates
	}
	return append(slices.Clone(poolStates), state)
}

func emitPool(b *batch, name string, p statusPool, lp listPool) {
	for _, s := range statesWith(p.State) {
		b.addBool(poolState, s == p.State, name, s)
	}
	props := lp.Properties
	b.addNum(poolSize, props.Size.Value, name)
	b.addNum(poolAllocated, props.Allocated.Value, name)
	b.addNum(poolFree, props.Free.Value, name)
	b.addNum(poolFrag, props.Fragmentation.Value.div(100), name)
	b.addNum(poolCapacity, props.Capacity.Value.div(100), name)
	b.addNum(poolDedup, props.DedupRatio.Value, name)
	b.addNum(poolExpand, props.ExpandSize.Value, name)
	b.addNum(poolCheckpt, props.Checkpoint.Value, name)
	b.addNum(poolErrors, p.ErrorCount, name)
	b.addBool(poolAttention, p.Status != "", name)

	if s := p.ScanStats; s != nil && s.Function != "" {
		b.add(scanState, 1, name, s.Function, s.State)
		b.addNum(scanStart, s.StartTime, name)
		if s.State == "FINISHED" && s.EndTime.Value > 0 {
			b.addNum(scanEnd, s.EndTime, name)
		}
		b.addNum(scanToExamine, s.ToExamine, name)
		b.addNum(scanExamined, s.Examined, name)
		b.addNum(scanIssued, s.Issued, name)
		b.addNum(scanProcessed, s.Processed, name)
		b.addNum(scanErrors, s.Errors, name)
		b.addBool(scanPaused, s.ScrubPause.Valid && s.ScrubPause.Value != 0, name)
	}
}

func emitVdev(b *batch, pool string, v flatVdev, info vdevInfo, frag flexNum) {
	withState := [...]string{pool, v.Name, v.VdevType, v.class, info.Device, info.Disk, ""}
	labels := withState[:len(vdevLabels)]
	for _, s := range statesWith(v.State) {
		withState[len(vdevLabels)] = s
		b.addBool(vdevState, s == v.State, withState[:]...)
	}
	b.addNum(vdevReadErrors, v.ReadErrors, labels...)
	b.addNum(vdevWriteErrors, v.WriteErrors, labels...)
	b.addNum(vdevChecksumErrors, v.ChecksumErrors, labels...)
	b.addNum(vdevSlowIOs, v.SlowIOs, labels...)
	b.addNum(vdevAllocated, v.AllocSpace, labels...)
	size := v.PhysSpace
	if !size.Valid {
		size = v.TotalSpace
	}
	b.addNum(vdevSize, size, labels...)
	b.addNum(vdevFrag, frag.div(100), labels...)
}
