package collector

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	collectorUp = gauge("exporter_collector_up",
		"Whether the last run of a collector succeeded (1) or failed (0).", "collector")
	collectorDuration = gauge("exporter_collector_duration_seconds",
		"Duration of the last run of a collector.", "collector")
	collectorLastSuccess = gauge("exporter_collector_last_success_timestamp_seconds",
		"Unix time of the last successful run of a collector.", "collector")
)

// Job collects one group of metrics.
type Job interface {
	Name() string
	Collect(ctx context.Context) ([]prometheus.Metric, error)
}

// ScheduledJob is a Job with its run interval and per-run timeout.
type ScheduledJob struct {
	Job      Job
	Interval time.Duration
	Timeout  time.Duration
}

type schedule struct {
	ScheduledJob

	mu          sync.Mutex
	metrics     []prometheus.Metric
	ran         bool
	ok          bool
	duration    time.Duration
	lastSuccess time.Time
}

// Exporter runs jobs in the background and serves their latest results as a
// prometheus.Collector. Scrapes never run commands.
type Exporter struct {
	schedules []*schedule
	logger    *slog.Logger
	now       func() time.Time
}

// NewExporter returns an Exporter for the given jobs.
func NewExporter(logger *slog.Logger, jobs ...ScheduledJob) *Exporter {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	e := &Exporter{logger: logger, now: time.Now}
	for _, j := range jobs {
		e.schedules = append(e.schedules, &schedule{ScheduledJob: j})
	}
	return e
}

// Start runs every job in its own goroutine until ctx is cancelled. A job
// whose run takes longer than its interval starts again immediately.
func (e *Exporter) Start(ctx context.Context) *sync.WaitGroup {
	var wg sync.WaitGroup
	for _, s := range e.schedules {
		wg.Go(func() { e.loop(ctx, s) })
	}
	return &wg
}

// RunOnce runs every job once, in order.
func (e *Exporter) RunOnce(ctx context.Context) {
	for _, s := range e.schedules {
		e.run(ctx, s)
	}
}

func (e *Exporter) loop(ctx context.Context, s *schedule) {
	for {
		start := e.now()
		e.run(ctx, s)
		wait := s.Interval - e.now().Sub(start)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (e *Exporter) run(ctx context.Context, s *schedule) {
	if ctx.Err() != nil {
		return
	}
	runCtx := ctx
	if s.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, s.Timeout)
		defer cancel()
	}
	start := e.now()
	metrics, err := s.Job.Collect(runCtx)
	elapsed := e.now().Sub(start)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ran = true
	s.duration = elapsed
	if err != nil {
		s.ok = false
		s.metrics = nil
		e.logger.Warn("collector failed", "collector", s.Job.Name(), "duration", elapsed, "err", err)
		return
	}
	s.ok = true
	s.metrics = metrics
	s.lastSuccess = e.now()
	e.logger.Debug("collector finished", "collector", s.Job.Name(), "duration", elapsed, "metrics", len(metrics))
}

// Describe implements prometheus.Collector. The exporter is unchecked
// because kernel statistic names are only known at run time.
func (e *Exporter) Describe(chan<- *prometheus.Desc) {}

// Collect implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	for _, s := range e.schedules {
		s.mu.Lock()
		for _, m := range s.metrics {
			ch <- m
		}
		if s.ran {
			name := s.Job.Name()
			ch <- prometheus.MustNewConstMetric(collectorUp.desc, prometheus.GaugeValue, boolFloat(s.ok), name)
			ch <- prometheus.MustNewConstMetric(collectorDuration.desc, prometheus.GaugeValue, s.duration.Seconds(), name)
			if !s.lastSuccess.IsZero() {
				ch <- prometheus.MustNewConstMetric(collectorLastSuccess.desc, prometheus.GaugeValue,
					float64(s.lastSuccess.UnixNano())/1e9, name)
			}
		}
		s.mu.Unlock()
	}
}
