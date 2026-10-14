package collector

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

// FreeBSDPlatform reads devices from glabel(8) and geom(8) and kernel
// statistics from sysctl(8).
type FreeBSDPlatform struct {
	Run   runner.Runner
	Paths Paths
}

// Name implements Platform.
func (p *FreeBSDPlatform) Name() string { return "freebsd" }

// Devices implements Platform.
func (p *FreeBSDPlatform) Devices(ctx context.Context) (DeviceMap, error) {
	res, err := p.Run.Run(ctx, p.Paths.Geom, "disk", "list")
	if err != nil {
		return DeviceMap{}, err
	}
	disks := parseGeomDiskList(res.Stdout)
	labels := map[string]string{}
	res, err = p.Run.Run(ctx, p.Paths.Glabel, "status")
	if err == nil {
		labels = parseGlabelStatus(res.Stdout)
	}
	m := DeviceMap{Disks: disks, resolve: func(vdevPath string) (string, string) {
		dev := strings.TrimPrefix(vdevPath, "/dev/")
		suffix := ""
		for _, s := range []string{".eli", ".nop"} {
			if strings.HasSuffix(dev, s) {
				suffix = s
				dev = strings.TrimSuffix(dev, s)
				break
			}
		}
		if provider, ok := labels[dev]; ok {
			dev = provider
		}
		return dev + suffix, diskOf(dev, disks)
	}}
	if err != nil {
		return m, fmt.Errorf("glabel status: %w", err)
	}
	return m, nil
}

func parseGlabelStatus(data []byte) map[string]string {
	labels := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || (f[0] == "Name" && f[1] == "Status") {
			continue
		}
		labels[f[0]] = f[len(f)-1]
	}
	return labels
}

func parseGeomDiskList(data []byte) map[string]Disk {
	disks := map[string]Disk{}
	var cur *Disk
	flush := func() {
		if cur != nil && cur.Name != "" {
			disks[cur.Name] = *cur
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "(null)" {
			value = ""
		}
		switch key {
		case "Geom name":
			flush()
			cur = &Disk{Name: value}
		case "descr":
			if cur != nil {
				cur.Model = value
			}
		case "ident":
			if cur != nil {
				cur.Serial = value
			}
		case "lunid":
			if cur != nil {
				cur.WWN = strings.ToLower(value)
			}
		}
	}
	flush()
	return disks
}

// ARC implements Platform.
func (p *FreeBSDPlatform) ARC(ctx context.Context) (KstatSnapshot, error) {
	res, err := p.Run.Run(ctx, p.Paths.Sysctl, "-q", "kstat.zfs.misc.arcstats")
	if err != nil {
		return KstatSnapshot{}, err
	}
	snap := parseSysctlKstats(res.Stdout)
	if len(snap.Misc["arcstats"]) == 0 {
		return snap, errors.New("sysctl returned no ARC statistics")
	}
	return snap, nil
}

// Kstats implements Platform.
func (p *FreeBSDPlatform) Kstats(ctx context.Context) (KstatSnapshot, error) {
	res, err := p.Run.Run(ctx, p.Paths.Sysctl, "-q", "kstat.zfs")
	if err != nil {
		return KstatSnapshot{}, err
	}
	snap := parseSysctlKstats(res.Stdout)
	delete(snap.Misc, "arcstats")
	return snap, nil
}

var (
	sysctlMisc    = regexp.MustCompile(`^kstat\.zfs\.misc\.([A-Za-z0-9_]+)\.([A-Za-z0-9_-]+): (.*)$`)
	sysctlIOStats = regexp.MustCompile(`^kstat\.zfs\.(.+)\.misc\.iostats\.([A-Za-z0-9_-]+): (.*)$`)
	sysctlObjset  = regexp.MustCompile(`^kstat\.zfs\.(.+)\.dataset\.(objset-0x[0-9a-fA-F]+)\.([A-Za-z0-9_-]+): (.*)$`)
)

func parseSysctlKstats(data []byte) KstatSnapshot {
	snap := KstatSnapshot{Misc: map[string]map[string]float64{}}
	type objKey struct{ pool, objset string }
	pools := map[string]*PoolKstats{}
	var poolOrder []string
	objsets := map[objKey]*DatasetKstats{}
	var objOrder []objKey
	pool := func(name string) *PoolKstats {
		if p, ok := pools[name]; ok {
			return p
		}
		p := &PoolKstats{Pool: name, IOStats: map[string]float64{}}
		pools[name] = p
		poolOrder = append(poolOrder, name)
		return p
	}

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := sysctlMisc.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseFloat(m[3], 64); err == nil {
				if snap.Misc[m[1]] == nil {
					snap.Misc[m[1]] = map[string]float64{}
				}
				snap.Misc[m[1]][m[2]] = v
			}
			continue
		}
		if m := sysctlIOStats.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseFloat(m[3], 64); err == nil {
				pool(m[1]).IOStats[m[2]] = v
			}
			continue
		}
		if m := sysctlObjset.FindStringSubmatch(line); m != nil {
			k := objKey{m[1], m[2]}
			ds, ok := objsets[k]
			if !ok {
				ds = &DatasetKstats{Stats: map[string]float64{}}
				objsets[k] = ds
				objOrder = append(objOrder, k)
				pool(m[1])
			}
			if m[3] == "dataset_name" {
				ds.Name = m[4]
			} else if v, err := strconv.ParseFloat(m[4], 64); err == nil {
				ds.Stats[m[3]] = v
			}
		}
	}
	for _, k := range objOrder {
		if ds := objsets[k]; ds.Name != "" {
			pools[k.pool].Datasets = append(pools[k.pool].Datasets, *ds)
		}
	}
	for _, name := range poolOrder {
		snap.Pools = append(snap.Pools, *pools[name])
	}
	return snap
}
