// Package collector gathers ZFS, kernel statistics, and Self-Monitoring,
// Analysis and Reporting Technology (SMART) data and exposes it as
// Prometheus metrics.
package collector

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "zfs"

// MetricDef describes one fixed metric family.
type MetricDef struct {
	Name      string
	Help      string
	Labels    []string
	ValueType prometheus.ValueType
	desc      *prometheus.Desc
}

// DynamicFamily describes metric names built at run time from kernel
// statistic names, documented by their prefix.
type DynamicFamily struct {
	Prefix string
	Help   string
	Labels []string
	descs  *descCache
}

type descKey struct {
	stat string
	vt   prometheus.ValueType
}

// descCache keeps the descriptors of one dynamic family between collections.
type descCache struct {
	mu sync.Mutex
	m  map[descKey]*prometheus.Desc
}

// maxCachedDescs bounds a descCache; statistics past it get a descriptor
// built on every collection.
const maxCachedDescs = 4096

var (
	registry        []*MetricDef
	dynamicFamilies []DynamicFamily
)

func def(name string, vt prometheus.ValueType, help string, labels ...string) *MetricDef {
	full := namespace + "_" + name
	d := &MetricDef{
		Name:      full,
		Help:      help,
		Labels:    labels,
		ValueType: vt,
		desc:      prometheus.NewDesc(full, help, labels, nil),
	}
	registry = append(registry, d)
	return d
}

func gauge(name, help string, labels ...string) *MetricDef {
	return def(name, prometheus.GaugeValue, help, labels...)
}

func counter(name, help string, labels ...string) *MetricDef {
	return def(name, prometheus.CounterValue, help, labels...)
}

func newDynamic(prefix, help string, labels ...string) DynamicFamily {
	return DynamicFamily{
		Prefix: namespace + "_" + prefix, Help: help, Labels: labels,
		descs: &descCache{m: map[descKey]*prometheus.Desc{}},
	}
}

func dynamic(prefix, help string, labels ...string) DynamicFamily {
	f := newDynamic(prefix, help, labels...)
	dynamicFamilies = append(dynamicFamilies, f)
	return f
}

// Definitions returns every fixed metric family the exporter can emit.
func Definitions() []*MetricDef { return append([]*MetricDef(nil), registry...) }

// DynamicFamilies returns the metric families whose names come from kernel
// statistics.
func DynamicFamilies() []DynamicFamily { return append([]DynamicFamily(nil), dynamicFamilies...) }

// sanitizeName turns a kernel statistic name into a valid metric name part.
func sanitizeName(s string) string {
	valid := func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}
	return strings.Trim(strings.Map(valid, s), "_")
}

// name returns the metric name of a statistic in the family.
func (f DynamicFamily) name(stat string, vt prometheus.ValueType) string {
	name := f.Prefix + "_" + sanitizeName(stat)
	if vt == prometheus.CounterValue {
		name = strings.TrimSuffix(name, "_count") + "_total"
	}
	return name
}

// desc returns the descriptor of a statistic in the family.
func (f DynamicFamily) desc(stat string, vt prometheus.ValueType) *prometheus.Desc {
	newDesc := func() *prometheus.Desc {
		return prometheus.NewDesc(f.name(stat, vt), f.Help+" ("+stat+")", f.Labels, nil)
	}
	c := f.descs
	if c == nil {
		return newDesc()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.m[descKey{stat, vt}]
	if !ok {
		d = newDesc()
		if len(c.m) < maxCachedDescs {
			c.m[descKey{strings.Clone(stat), vt}] = d
		}
	}
	return d
}

// batch accumulates metrics for one collection and remembers label errors
// instead of panicking.
type batch struct {
	metrics []prometheus.Metric
	errs    []error
}

// grow makes room for n more metrics.
func (b *batch) grow(n int) { b.metrics = slices.Grow(b.metrics, n) }

func (b *batch) add(d *MetricDef, value float64, labels ...string) {
	m, err := prometheus.NewConstMetric(d.desc, d.ValueType, value, labels...)
	if err != nil {
		b.errs = append(b.errs, fmt.Errorf("%s: %w", d.Name, err))
		return
	}
	b.metrics = append(b.metrics, m)
}

// addNum adds n when it holds a number.
func (b *batch) addNum(d *MetricDef, n flexNum, labels ...string) {
	if n.Valid {
		b.add(d, n.Value, labels...)
	}
}

func (b *batch) addDynamic(f DynamicFamily, stat string, vt prometheus.ValueType, value float64, labels ...string) {
	m, err := prometheus.NewConstMetric(f.desc(stat, vt), vt, value, labels...)
	if err != nil {
		b.errs = append(b.errs, fmt.Errorf("%s: %w", f.name(stat, vt), err))
		return
	}
	b.metrics = append(b.metrics, m)
}

func (b *batch) addBool(d *MetricDef, v bool, labels ...string) {
	b.add(d, boolFloat(v), labels...)
}

func (b *batch) err() error { return errors.Join(b.errs...) }

func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
