package collector

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	arcFamily       = dynamic("arc", "ZFS Adaptive Replacement Cache (ARC) kernel statistic")
	poolIOFamily    = dynamic("pool_iostats", "ZFS per-pool I/O kernel statistic", "pool")
	kstatGroupNames = map[string]string{
		"abdstats":          "abd",
		"dbufstats":         "dbuf",
		"dmu_tx":            "dmu_tx",
		"dnodestats":        "dnode",
		"fm":                "fm",
		"vdev_mirror_stats": "vdev_mirror",
		"zfetchstats":       "zfetch",
		"zil":               "zil",
	}
	kstatFamilies = func() map[string]DynamicFamily {
		m := map[string]DynamicFamily{}
		for _, g := range kstatMiscGroups {
			m[g] = dynamic(kstatGroupNames[g], "ZFS "+g+" kernel statistic")
		}
		return m
	}()

	datasetReads     = counter("dataset_reads_total", "Read operations on the dataset.", "pool", "dataset")
	datasetWrites    = counter("dataset_writes_total", "Write operations on the dataset.", "pool", "dataset")
	datasetReadB     = counter("dataset_read_bytes_total", "Bytes read from the dataset.", "pool", "dataset")
	datasetWrittenB  = counter("dataset_written_bytes_total", "Bytes written to the dataset.", "pool", "dataset")
	datasetUnlinks   = counter("dataset_unlinks_total", "Files queued for deletion on the dataset.", "pool", "dataset")
	datasetUnlinked  = counter("dataset_unlinked_total", "Files deleted on the dataset.", "pool", "dataset")
	datasetCounterOf = map[string]*MetricDef{
		"reads":     datasetReads,
		"writes":    datasetWrites,
		"nread":     datasetReadB,
		"nwritten":  datasetWrittenB,
		"nunlinks":  datasetUnlinks,
		"nunlinked": datasetUnlinked,
	}
)

// arcJob exports the ARC statistics.
type arcJob struct{ platform Platform }

func (j *arcJob) Name() string { return "arc" }

func (j *arcJob) Collect(ctx context.Context) ([]prometheus.Metric, error) {
	snap, err := j.platform.ARC(ctx)
	if err != nil {
		return nil, fmt.Errorf("ARC statistics: %w", err)
	}
	var b batch
	stats := snap.Misc["arcstats"]
	for _, name := range sortedKeys(stats) {
		b.addDynamic(arcFamily, name, prometheus.UntypedValue, stats[name])
	}
	return b.metrics, b.err()
}

// kstatJob exports the other misc kstat groups, per-pool I/O statistics, and
// per-dataset I/O counters.
type kstatJob struct{ platform Platform }

func (j *kstatJob) Name() string { return "kstat" }

func (j *kstatJob) Collect(ctx context.Context) ([]prometheus.Metric, error) {
	snap, err := j.platform.Kstats(ctx)
	if err != nil {
		return nil, fmt.Errorf("kernel statistics: %w", err)
	}
	var b batch
	for _, group := range kstatMiscGroups {
		stats := snap.Misc[group]
		for _, name := range sortedKeys(stats) {
			b.addDynamic(kstatFamilies[group], name, prometheus.UntypedValue, stats[name])
		}
	}
	for _, p := range snap.Pools {
		for _, name := range sortedKeys(p.IOStats) {
			b.addDynamic(poolIOFamily, name, prometheus.CounterValue, p.IOStats[name], p.Pool)
		}
		seen := map[string]bool{}
		for _, ds := range p.Datasets {
			if seen[ds.Name] {
				continue
			}
			seen[ds.Name] = true
			for _, stat := range sortedKeys(datasetCounterOf) {
				if v, ok := ds.Stats[stat]; ok {
					b.add(datasetCounterOf[stat], v, p.Pool, ds.Name)
				}
			}
		}
	}
	return b.metrics, b.err()
}
