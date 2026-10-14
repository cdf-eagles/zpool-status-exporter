package collector

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

// LinuxPlatform reads devices from lsblk(8) and kernel statistics from the
// Solaris Porting Layer (SPL) kstat files under /proc/spl/kstat/zfs.
type LinuxPlatform struct {
	Run   runner.Runner
	Paths Paths
	// KstatFS is the kstat directory; nil means os.DirFS(Paths.Kstat).
	KstatFS fs.FS
	// EvalSymlinks resolves vdev paths such as /dev/disk/by-id/...; nil
	// means filepath.EvalSymlinks.
	EvalSymlinks func(string) (string, error)
}

// Name implements Platform.
func (p *LinuxPlatform) Name() string { return "linux" }

type lsblkOutput struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}

type lsblkDevice struct {
	Name     string        `json:"name"`
	PKName   *string       `json:"pkname"`
	Type     string        `json:"type"`
	Model    *string       `json:"model"`
	Serial   *string       `json:"serial"`
	WWN      *string       `json:"wwn"`
	Children []lsblkDevice `json:"children"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// Devices implements Platform.
func (p *LinuxPlatform) Devices(ctx context.Context) (DeviceMap, error) {
	res, err := p.Run.Run(ctx, p.Paths.Lsblk, "-J", "-p", "-o", "NAME,PKNAME,TYPE,MODEL,SERIAL,WWN")
	if err != nil {
		return DeviceMap{}, err
	}
	var out lsblkOutput
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return DeviceMap{}, fmt.Errorf("parse lsblk JSON: %w", err)
	}
	disks := map[string]Disk{}
	parents := map[string]string{}
	var walk func(devs []lsblkDevice)
	walk = func(devs []lsblkDevice) {
		for _, d := range devs {
			base := path.Base(d.Name)
			if d.Type == "disk" {
				disks[base] = Disk{
					Name:   base,
					Model:  deref(d.Model),
					Serial: deref(d.Serial),
					WWN:    strings.TrimPrefix(strings.ToLower(deref(d.WWN)), "0x"),
				}
			}
			if pk := deref(d.PKName); pk != "" {
				parents[base] = path.Base(pk)
			}
			walk(d.Children)
		}
	}
	walk(out.BlockDevices)

	eval := p.EvalSymlinks
	if eval == nil {
		eval = filepath.EvalSymlinks
	}
	return DeviceMap{Disks: disks, resolve: func(vdevPath string) (string, string) {
		real := vdevPath
		if r, err := eval(vdevPath); err == nil {
			real = r
		}
		dev := path.Base(real)
		if _, ok := disks[dev]; ok {
			return dev, dev
		}
		if parent, ok := parents[dev]; ok {
			if _, isDisk := disks[parent]; isDisk {
				return dev, parent
			}
		}
		return dev, ""
	}}, nil
}

func (p *LinuxPlatform) kstatFS() fs.FS {
	if p.KstatFS != nil {
		return p.KstatFS
	}
	return osDirFS(p.Paths.Kstat)
}

// ARC implements Platform.
func (p *LinuxPlatform) ARC(context.Context) (KstatSnapshot, error) {
	data, err := fs.ReadFile(p.kstatFS(), "arcstats")
	if err != nil {
		return KstatSnapshot{}, fmt.Errorf("read arcstats: %w", err)
	}
	stats, _, err := parseProcKstat(data)
	if err != nil {
		return KstatSnapshot{}, fmt.Errorf("arcstats: %w", err)
	}
	return KstatSnapshot{Misc: map[string]map[string]float64{"arcstats": stats}}, nil
}

// Kstats implements Platform.
func (p *LinuxPlatform) Kstats(context.Context) (KstatSnapshot, error) {
	fsys := p.kstatFS()
	snap := KstatSnapshot{Misc: map[string]map[string]float64{}}
	for _, group := range kstatMiscGroups {
		data, err := fs.ReadFile(fsys, group)
		if err != nil {
			continue
		}
		stats, _, err := parseProcKstat(data)
		if err != nil {
			return snap, fmt.Errorf("%s: %w", group, err)
		}
		snap.Misc[group] = stats
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return snap, fmt.Errorf("read kstat directory: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pk := PoolKstats{Pool: e.Name(), IOStats: map[string]float64{}}
		if data, err := fs.ReadFile(fsys, e.Name()+"/iostats"); err == nil {
			if stats, _, err := parseProcKstat(data); err == nil {
				pk.IOStats = stats
			}
		}
		files, err := fs.ReadDir(fsys, e.Name())
		if err != nil {
			return snap, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasPrefix(f.Name(), "objset-") {
				continue
			}
			data, err := fs.ReadFile(fsys, e.Name()+"/"+f.Name())
			if err != nil {
				continue
			}
			stats, strs, err := parseProcKstat(data)
			if err != nil || strs["dataset_name"] == "" {
				continue
			}
			pk.Datasets = append(pk.Datasets, DatasetKstats{Name: strs["dataset_name"], Stats: stats})
		}
		if len(pk.IOStats) > 0 || len(pk.Datasets) > 0 {
			snap.Pools = append(snap.Pools, pk)
		}
	}
	return snap, nil
}

// kstatTypeString is the SPL kstat data type for character strings.
const kstatTypeString = 7

// parseProcKstat parses an SPL kstat file: a header line, a "name type data"
// line, then one statistic per line.
func parseProcKstat(data []byte) (map[string]float64, map[string]string, error) {
	nums := map[string]float64{}
	strs := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	line := 0
	seenHeader := false
	for sc.Scan() {
		line++
		text := sc.Text()
		if line == 1 {
			continue
		}
		f := strings.Fields(text)
		if !seenHeader {
			if len(f) == 3 && f[0] == "name" && f[1] == "type" && f[2] == "data" {
				seenHeader = true
			}
			continue
		}
		if len(f) < 2 {
			continue
		}
		typ, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		if typ == kstatTypeString {
			strs[f[0]] = strings.Join(f[2:], " ")
			continue
		}
		if len(f) != 3 {
			continue
		}
		if v, err := strconv.ParseFloat(f[2], 64); err == nil {
			nums[f[0]] = v
		}
	}
	if !seenHeader {
		return nil, nil, fmt.Errorf("no %q header line", "name type data")
	}
	return nums, strs, nil
}
