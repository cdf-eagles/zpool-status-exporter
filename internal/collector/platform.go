package collector

import (
	"context"
	"path"
	"regexp"
	"strings"
)

// Disk is one physical disk known to the operating system.
type Disk struct {
	Name   string
	Model  string
	Serial string
	WWN    string
}

// DeviceMap maps vdev paths to the partition and disk that back them.
type DeviceMap struct {
	Disks   map[string]Disk
	resolve func(vdevPath string) (device, disk string)
}

// Resolve returns the device (partition or whole disk) and the disk behind a
// vdev path. Unknown paths return the path's base name and an empty disk.
func (m DeviceMap) Resolve(vdevPath string) (device, disk string) {
	if m.resolve == nil {
		return path.Base(vdevPath), ""
	}
	return m.resolve(vdevPath)
}

// DiskBySerial returns the disk with the given serial number.
func (m DeviceMap) DiskBySerial(serial string) (Disk, bool) {
	if serial == "" {
		return Disk{}, false
	}
	for _, name := range sortedKeys(m.Disks) {
		if d := m.Disks[name]; strings.EqualFold(d.Serial, serial) {
			return d, true
		}
	}
	return Disk{}, false
}

// KstatSnapshot holds ZFS kernel statistics.
type KstatSnapshot struct {
	// Misc maps a group such as "arcstats" to its statistics.
	Misc map[string]map[string]float64
	// Pools holds per-pool statistics.
	Pools []PoolKstats
}

// PoolKstats holds the statistics of one pool.
type PoolKstats struct {
	Pool     string
	IOStats  map[string]float64
	Datasets []DatasetKstats
}

// DatasetKstats holds the I/O counters of one dataset (objset).
type DatasetKstats struct {
	Name  string
	Stats map[string]float64
}

// Platform hides the operating-system specific sources of device and kernel
// statistic data.
type Platform interface {
	// Name is the platform name, e.g. "freebsd".
	Name() string
	// Devices returns the disk inventory and vdev path resolution.
	Devices(ctx context.Context) (DeviceMap, error)
	// ARC returns the ARC statistics in Misc["arcstats"].
	ARC(ctx context.Context) (KstatSnapshot, error)
	// Kstats returns the misc groups and the per-pool statistics.
	Kstats(ctx context.Context) (KstatSnapshot, error)
}

// Paths holds the external programs and files the collectors use.
type Paths struct {
	Zpool    string
	Zfs      string
	Smartctl string
	Sysctl   string
	Geom     string
	Glabel   string
	Lsblk    string
	Kstat    string
}

// DefaultPaths returns program names resolved through PATH and the Linux
// kstat directory.
func DefaultPaths() Paths {
	return Paths{
		Zpool:    "zpool",
		Zfs:      "zfs",
		Smartctl: "smartctl",
		Sysctl:   "sysctl",
		Geom:     "geom",
		Glabel:   "glabel",
		Lsblk:    "lsblk",
		Kstat:    "/proc/spl/kstat/zfs",
	}
}

// kstatMiscGroups are the misc kstat groups exported by the kstat collector.
var kstatMiscGroups = []string{
	"abdstats", "dbufstats", "dmu_tx", "dnodestats", "fm",
	"vdev_mirror_stats", "zfetchstats", "zil",
}

var partitionSuffix = regexp.MustCompile(`^(p[0-9]+|s[0-9]+[a-h]?)?$`)

// diskOf returns the disk in disks that device is, or is a partition of.
func diskOf(device string, disks map[string]Disk) string {
	best := ""
	for name := range disks {
		if strings.HasPrefix(device, name) && partitionSuffix.MatchString(device[len(name):]) && len(name) > len(best) {
			best = name
		}
	}
	return best
}
