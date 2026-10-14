//go:build !freebsd && !linux

package collector

import "github.com/cdf-eagles/zpool-status-exporter/internal/runner"

// DefaultPlatform returns the FreeBSD implementation on unsupported systems
// so the binary builds for development; collection will fail at run time.
func DefaultPlatform(run runner.Runner, paths Paths) Platform {
	return &FreeBSDPlatform{Run: run, Paths: paths}
}
