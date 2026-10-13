package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

func newSmartJob(run runner.Runner) *smartJob {
	return &smartJob{run: run, smartctl: "smartctl", platform: freebsdPlatform(run), cmdTimeout: time.Second, logger: discard()}
}

func TestSmartCollectorFreeBSD(t *testing.T) {
	run := freebsdFake(t)
	got := collect(t, newSmartJob(run))

	want(t, got, `zfs_smart_device_info{device="da29",disk="da29",firmware="A4ADWDA0",model="HGST HUH728080ALE604",protocol="ATA",serial="SERIAL0018",wwn="5000000000000012"}`, 1)
	want(t, got, `zfs_smart_device_info{device="nvme0",disk="nda0",firmware="5B2QJXD7",model="Samsung SSD 990 PRO 2TB",protocol="NVMe",serial="SERIAL0001",wwn=""}`, 1)
	want(t, got, `zfs_smart_device_up{device="da29"}`, 1)
	want(t, got, `zfs_smart_device_standby{device="da29"}`, 0)
	want(t, got, `zfs_smart_healthy{device="da29"}`, 1)
	want(t, got, `zfs_smart_exit_status_bit{bit="6",device="da29",meaning="error_log_has_errors"}`, 1)
	want(t, got, `zfs_smart_exit_status_bit{bit="3",device="da29",meaning="disk_failing"}`, 0)
	want(t, got, `zfs_smart_exit_status_bit{bit="6",device="ada0",meaning="error_log_has_errors"}`, 0)
	want(t, got, `zfs_smart_error_log_entries{device="da29"}`, 91)
	want(t, got, `zfs_smart_temperature_celsius{device="da29"}`, 35)
	want(t, got, `zfs_smart_power_on_seconds{device="da29"}`, 76920*3600)
	want(t, got, `zfs_smart_power_cycles{device="da29"}`, 49)
	want(t, got, `zfs_smart_attribute_value{attribute="Temperature_Celsius",device="da29",id="194"}`, 171)
	want(t, got, `zfs_smart_attribute_worst{attribute="Temperature_Celsius",device="da29",id="194"}`, 171)
	want(t, got, `zfs_smart_attribute_threshold{attribute="Temperature_Celsius",device="da29",id="194"}`, 0)
	want(t, got, `zfs_smart_attribute_raw_value{attribute="Temperature_Celsius",device="da29",id="194"}`, 227634380835)
	if n := count(got, `zfs_smart_attribute_value{`); n != 18+15 {
		t.Fatalf("ATA attributes = %d, want 33", n)
	}
	want(t, got, `zfs_smart_temperature_celsius{device="nvme0"}`, 55)
	want(t, got, `zfs_smart_nvme_percentage_used{device="nvme0"}`, 1)
	want(t, got, `zfs_smart_nvme_unsafe_shutdowns{device="nvme0"}`, 87)
	want(t, got, `zfs_smart_error_log_entries{device="nvme0"}`, 0)
	absent(t, got, `zfs_smart_nvme_nsid{`)
	absent(t, got, `zfs_smart_nvme_temperature_sensors{`)
	absent(t, got, `zfs_smart_nvme_power_on_hours{`)
	absent(t, got, `zfs_smart_nvme_temperature{`)
	absent(t, got, `zfs_smart_device_up{device="ses0"}`)

	for _, c := range run.Calls() {
		if strings.Contains(c, "ses0") && strings.HasPrefix(c, "smartctl -j") {
			t.Fatalf("enclosure was queried: %s", c)
		}
	}
}

func TestSmartStandbyAndFailures(t *testing.T) {
	f := freebsdFake(t)
	f.Set(cmdScan, runner.Response{Stdout: "/dev/da29 -d sat # x\n/dev/ada0 -d atacam # x\n/dev/ada1 -d atacam # x\n/dev/ada2 -d atacam # x\n/dev/ada3 # no type\n"})
	sleeping := strings.ReplaceAll(fixture(t, "freebsd/smartctl-standby-ses0.json"), "/dev/ses0", "/dev/da29")
	f.Set(cmdSmartDa, runner.Response{Stdout: sleeping, ExitCode: 2})
	f.Set(cmdSmartAd, runner.Response{Stdout: `{"smartctl":{"exit_status":2},"device":{"name":"/dev/ada0"}}`, ExitCode: 2})
	f.Set("smartctl -j -x -n standby -d atacam /dev/ada1", runner.Response{Stdout: "not json", ExitCode: 1})
	f.Set("smartctl -j -x -n standby -d atacam /dev/ada2", runner.Response{Err: errors.New("exec: smartctl: not found")})
	f.Set("smartctl -j -x -n standby /dev/ada3", runner.Response{Stdout: `{"smart_status":{"passed":false},"model_name":"M","serial_number":"SERIAL0005"}`, ExitCode: 8})

	got := collect(t, newSmartJob(f))
	want(t, got, `zfs_smart_device_standby{device="da29"}`, 1)
	want(t, got, `zfs_smart_device_up{device="da29"}`, 0)
	absent(t, got, `zfs_smart_healthy{device="da29"}`)
	want(t, got, `zfs_smart_device_standby{device="ada0"}`, 0)
	want(t, got, `zfs_smart_device_up{device="ada0"}`, 0)
	want(t, got, `zfs_smart_device_up{device="ada1"}`, 0)
	want(t, got, `zfs_smart_device_up{device="ada2"}`, 0)
	want(t, got, `zfs_smart_device_up{device="ada3"}`, 1)
	want(t, got, `zfs_smart_healthy{device="ada3"}`, 0)
	want(t, got, `zfs_smart_exit_status_bit{bit="3",device="ada3",meaning="disk_failing"}`, 1)
	want(t, got, `zfs_smart_device_info{device="ada3",disk="ada3",firmware="",model="M",protocol="",serial="SERIAL0005",wwn=""}`, 1)
}

func TestSmartCollectorErrors(t *testing.T) {
	f := freebsdFake(t)
	f.Set(cmdScan, runner.Response{ExitCode: 1})
	if _, err := newSmartJob(f).Collect(context.Background()); err == nil {
		t.Fatal("scan failure must fail the job")
	}
	f.Set(cmdScan, runner.Response{Stdout: "/dev/ses0 -d atacam # enclosure\n"})
	if _, err := newSmartJob(f).Collect(context.Background()); err == nil || !strings.Contains(err.Error(), "no disks") {
		t.Fatalf("enclosures only: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.Set(cmdScan, runner.Response{Stdout: smallScan})
	if _, err := newSmartJob(f).Collect(ctx); err == nil {
		t.Fatal("cancelled context must fail")
	}
}

func TestSmartWithoutInventory(t *testing.T) {
	f := freebsdFake(t)
	f.Set(cmdGeom, runner.Response{ExitCode: 1})
	got := collect(t, newSmartJob(f))
	want(t, got, `zfs_smart_device_info{device="da29",disk="",firmware="A4ADWDA0",model="HGST HUH728080ALE604",protocol="ATA",serial="SERIAL0018",wwn="5000000000000012"}`, 1)
	absent(t, got, `zfs_smart_device_up{device="ses0"}`)
}

func TestSmartControllerDevices(t *testing.T) {
	disks := map[string]Disk{"sda": {Name: "sda"}}
	got := parseScanOpen([]byte("/dev/sda -d megaraid,0 # x\n/dev/sda -d megaraid,1 # x\n/dev/sda -d megaraid,1 # dup\n/dev/sda -d sat # x\n"), disks)
	labels := make([]string, 0, len(got))
	for _, tg := range got {
		labels = append(labels, tg.label())
	}
	if strings.Join(labels, ",") != "sda:megaraid,0,sda:megaraid,1,sda" {
		t.Fatalf("labels = %v", labels)
	}
}

func TestSmartScanIsCached(t *testing.T) {
	f := freebsdFake(t)
	now := fixedTime
	j := newSmartJob(f)
	j.now = func() time.Time { return now }
	scans := func() int {
		n := 0
		for _, c := range f.Calls() {
			if c == cmdScan {
				n++
			}
		}
		return n
	}
	for range 3 {
		if _, err := j.Collect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if scans() != 1 {
		t.Fatalf("scans = %d, want 1", scans())
	}
	now = now.Add(defaultScanMaxAge)
	if _, err := j.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if scans() != 2 {
		t.Fatalf("scans after max age = %d, want 2", scans())
	}
}

func TestSmartPrefersDeviceNameOverSerial(t *testing.T) {
	f := freebsdFake(t)
	f.Set(cmdScan, runner.Response{Stdout: "/dev/ada0 -d atacam # x\n"})
	f.Set(cmdSmartAd, runner.Response{Stdout: strings.Replace(fixture(t, "freebsd/smartctl-x-ada0.json"), "SERIAL0002", "SERIAL0001", 1)})
	got := collect(t, newSmartJob(f))
	if count(got, `zfs_smart_device_info{device="ada0",disk="ada0"`) != 1 {
		t.Fatal("a device that is itself a known disk must keep its own name")
	}
}

func TestParseScanOpen(t *testing.T) {
	disks := map[string]Disk{"ada0": {}, "da29": {}, "nda0": {}}
	got := parseScanOpen([]byte(fixture(t, "freebsd/smartctl-scan-open.txt")), disks)
	names := make([]string, 0, len(got))
	for _, tg := range got {
		names = append(names, tg.path+" "+tg.typ)
	}
	if strings.Join(names, ",") != "/dev/ada0 atacam,/dev/da29 sat,/dev/nvme0 nvme" {
		t.Fatalf("targets = %v", names)
	}
}

func TestErrorLogSummary(t *testing.T) {
	var s smartJSON
	n := 3.0
	s.ATAErrorLog = &struct {
		Extended *struct {
			Count *float64 `json:"count"`
		} `json:"extended"`
		Summary *struct {
			Count *float64 `json:"count"`
		} `json:"summary"`
	}{Summary: &struct {
		Count *float64 `json:"count"`
	}{Count: &n}}
	if v, ok := s.errorLogEntries(); !ok || v != 3 {
		t.Fatalf("summary count = %v %v", v, ok)
	}
	if _, ok := (&smartJSON{}).errorLogEntries(); ok {
		t.Fatal("no log must report not ok")
	}
}
