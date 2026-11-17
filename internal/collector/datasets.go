package collector

import (
	"context"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

var datasetLabels = []string{"pool", "dataset", "type"}

// datasetProperties maps zfs get properties to their metrics, in the order
// they are requested.
var datasetProperties = [...]struct {
	prop string
	def  *MetricDef
}{
	{"used", gauge("dataset_used_bytes", "Space used by the dataset and its descendants.", datasetLabels...)},
	{"available", gauge("dataset_available_bytes", "Space available to the dataset.", datasetLabels...)},
	{"referenced", gauge("dataset_referenced_bytes", "Data referenced by the dataset.", datasetLabels...)},
	{"logicalused", gauge("dataset_logical_used_bytes", "Space used before compression.", datasetLabels...)},
	{"logicalreferenced", gauge("dataset_logical_referenced_bytes", "Data referenced before compression.", datasetLabels...)},
	{"usedbysnapshots", gauge("dataset_used_by_snapshots_bytes", "Space used by snapshots of the dataset.", datasetLabels...)},
	{"usedbydataset", gauge("dataset_used_by_dataset_bytes", "Space used by the dataset itself.", datasetLabels...)},
	{"usedbychildren", gauge("dataset_used_by_children_bytes", "Space used by descendants of the dataset.", datasetLabels...)},
	{"usedbyrefreservation", gauge("dataset_used_by_refreservation_bytes", "Space used by the dataset's refreservation.", datasetLabels...)},
	{"quota", gauge("dataset_quota_bytes", "Quota of the dataset; 0 means none.", datasetLabels...)},
	{"refquota", gauge("dataset_refquota_bytes", "Reference quota of the dataset; 0 means none.", datasetLabels...)},
	{"reservation", gauge("dataset_reservation_bytes", "Reservation of the dataset.", datasetLabels...)},
	{"refreservation", gauge("dataset_refreservation_bytes", "Reference reservation of the dataset.", datasetLabels...)},
	{"volsize", gauge("dataset_volume_size_bytes", "Logical size of a volume.", datasetLabels...)},
	{"written", gauge("dataset_written_since_snapshot_bytes", "Data written since the latest snapshot.", datasetLabels...)},
	{"compressratio", gauge("dataset_compress_ratio", "Compression ratio achieved for the dataset.", datasetLabels...)},
}

func datasetPropertyList() string {
	props := make([]string, 0, 1+len(datasetProperties))
	props = append(props, "type")
	for _, p := range datasetProperties {
		props = append(props, p.prop)
	}
	return strings.Join(props, ",")
}

// datasetPropertyIndex maps a property to its place in datasetProperties.
var datasetPropertyIndex = func() map[string]int {
	m := make(map[string]int, len(datasetProperties))
	for i, p := range datasetProperties {
		m[p.prop] = i
	}
	return m
}()

// datasetValues holds one dataset's type and its properties in the order of
// datasetProperties.
type datasetValues struct {
	name   string
	typ    string
	values [len(datasetProperties)]flexNum
}

// parseZfsGet reads the name, property, and value columns of zfs get -Hp.
func parseZfsGet(data []byte) []datasetValues {
	var out []datasetValues
	index := map[string]int{}
	for line := range lines(data) {
		name, rest, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		prop, value, ok := strings.Cut(rest, "\t")
		if !ok {
			continue
		}
		value, _, _ = strings.Cut(value, "\t")
		i := len(out) - 1
		if i < 0 || out[i].name != name {
			if i, ok = index[name]; !ok {
				i = len(out)
				name = strings.Clone(name)
				index[name] = i
				out = append(out, datasetValues{name: name})
			}
		}
		if p, ok := datasetPropertyIndex[prop]; ok {
			out[i].values[p] = parseFlex(value)
		} else if prop == "type" {
			out[i].typ = strings.Clone(value)
		}
	}
	return out
}

// datasetJob exports dataset space properties from zfs get.
type datasetJob struct {
	run runner.Runner
	zfs string
}

func (j *datasetJob) Name() string { return "datasets" }

func (j *datasetJob) Collect(ctx context.Context) ([]prometheus.Metric, error) {
	res, err := j.run.Run(ctx, j.zfs, "get", "-Hp", "-o", "name,property,value",
		"-t", "filesystem,volume", datasetPropertyList())
	if err != nil {
		return nil, err
	}
	datasets := parseZfsGet(res.Stdout)
	var b batch
	b.grow(len(datasets) * len(datasetProperties))
	for _, ds := range datasets {
		pool, _, _ := strings.Cut(ds.name, "/")
		labels := []string{pool, ds.name, ds.typ}
		for i, p := range datasetProperties {
			b.addNum(p.def, ds.values[i], labels...)
		}
	}
	return b.metrics, b.err()
}
