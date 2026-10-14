// Command zpool-status-exporter exports ZFS pool, virtual device, kernel
// statistic, dataset, and Self-Monitoring, Analysis and Reporting Technology
// (SMART) data as Prometheus metrics.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promslog"
	"github.com/prometheus/common/promslog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	"github.com/prometheus/exporter-toolkit/web/kingpinflag"

	"github.com/cdf-eagles/zpool-status-exporter/internal/collector"
	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

const (
	appName       = "zpool-status-exporter"
	defaultListen = ":10058"
)

type options struct {
	web           *web.FlagConfig
	telemetryPath string
	log           promslog.Config
	collect       collector.Config
}

func newApp(stdout, stderr io.Writer) (*kingpin.Application, *options) {
	app := kingpin.New(appName, "Prometheus exporter for ZFS pools, vdevs, I/O, ARC, datasets, and SMART data.")
	app.UsageWriter(stdout)
	app.ErrorWriter(stderr)
	app.Version(version.Print(appName))
	app.HelpFlag.Short('h')

	o := &options{collect: collector.Config{Paths: collector.DefaultPaths()}}
	o.web = kingpinflag.AddFlags(app, defaultListen)
	app.Flag("web.telemetry-path", "Path under which to expose metrics.").
		Default("/metrics").StringVar(&o.telemetryPath)

	app.Flag("collector.pool", "Collect pool and vdev state, capacity, errors, and scans from zpool status and zpool list.").
		Default("true").BoolVar(&o.collect.Pool)
	app.Flag("collector.iostat", "Collect pool and vdev I/O rates and latencies from zpool iostat.").
		Default("true").BoolVar(&o.collect.IOStat)
	app.Flag("collector.arc", "Collect Adaptive Replacement Cache (ARC) statistics.").
		Default("true").BoolVar(&o.collect.ARC)
	app.Flag("collector.kstat", "Collect other ZFS kernel statistics and per-dataset I/O counters.").
		Default("true").BoolVar(&o.collect.Kstat)
	app.Flag("collector.datasets", "Collect dataset space properties from zfs get.").
		Default("true").BoolVar(&o.collect.Datasets)
	app.Flag("collector.smart", "Collect SMART data from smartctl.").
		Default("true").BoolVar(&o.collect.SMART)

	app.Flag("collect.interval", "Interval for the pool, iostat, arc, and kstat collectors (whole seconds).").
		Default("10s").DurationVar(&o.collect.Interval)
	app.Flag("collect.dataset-interval", "Interval for the datasets collector.").
		Default("1m").DurationVar(&o.collect.DatasetInterval)
	app.Flag("collect.smart-interval", "Interval for the smart collector.").
		Default("10m").DurationVar(&o.collect.SmartInterval)
	app.Flag("collect.timeout", "Timeout for each external command.").
		Default("30s").DurationVar(&o.collect.Timeout)

	p := &o.collect.Paths
	app.Flag("path.zpool", "zpool(8) program.").Default(p.Zpool).StringVar(&p.Zpool)
	app.Flag("path.zfs", "zfs(8) program.").Default(p.Zfs).StringVar(&p.Zfs)
	app.Flag("path.smartctl", "smartctl(8) program.").Default(p.Smartctl).StringVar(&p.Smartctl)
	app.Flag("path.sysctl", "sysctl(8) program (FreeBSD).").Default(p.Sysctl).StringVar(&p.Sysctl)
	app.Flag("path.geom", "geom(8) program (FreeBSD).").Default(p.Geom).StringVar(&p.Geom)
	app.Flag("path.glabel", "glabel(8) program (FreeBSD).").Default(p.Glabel).StringVar(&p.Glabel)
	app.Flag("path.lsblk", "lsblk(8) program (Linux).").Default(p.Lsblk).StringVar(&p.Lsblk)
	app.Flag("path.kstat", "ZFS kstat directory (Linux).").Default(p.Kstat).StringVar(&p.Kstat)

	flag.AddFlags(app, &o.log)
	return app, o
}

func newRegistry(exporter prometheus.Collector) (*prometheus.Registry, error) {
	reg := prometheus.NewRegistry()
	for _, c := range []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		versioncollector.NewCollector("zpool_status_exporter"),
		exporter,
	} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register collector: %w", err)
		}
	}
	return reg, nil
}

func newMux(reg *prometheus.Registry, telemetryPath string, logger *slog.Logger) (*http.ServeMux, error) {
	mux := http.NewServeMux()
	mux.Handle(telemetryPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ErrorHandling:     promhttp.ContinueOnError,
		EnableOpenMetrics: true,
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	if telemetryPath != "/" {
		landing, err := web.NewLandingPage(web.LandingConfig{
			Name:        "ZFS zpool status exporter",
			Description: "Prometheus exporter for ZFS and SMART data",
			Version:     version.Info(),
			Links: []web.LandingLinks{
				{Address: telemetryPath, Text: "Metrics"},
				{Address: "/healthz", Text: "Health"},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("landing page: %w", err)
		}
		mux.Handle("/", landing)
	}
	return mux, nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	app, o := newApp(stdout, stderr)
	if _, err := app.Parse(args); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", appName, err)
		return 2
	}
	if !strings.HasPrefix(o.telemetryPath, "/") || o.telemetryPath == "/healthz" {
		_, _ = fmt.Fprintf(stderr, "%s: --web.telemetry-path must start with / and must not be /healthz\n", appName)
		return 2
	}
	o.log.Writer = stderr
	logger := promslog.New(&o.log)
	logger.Info("starting", "version", version.Info(), "build_context", version.BuildContext())

	run := runner.NewExec()
	o.collect.Runner = run
	o.collect.Logger = logger
	o.collect.Platform = collector.DefaultPlatform(run, o.collect.Paths)
	jobs, err := collector.Jobs(o.collect)
	if err != nil {
		logger.Error("invalid configuration", "err", err)
		return 2
	}

	exporter := collector.NewExporter(logger, jobs...)
	reg, err := newRegistry(exporter)
	if err != nil {
		logger.Error("registry", "err", err)
		return 1
	}
	mux, err := newMux(reg, o.telemetryPath, logger)
	if err != nil {
		logger.Error("http handler", "err", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	collectCtx, cancelCollect := context.WithCancel(ctx)
	defer cancelCollect()
	wg := exporter.Start(collectCtx)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- web.ListenAndServe(srv, o.web, logger) }()

	code := 0
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "err", err)
			code = 1
		}
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("http shutdown", "err", err)
			code = 1
		}
		cancel()
	}
	cancelCollect()
	wg.Wait()
	return code
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
