//go:build linux

package collector

import "github.com/cdf-eagles/zpool-status-exporter/internal/runner"

// DefaultPlatform returns the platform the binary was built for.
func DefaultPlatform(run runner.Runner, paths Paths) Platform {
	return &LinuxPlatform{Run: run, Paths: paths}
}
