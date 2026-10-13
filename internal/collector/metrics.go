// Package collector gathers ZFS, kernel statistics, and Self-Monitoring,
// Analysis and Reporting Technology (SMART) data and exposes it as
// Prometheus metrics.
package collector

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

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
}

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

func dynamic(prefix, help string, labels ...string) DynamicFamily {
	f := DynamicFamily{Prefix: namespace + "_" + prefix, Help: help, Labels: labels}
	dynamicFamilies = append(dynamicFamilies, f)
	return f
}

// Definitions returns every fixed metric family the exporter can emit.
func Definitions() []*MetricDef { return append([]*MetricDef(nil), registry...) }

// DynamicFamilies returns the metric families whose names come from kernel
// statistics.
func DynamicFamilies() []DynamicFamily { return append([]DynamicFamily(nil), dynamicFamilies...) }

var invalidNameChars = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// sanitizeName turns a kernel statistic name into a valid metric name part.
func sanitizeName(s string) string {
	s = invalidNameChars.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

// batch accumulates metrics for one collection and remembers label errors
// instead of panicking.
type batch struct {
	metrics []prometheus.Metric
	errs    []error
}

func (b *batch) add(d *MetricDef, value float64, labels ...string) {
	m, err := prometheus.NewConstMetric(d.desc, d.ValueType, value, labels...)
	if err != nil {
		b.errs = append(b.errs, fmt.Errorf("%s: %w", d.Name, err))
		return
	}
	b.metrics = append(b.metrics, m)
}

func (b *batch) addDynamic(f DynamicFamily, stat string, vt prometheus.ValueType, value float64, labels ...string) {
	name := f.Prefix + "_" + sanitizeName(stat)
	if vt == prometheus.CounterValue {
		name = strings.TrimSuffix(name, "_count") + "_total"
	}
	desc := prometheus.NewDesc(name, f.Help+" ("+stat+")", f.Labels, nil)
	m, err := prometheus.NewConstMetric(desc, vt, value, labels...)
	if err != nil {
		b.errs = append(b.errs, fmt.Errorf("%s: %w", name, err))
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
