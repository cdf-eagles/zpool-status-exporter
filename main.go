package main

import (
	"fmt"
	"context"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"github.com/krystal/go-zfs"
)

// ZpoolInfo holds zpool status information
// https://pkg.go.dev/github.com/krystal/go-zfs#Pool
type ZpoolInfo struct {
	Name			string
	Allocated		uint64
	Capacity		uint64
	Fragmentation	uint64
	Free			uint64
	Freeing			uint64
	Health			string
	Leaked			uint64
	ReadOnly		bool
	Size			uint64
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
		p, err := z.GetPool(ctx,pool.Name)
		if err != nil {
			log.Printf("Error retrieving pool %s: %v", pool.Name, err)
			continue
		}

		// Get pool stats
		poolAllocated, present := p.Allocated()
		if present == true {
			info.Allocated = poolAllocated
		}
		poolCapacity, present := p.Capacity()
		if present == true {
			info.Capacity = poolCapacity
		}
		poolFragmentation, present := p.Fragmentation()
		if present == true {
			info.Fragmentation = poolFragmentation
		}
		poolFree, present := p.Free()
		if present == true {
			info.Free = poolFree
		}
		poolFreeing, present := p.Freeing()
		if present == true {
			info.Freeing = poolFreeing
		}
		poolHealth, present := p.Health()
		if present == true {
			info.Health = poolHealth
		}
		poolLeaked, present := p.Leaked()
		if present == true {
			info.Leaked = poolLeaked
		}
		poolReadOnly, present := p.ReadOnly()
		if present == true {
			info.ReadOnly = poolReadOnly
		}
		poolSize, present := p.Size()
		if present == true {
			info.Size = poolSize
		}

		metrics[pool.Name] = info
	}

	return metrics
}

// Handler for Prometheus scraping
func prometheusHandler(w http.ResponseWriter, r *http.Request) {
	metrics := collectZpoolMetrics()
	if metrics == nil {
		http.Error(w, "Error collecting metrics", http.StatusInternalServerError)
		return
	}

	// Generate Prometheus exposition format
	var output string
	for name, info := range metrics {
		// Pool health metric
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
		output += fmt.Sprintf("# HELP zpool_health Health returns the value of the 'health' property.\n")
		output += fmt.Sprintf("# TYPE zpool_health gauge\n")
		output += fmt.Sprintf("zpool_health{pool=\"%s\"} %g\n", name, healthValue)

		// Allocated metric
		output += fmt.Sprintf("# HELP zpool_allocated_bytes Allocated returns the value of the 'allocated' property as number of bytes.\n")
		output += fmt.Sprintf("# TYPE zpool_allocated_bytes gauge\n")
		output += fmt.Sprintf("zpool_allocated_bytes{pool=\"%s\"} %g\n", name, float64(info.Allocated))
		output += fmt.Sprintf("zpool_allocated_bytes{pool=\"%s\"} %g\n", name, float64(info.Allocated))

		// Capacity metric
		output += fmt.Sprintf("# HELP zpool_capacity_bytes Capacity returns the value of the 'capacity' property as percentage.\n")
		output += fmt.Sprintf("# TYPE zpool_capacity_bytes gauge\n")
		output += fmt.Sprintf("zpool_capacity_percent{pool=\"%s\"} %g\n", name, float64(info.Capacity))

		// Fragmentation metric
		output += fmt.Sprintf("# HELP zpool_fragmentation_bytes Fragmentation returns the value of the 'fragmentation' property as a percentage.\n")
		output += fmt.Sprintf("# TYPE zpool_fragmentation_bytes gauge\n")
		output += fmt.Sprintf("zpool_fragmentation_percent{pool=\"%s\"} %g\n", name, float64(info.Fragmentation))

		// Free space metric
		output += fmt.Sprintf("# HELP zpool_free_bytes Free returns the value of the 'free' property as number of bytes.\n")
		output += fmt.Sprintf("# TYPE zpool_free_bytes gauge\n")
		output += fmt.Sprintf("zpool_free_bytes{pool=\"%s\"} %g\n", name, float64(info.Free))

		// Freeing space metric
		output += fmt.Sprintf("# HELP zpool_freeing_bytes Freeing returns the value of the 'freeing' property as number of bytes.\n")
		output += fmt.Sprintf("# TYPE zpool_freeing_bytes gauge\n")
		output += fmt.Sprintf("zpool_freeing_bytes{pool=\"%s\"} %g\n", name, float64(info.Freeing))

		// Leaked space metric
		output += fmt.Sprintf("# HELP zpool_leaked Leaked returns the value of the 'leaked' property as number of bytes.\n")
		output += fmt.Sprintf("# TYPE zpool_leaked gauge\n")
		output += fmt.Sprintf("zpool_leaked{pool=\"%s\"} %g\n", name, float64(info.Leaked))

		// ReadOnly metric
		output += fmt.Sprintf("# HELP zpool_readonly ReadOnly returns the value of the 'readonly' property as a boolean.\n")
		output += fmt.Sprintf("# TYPE zpool_readonly gauge\n")
		output += fmt.Sprintf("zpool_readonly{pool=\"%s\"} %d\n", name, Bool2int(info.ReadOnly))

		// Size space metric
		output += fmt.Sprintf("# HELP zpool_size_bytes Size returns the value of the 'size' property as number of bytes.\n")
		output += fmt.Sprintf("# TYPE zpool_size_bytes gauge\n")
		output += fmt.Sprintf("zpool_size_bytes{pool=\"%s\"} %g\n", name, float64(info.Size))
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.Write([]byte(output))
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

	// Start HTTP server
	http.HandleFunc("/metrics", prometheusHandler)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		metrics := collectZpoolMetrics()

		html := "<html><head><title>ZPool Status</title></head><body><h1>ZPool Status</h1>"

		html += "<h2>Pools</h2><ul>\n"

		gigaByte := float64(1024*1024*1024)
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
			html += fmt.Sprintf("</li></p>\n")
			// html += fmt.Sprintf("\n")
		}

		html += "</ul></body></html>"
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	})

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
		log.Fatal("Server error:", err)
	}
}
