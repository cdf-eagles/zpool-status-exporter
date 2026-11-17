package collector

import (
	"context"
	"errors"
	"fmt"
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
	for line := range lines(data) {
		f := strings.Fields(line)
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
	for line := range lines(data) {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
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

const (
	groupChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_"
	statChars  = groupChars + "-"
	hexChars   = "0123456789ABCDEFabcdef"
)

// only reports whether s is not empty and holds only bytes from chars.
func only(s, chars string) bool { return s != "" && strings.Trim(s, chars) == "" }

// cutStat splits "<statistic>: <value>".
func cutStat(s string) (stat, value string, ok bool) {
	stat, value, ok = strings.Cut(s, ": ")
	return stat, value, ok && only(stat, statChars)
}

// sysctlMisc splits "kstat.zfs.misc.<group>.<statistic>: <value>".
func sysctlMisc(line string) (group, stat, value string, ok bool) {
	rest, ok := strings.CutPrefix(line, "kstat.zfs.misc.")
	if ok {
		group, rest, ok = strings.Cut(rest, ".")
	}
	if !ok || !only(group, groupChars) {
		return "", "", "", false
	}
	stat, value, ok = cutStat(rest)
	return group, stat, value, ok
}

// cutPool splits "kstat.zfs.<pool><sep><tail>" at the last sep for which
// valid(tail) holds.
func cutPool(line, sep string, valid func(tail string) bool) (pool string, ok bool) {
	rest, ok := strings.CutPrefix(line, "kstat.zfs.")
	if !ok {
		return "", false
	}
	for end := len(rest); ; {
		i := strings.LastIndex(rest[:end], sep)
		if i <= 0 {
			return "", false
		}
		if valid(rest[i+len(sep):]) {
			return rest[:i], true
		}
		// The next search keeps all but the last byte of this separator,
		// because separators can overlap.
		end = i + len(sep) - 1
	}
}

// sysctlIOStats splits "kstat.zfs.<pool>.misc.iostats.<statistic>: <value>".
func sysctlIOStats(line string) (pool, stat, value string, ok bool) {
	pool, ok = cutPool(line, ".misc.iostats.", func(tail string) bool {
		stat, value, ok = cutStat(tail)
		return ok
	})
	if !ok {
		return "", "", "", false
	}
	return pool, stat, value, true
}

// sysctlObjset splits
// "kstat.zfs.<pool>.dataset.objset-0x<hex>.<statistic>: <value>".
func sysctlObjset(line string) (pool, objset, stat, value string, ok bool) {
	pool, ok = cutPool(line, ".dataset.", func(tail string) bool {
		var rest string
		objset, rest, ok = strings.Cut(tail, ".")
		hex, isObjset := strings.CutPrefix(objset, "objset-0x")
		if !ok || !isObjset || !only(hex, hexChars) {
			return false
		}
		stat, value, ok = cutStat(rest)
		return ok
	})
	if !ok {
		return "", "", "", "", false
	}
	return pool, objset, stat, value, true
}

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
		name = strings.Clone(name)
		p := &PoolKstats{Pool: name, IOStats: map[string]float64{}}
		pools[name] = p
		poolOrder = append(poolOrder, name)
		return p
	}

	for line := range lines(data) {
		if group, stat, value, ok := sysctlMisc(line); ok {
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				if snap.Misc[group] == nil {
					snap.Misc[group] = map[string]float64{}
				}
				snap.Misc[group][stat] = v
			}
			continue
		}
		if name, stat, value, ok := sysctlIOStats(line); ok {
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				pool(name).IOStats[stat] = v
			}
			continue
		}
		if name, objset, stat, value, ok := sysctlObjset(line); ok {
			k := objKey{name, objset}
			ds, ok := objsets[k]
			if !ok {
				ds = &DatasetKstats{Stats: map[string]float64{}}
				p := pool(name)
				k.pool = p.Pool
				objsets[k] = ds
				objOrder = append(objOrder, k)
			}
			if stat == "dataset_name" {
				ds.Name = strings.Clone(value)
			} else if v, err := strconv.ParseFloat(value, 64); err == nil {
				ds.Stats[stat] = v
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
