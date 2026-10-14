//go:build freebsd

package collector

import "github.com/cdf-eagles/zpool-status-exporter/internal/runner"

// DefaultPlatform returns the platform the binary was built for.
func DefaultPlatform(run runner.Runner, paths Paths) Platform {
	return &FreeBSDPlatform{Run: run, Paths: paths}
}
