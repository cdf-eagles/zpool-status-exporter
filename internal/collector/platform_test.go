package collector

import (
	"context"
	"errors"
	"testing"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

const cmdLsblk = "lsblk -J -p -o NAME,PKNAME,TYPE,MODEL,SERIAL,WWN"

func TestLinuxDevices(t *testing.T) {
	f := runner.NewFake(map[string]runner.Response{cmdLsblk: {Stdout: fixture(t, "linux/lsblk.json")}})
	links := map[string]string{
		"/dev/disk/by-id/ata-WDC_WD80EFZZ-68BTXN0_SERIALL001-part1": "/dev/sda1",
		"/dev/disk/by-id/wwn-0x5000000000000102":                    "/dev/sdb",
	}
	p := &LinuxPlatform{Run: f, Paths: DefaultPaths(), EvalSymlinks: func(s string) (string, error) {
		if r, ok := links[s]; ok {
			return r, nil
		}
		return "", errors.New("no such file")
	}}
	m, err := p.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Disks) != 3 || m.Disks["sda"].Serial != "SERIALL001" || m.Disks["sda"].WWN != "5000000000000101" {
		t.Fatalf("disks = %+v", m.Disks)
	}
	cases := map[string][2]string{
		"/dev/disk/by-id/ata-WDC_WD80EFZZ-68BTXN0_SERIALL001-part1": {"sda1", "sda"},
		"/dev/disk/by-id/wwn-0x5000000000000102":                    {"sdb", "sdb"},
		"/dev/nvme0n1":                                              {"nvme0n1", "nvme0n1"},
		"/dev/sr0":                                                  {"sr0", ""},
		"/dev/unknown9":                                             {"unknown9", ""},
	}
	for in, w := range cases {
		dev, disk := m.Resolve(in)
		if dev != w[0] || disk != w[1] {
			t.Fatalf("Resolve(%s) = %s %s, want %v", in, dev, disk, w)
		}
	}
	if d, ok := m.DiskBySerial("serialL003"); !ok || d.Name != "nvme0n1" {
		t.Fatalf("DiskBySerial = %+v %v", d, ok)
	}
	if p.Name() != "linux" || (&FreeBSDPlatform{}).Name() != "freebsd" {
		t.Fatal("names")
	}
}

func TestLinuxDevicesErrors(t *testing.T) {
	f := runner.NewFake(map[string]runner.Response{cmdLsblk: {ExitCode: 32}})
	p := &LinuxPlatform{Run: f, Paths: DefaultPaths()}
	if _, err := p.Devices(context.Background()); err == nil {
		t.Fatal("lsblk failure must fail")
	}
	f.Set(cmdLsblk, runner.Response{Stdout: "{"})
	if _, err := p.Devices(context.Background()); err == nil {
		t.Fatal("bad JSON must fail")
	}
	f.Set(cmdLsblk, runner.Response{Stdout: `{"blockdevices":[{"name":"/dev/sdz","type":"disk"}]}`})
	m, err := p.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if dev, disk := m.Resolve("/dev/sdz"); dev != "sdz" || disk != "sdz" {
		t.Fatalf("real EvalSymlinks fallback: %s %s", dev, disk)
	}
}

func TestFreeBSDDevices(t *testing.T) {
	f := freebsdFake(t)
	m, err := freebsdPlatform(f).Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][2]string{
		"/dev/gpt/chassis02": {"ada2p1", "ada2"},
		"/dev/gpt/a0d06":     {"da26p1", "da26"},
		"/dev/ada0p3":        {"ada0p3", "ada0"},
		"/dev/da5":           {"da5", "da5"},
		"/dev/gpt/missing":   {"gpt/missing", ""},
		"/dev/ada0s1a":       {"ada0s1a", "ada0"},
		"/dev/ada0p3.eli":    {"ada0p3.eli", "ada0"},
		"/dev/gpt/a0d06.eli": {"da26p1.eli", "da26"},
		"/dev/gpt/a0d07.nop": {"da27p1.nop", "da27"},
	}
	for in, w := range cases {
		dev, disk := m.Resolve(in)
		if dev != w[0] || disk != w[1] {
			t.Fatalf("Resolve(%s) = %s %s, want %v", in, dev, disk, w)
		}
	}
	f.Set(cmdGlabel, runner.Response{ExitCode: 1})
	m, err = freebsdPlatform(f).Devices(context.Background())
	if err == nil || len(m.Disks) == 0 {
		t.Fatalf("glabel failure must keep disks and report the error: %v", err)
	}
	if dev, disk := m.Resolve("/dev/gpt/chassis02"); dev != "gpt/chassis02" || disk != "" {
		t.Fatalf("without labels: %s %s", dev, disk)
	}
	if _, ok := m.DiskBySerial(""); ok {
		t.Fatal("empty serial must not match")
	}
	var empty DeviceMap
	if dev, disk := empty.Resolve("/dev/gpt/x"); dev != "x" || disk != "" {
		t.Fatalf("zero DeviceMap: %s %s", dev, disk)
	}
}

func TestDiskBySerialIsDeterministic(t *testing.T) {
	m := DeviceMap{Disks: map[string]Disk{"da1": {Name: "da1", Serial: "S"}, "da0": {Name: "da0", Serial: "S"}, "da2": {Name: "da2"}}}
	for range 50 {
		if d, ok := m.DiskBySerial("s"); !ok || d.Name != "da0" {
			t.Fatalf("DiskBySerial = %+v", d)
		}
	}
}

func TestParseGeomNull(t *testing.T) {
	d := parseGeomDiskList([]byte("descr: orphan\nGeom name: cd0\n   ident: (null)\n   lunid: ABC\n"))
	if d["cd0"].Serial != "" || d["cd0"].WWN != "abc" || len(d) != 1 {
		t.Fatalf("geom = %+v", d)
	}
}

func TestDefaultPlatform(t *testing.T) {
	p := DefaultPlatform(runner.NewFake(nil), DefaultPaths())
	if p.Name() != "linux" && p.Name() != "freebsd" {
		t.Fatalf("platform %s", p.Name())
	}
}
