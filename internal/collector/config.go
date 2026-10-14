package collector

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

// Config selects collectors and their schedules.
type Config struct {
	Runner   runner.Runner
	Platform Platform
	Paths    Paths
	Logger   *slog.Logger

	Interval        time.Duration
	DatasetInterval time.Duration
	SmartInterval   time.Duration
	Timeout         time.Duration

	Pool     bool
	IOStat   bool
	ARC      bool
	Kstat    bool
	Datasets bool
	SMART    bool

	now func() time.Time
}

// Jobs builds the scheduled jobs for the enabled collectors.
func Jobs(cfg Config) ([]ScheduledJob, error) {
	if cfg.Runner == nil {
		return nil, errors.New("collector: no runner")
	}
	if cfg.Platform == nil {
		return nil, errors.New("collector: no platform")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.Interval < time.Second {
		return nil, fmt.Errorf("collector: interval %s is shorter than 1s", cfg.Interval)
	}
	if cfg.Interval%time.Second != 0 {
		return nil, fmt.Errorf("collector: interval %s is not a whole number of seconds", cfg.Interval)
	}
	if cfg.DatasetInterval <= 0 || cfg.SmartInterval <= 0 || cfg.Timeout <= 0 {
		return nil, errors.New("collector: intervals and timeout must be positive")
	}

	pool := &poolJob{run: cfg.Runner, zpool: cfg.Paths.Zpool, platform: cfg.Platform, logger: cfg.Logger}
	cache := &topologyCache{maxAge: 3 * cfg.Interval, now: cfg.now, refresh: pool.topology}
	pool.cache = cache

	var jobs []ScheduledJob
	if cfg.Pool {
		jobs = append(jobs, ScheduledJob{Job: pool, Interval: cfg.Interval, Timeout: cfg.Timeout})
	}
	if cfg.IOStat {
		jobs = append(jobs, ScheduledJob{
			Job:      &iostatJob{run: cfg.Runner, zpool: cfg.Paths.Zpool, interval: cfg.Interval, cache: cache},
			Interval: cfg.Interval,
			Timeout:  cfg.Interval + cfg.Timeout,
		})
	}
	if cfg.ARC {
		jobs = append(jobs, ScheduledJob{Job: &arcJob{platform: cfg.Platform}, Interval: cfg.Interval, Timeout: cfg.Timeout})
	}
	if cfg.Kstat {
		jobs = append(jobs, ScheduledJob{Job: &kstatJob{platform: cfg.Platform}, Interval: cfg.Interval, Timeout: cfg.Timeout})
	}
	if cfg.Datasets {
		jobs = append(jobs, ScheduledJob{
			Job:      &datasetJob{run: cfg.Runner, zfs: cfg.Paths.Zfs},
			Interval: cfg.DatasetInterval,
			Timeout:  cfg.Timeout,
		})
	}
	if cfg.SMART {
		jobs = append(jobs, ScheduledJob{
			Job: &smartJob{
				run: cfg.Runner, smartctl: cfg.Paths.Smartctl, platform: cfg.Platform,
				cmdTimeout: cfg.Timeout, logger: cfg.Logger,
			},
			Interval: cfg.SmartInterval,
			Timeout:  cfg.SmartInterval,
		})
	}
	if len(jobs) == 0 {
		return nil, errors.New("collector: every collector is disabled")
	}
	return jobs, nil
}
