package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"github.com/krystal/go-zfs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type promMetrics struct {
	zpoolHealth        *prometheus.GaugeVec
	zpoolAllocated     *prometheus.GaugeVec
	zpoolCapacity      *prometheus.GaugeVec
	zpoolFragmentation *prometheus.GaugeVec
	zpoolFree          *prometheus.GaugeVec
	zpoolFreeing       *prometheus.GaugeVec
	zpoolLeaked        *prometheus.GaugeVec
	zpoolReadOnly      *prometheus.GaugeVec
	zpoolSize          *prometheus.GaugeVec
}

// ZpoolInfo holds zpool status information
// https://pkg.go.dev/github.com/krystal/go-zfs#Pool
type ZpoolInfo struct {
	Name          string
	Allocated     uint64
	Capacity      uint64
	Fragmentation uint64
	Free          uint64
	Freeing       uint64
	Health        string
	Leaked        uint64
	ReadOnly      bool
	Size          uint64
}

// convert booleans to integers for output
// https://dev.to/chigbeef_77/bool-int-but-stupid-in-go-3jb3
func Bool2int(b bool) int {
	// The compiler currently only optimizes this form.
	// See issue 6011.
	var i int
	if b {
		i = 1
	} else {
		i = 0
	}
	return i
}

// collectZpoolMetrics gathers zpool status metrics
func collectZpoolMetrics() map[string]*ZpoolInfo {
	metrics := make(map[string]*ZpoolInfo)
	ctx := context.Background()
	z := zfs.New()

	// Get all pools using the actual API
	pools, err := z.ListPools(ctx)
	if err != nil {
		log.Printf("Error getting pools: %v", err)
		return nil
	}

	for _, pool := range pools {
		info := &ZpoolInfo{
			Name: pool.Name,
		}
		p, err := z.GetPool(ctx, pool.Name)
		if err != nil {
			log.Printf("Error retrieving pool %s: %v", pool.Name, err)
			continue
		}

		// Get pool stats
		poolAllocated, present := p.Allocated()
		if present {
			info.Allocated = poolAllocated
		}
		poolCapacity, present := p.Capacity()
		if present {
			info.Capacity = poolCapacity
		}
		poolFragmentation, present := p.Fragmentation()
		if present {
			info.Fragmentation = poolFragmentation
		}
		poolFree, present := p.Free()
		if present {
			info.Free = poolFree
		}
		poolFreeing, present := p.Freeing()
		if present {
			info.Freeing = poolFreeing
		}
		poolHealth, present := p.Health()
		if present {
			info.Health = poolHealth
		}
		poolLeaked, present := p.Leaked()
		if present {
			info.Leaked = poolLeaked
		}
		poolReadOnly, present := p.ReadOnly()
		if present {
			info.ReadOnly = poolReadOnly
		}
		poolSize, present := p.Size()
		if present {
			info.Size = poolSize
		}

		metrics[pool.Name] = info
	}

	return metrics
}

// Create Prometheus Metrics Objects
func zfsPromMetrics(reg prometheus.Registerer) *promMetrics {
	m := &promMetrics{
		zpoolHealth: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "zpool_health",
			Help: "Health returns the value of the 'health' property.",
		},
		[]string {
			"pool", // ZFS Pool Name
		}),
		zpoolAllocated: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "zpool_allocated_bytes",
			Help: "Allocated returns the value of the 'allocated' property as number of bytes.",
		},
		[]string {
			"pool", // ZFS Pool Name
		}),
		zpoolCapacity: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "zpool_capacity_bytes",
			Help: "Capacity returns the value of the 'capacity' property as percentage.",
		},
		[]string {
			"pool", // ZFS Pool Name
		}),
		zpoolFragmentation: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "zpool_fragmentation_bytes",
			Help: "Fragmentation returns the value of the 'fragmentation' property as a percentage.",
		},
		[]string {
			"pool", // ZFS Pool Name
		}),
		zpoolFree: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "zpool_free_bytes",
			Help: "Free returns the value of the 'free' property as number of bytes.",
		},
		[]string {
			"pool", // ZFS Pool Name
		}),
		zpoolFreeing: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "zpool_freeing_bytes",
			Help: "Freeing returns the value of the 'freeing' property as number of bytes.",
		},
		[]string {
			"pool", // ZFS Pool Name
		}),
		zpoolLeaked: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "zpool_leaked",
			Help: "Leaked returns the value of the 'leaked' property as number of bytes.",
		},
		[]string {
			"pool", // ZFS Pool Name
		}),
		zpoolReadOnly: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "zpool_readonly",
			Help: "ReadOnly returns the value of the 'readonly' property as a boolean.",
		},
		[]string {
			"pool", // ZFS Pool Name
		}),
		zpoolSize: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "zpool_size_bytes",
			Help: "Size returns the value of the 'size' property as number of bytes.",
		},
		[]string {
			"pool", // ZFS Pool Name
		}),
	}
	return m
}

// Record Prometheus Metrics
func recordPromMetrics(m *promMetrics) {
	metrics := collectZpoolMetrics()

	for _, info := range metrics {
		// Pool health metric (convert string to float64)
		healthValue := 0.0
		switch info.Health {
		case "ONLINE":
			healthValue = 0.0
		case "DEGRADED":
			healthValue = 1.0
		case "FAULTED":
			healthValue = 2.0
		case "OFFLINE":
			healthValue = 3.0
		case "UNAVAIL":
			healthValue = 4.0
		case "REMOVED":
			healthValue = 5.0
		default:
			healthValue = -1.0
		}

		// Pool readOnly metric (convert boolean to float64)
		readOnlyValue := 0.0
		if info.ReadOnly {
			readOnlyValue = 1.0
		} else {
			readOnlyValue = 0.0
		}

		// Assign collected metrics to Prometheus objects
		m.zpoolHealth.WithLabelValues(info.Name).Set(healthValue)
		m.zpoolAllocated.WithLabelValues(info.Name).Set(float64(info.Allocated))
		m.zpoolCapacity.WithLabelValues(info.Name).Set(float64(info.Capacity))
		m.zpoolFree.WithLabelValues(info.Name).Set(float64(info.Free))
		m.zpoolFreeing.WithLabelValues(info.Name).Set(float64(info.Freeing))
		m.zpoolLeaked.WithLabelValues(info.Name).Set(float64(info.Leaked))
		m.zpoolReadOnly.WithLabelValues(info.Name).Set(float64(readOnlyValue))
		m.zpoolSize.WithLabelValues(info.Name).Set(float64(info.Size))
	}
}

// Main function
func main() {
	// Check if zfs is available
	if _, err := os.Stat("/dev/zfs"); err != nil {
		log.Println("ZFS not available on this system")
		os.Exit(1)
	}

	fmt.Println("Starting ZPool Status Monitor...")
	fmt.Printf("Go version: %s\n", runtime.Version())

	// use Prometheus library to collect metrics
	promReg := prometheus.NewRegistry()
	// default Prometheus metrics for the exporter
	promReg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	// ZPool metrics for the exporter
	promMetrics := zfsPromMetrics(promReg)

	// Start HTTP server
	http.Handle("/metrics", promhttp.HandlerFor(promReg, promhttp.HandlerOpts{}))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		metrics := collectZpoolMetrics()
		recordPromMetrics(promMetrics)

		html := "<html><head><title>ZPool Status</title></head><body><h1>ZPool Status</h1>"

		html += "<h2>Pools</h2><ul>\n"

		gigaByte := float64(1024 * 1024 * 1024)
		for _, info := range metrics {
			html += fmt.Sprintf("<p><li>Pool: %s", info.Name)
			html += fmt.Sprintf("<br>  Health: %s", info.Health)
			html += fmt.Sprintf("<br>  Allocated: %d bytes (%.2f GB)", info.Allocated, float64(info.Allocated)/float64(gigaByte))
			html += fmt.Sprintf("<br>  Capacity: %d%% ", info.Capacity)
			html += fmt.Sprintf("<br>  Fragmentation: %d%%", info.Fragmentation)
			html += fmt.Sprintf("<br>  Free: %d bytes (%.2f GB)", info.Free, float64(info.Free)/float64(gigaByte))
			html += fmt.Sprintf("<br>  Freeing: %d bytes (%.2f GB)", info.Freeing, float64(info.Freeing)/float64(gigaByte))
			html += fmt.Sprintf("<br>  Leaked: %d bytes (%.2f GB)", info.Leaked, float64(info.Leaked)/float64(gigaByte))
			html += fmt.Sprintf("<br>  ReadOnly: %v", info.ReadOnly)
			html += fmt.Sprintf("<br>  Size: %d bytes (%.2f GB)", info.Size, float64(info.Size)/float64(gigaByte))
			html += "</li></p>\n"
		}

		html += "</ul></body></html>"
		w.Header().Set("Content-Type", "text/html")
		n, err := w.Write([]byte(html))
		if err != nil {
			log.Fatalf("Failed to write HTML data: response=%v err=%v", n, err)
		}
	})

	// default port 62000 can be changed on the command line
	port := "62000"
	if len(os.Args) > 1 {
		if portNum, err := strconv.Atoi(os.Args[1]); err == nil {
			port = strconv.Itoa(portNum)
		}
	}

	addr := ":" + port
	fmt.Printf("Listening on %s\n", addr)
	fmt.Printf("Prometheus metrics at: %s/metrics\n", addr)
	fmt.Printf("HTML status at: %s/\n", addr)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server error: %v", err)
		os.Exit(1)
	}
}
