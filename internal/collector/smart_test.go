package collector

import (
	"context"
	"encoding/json"
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

// smartctlVariant returns a captured smartctl document with one change
// applied, so the cases a healthy host never prints use real field layouts.
func smartctlVariant(t *testing.T, name string, edit func(doc map[string]any)) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(fixture(t, name)), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func sub(t *testing.T, v any, keys ...string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not a JSON object: %v", v)
	}
	for _, k := range keys {
		if m, ok = m[k].(map[string]any); !ok {
			t.Fatalf("no JSON object %q", k)
		}
	}
	return m
}

func rowsOf(t *testing.T, v any) []any {
	t.Helper()
	rows, ok := v.([]any)
	if !ok {
		t.Fatalf("not a JSON array: %v", v)
	}
	return rows
}

func ataRows(t *testing.T, doc map[string]any) []any {
	t.Helper()
	return rowsOf(t, sub(t, doc, "ata_smart_self_test_log", "extended")["table"])
}

func nvmeRows(t *testing.T, doc map[string]any) []any {
	t.Helper()
	return rowsOf(t, sub(t, doc, "nvme_self_test_log")["table"])
}

func setStatus(t *testing.T, row any, value float64) { sub(t, row, "status")["value"] = value }

func smartWith(t *testing.T, cmd, out string) map[string]float64 {
	t.Helper()
	f := freebsdFake(t)
	f.Set(cmd, runner.Response{Stdout: out})
	return collect(t, newSmartJob(f))
}

const hour = 3600

func TestSmartSelfTestsFreeBSD(t *testing.T) {
	got := collect(t, newSmartJob(freebsdFake(t)))

	want(t, got, `zfs_smart_self_test_passed{device="ada0",type="short"}`, 1)
	want(t, got, `zfs_smart_self_test_age_seconds{device="ada0",type="short"}`, 14*hour)
	want(t, got, `zfs_smart_self_test_passed{device="ada0",type="extended"}`, 1)
	want(t, got, `zfs_smart_self_test_age_seconds{device="ada0",type="extended"}`, 299*hour)
	want(t, got, `zfs_smart_self_test_in_progress{device="ada0"}`, 0)

	// da29 powered on 76920 hours, but its log counts lifetime hours in 16
	// bits and shows 11370 for the newest test.
	want(t, got, `zfs_smart_self_test_age_seconds{device="da29",type="short"}`, 14*hour)
	want(t, got, `zfs_smart_self_test_age_seconds{device="da29",type="extended"}`, 260*hour)
	want(t, got, `zfs_smart_self_test_in_progress{device="da29"}`, 0)

	want(t, got, `zfs_smart_self_test_passed{device="nvme0",type="short"}`, 1)
	want(t, got, `zfs_smart_self_test_age_seconds{device="nvme0",type="short"}`, 14*hour)
	want(t, got, `zfs_smart_self_test_passed{device="nvme0",type="extended"}`, 1)
	want(t, got, `zfs_smart_self_test_age_seconds{device="nvme0",type="extended"}`, 301*hour)
	want(t, got, `zfs_smart_self_test_in_progress{device="nvme0"}`, 0)

	if n := count(got, "zfs_smart_self_test_passed{"); n != 6 {
		t.Fatalf("self-test series = %d, want 6", n)
	}
	absent(t, got, `zfs_smart_self_test_passed{device="ada0",type="other"`)
}

func TestSmartSelfTestAtaOutcomes(t *testing.T) {
	variant := func(edit func(doc map[string]any)) map[string]float64 {
		return smartWith(t, cmdSmartAd, smartctlVariant(t, "freebsd/smartctl-x-ada0.json", edit))
	}

	got := variant(func(doc map[string]any) { setStatus(t, ataRows(t, doc)[0], 7) })
	want(t, got, `zfs_smart_self_test_passed{device="ada0",type="short"}`, 0)
	want(t, got, `zfs_smart_self_test_age_seconds{device="ada0",type="short"}`, 14*hour)
	want(t, got, `zfs_smart_self_test_passed{device="ada0",type="extended"}`, 1)

	// An aborted test is skipped; the next newest short test counts.
	got = variant(func(doc map[string]any) { setStatus(t, ataRows(t, doc)[0], 1) })
	want(t, got, `zfs_smart_self_test_passed{device="ada0",type="short"}`, 1)
	want(t, got, `zfs_smart_self_test_age_seconds{device="ada0",type="short"}`, 38*hour)

	got = variant(func(doc map[string]any) {
		for _, row := range ataRows(t, doc) {
			if sub(t, row, "type")["value"] == float64(1) {
				setStatus(t, row, 2)
			}
		}
	})
	absent(t, got, `zfs_smart_self_test_passed{device="ada0",type="short"`)
	want(t, got, `zfs_smart_self_test_passed{device="ada0",type="extended"}`, 1)

	got = variant(func(doc map[string]any) {
		log := sub(t, doc, "ata_smart_self_test_log")
		log["standard"] = log["extended"]
		delete(log, "extended")
	})
	want(t, got, `zfs_smart_self_test_age_seconds{device="ada0",type="extended"}`, 299*hour)

	got = variant(func(doc map[string]any) {
		rows := ataRows(t, doc)
		sub(t, rows[0], "type")["value"] = float64(4)
		sub(t, rows[1], "type")["value"] = float64(99)
		sub(t, rows[2], "type")["value"] = float64(3)
		for _, row := range rows {
			if sub(t, row, "type")["value"] == float64(2) {
				sub(t, row, "type")["value"] = float64(130)
			}
		}
	})
	want(t, got, `zfs_smart_self_test_age_seconds{device="ada0",type="selective"}`, 14*hour)
	want(t, got, `zfs_smart_self_test_age_seconds{device="ada0",type="other"}`, 38*hour)
	want(t, got, `zfs_smart_self_test_age_seconds{device="ada0",type="conveyance"}`, 62*hour)
	want(t, got, `zfs_smart_self_test_age_seconds{device="ada0",type="extended"}`, 299*hour)

	got = variant(func(doc map[string]any) {
		sub(t, doc, "ata_smart_data", "self_test", "status")["value"] = float64(0xF9)
	})
	want(t, got, `zfs_smart_self_test_in_progress{device="ada0"}`, 1)

	got = variant(func(doc map[string]any) {
		delete(doc, "power_on_time")
		delete(sub(t, doc, "ata_smart_data"), "self_test")
	})
	want(t, got, `zfs_smart_self_test_passed{device="ada0",type="short"}`, 1)
	absent(t, got, `zfs_smart_self_test_age_seconds{device="ada0"`)
	absent(t, got, `zfs_smart_self_test_in_progress{device="ada0"`)
}

func TestSmartSelfTestNVMeOutcomes(t *testing.T) {
	variant := func(edit func(doc map[string]any)) map[string]float64 {
		return smartWith(t, cmdSmartNv, smartctlVariant(t, "freebsd/smartctl-x-nvme0.json", edit))
	}
	result := func(row any, v float64) { sub(t, row, "self_test_result")["value"] = v }

	got := variant(func(doc map[string]any) { result(nvmeRows(t, doc)[0], 7) })
	want(t, got, `zfs_smart_self_test_passed{device="nvme0",type="short"}`, 0)
	want(t, got, `zfs_smart_self_test_age_seconds{device="nvme0",type="short"}`, 14*hour)

	got = variant(func(doc map[string]any) { result(nvmeRows(t, doc)[0], 1) })
	want(t, got, `zfs_smart_self_test_passed{device="nvme0",type="short"}`, 1)
	want(t, got, `zfs_smart_self_test_age_seconds{device="nvme0",type="short"}`, 38*hour)

	got = variant(func(doc map[string]any) {
		rows := nvmeRows(t, doc)
		sub(t, rows[0], "self_test_code")["value"] = float64(14)
		result(rows[1], 15)
	})
	want(t, got, `zfs_smart_self_test_age_seconds{device="nvme0",type="other"}`, 14*hour)
	want(t, got, `zfs_smart_self_test_age_seconds{device="nvme0",type="short"}`, 62*hour)

	got = variant(func(doc map[string]any) {
		sub(t, doc, "nvme_self_test_log", "current_self_test_operation")["value"] = float64(1)
	})
	want(t, got, `zfs_smart_self_test_in_progress{device="nvme0"}`, 1)

	got = variant(func(doc map[string]any) {
		delete(sub(t, doc, "nvme_self_test_log"), "current_self_test_operation")
		delete(doc, "power_on_time")
	})
	want(t, got, `zfs_smart_self_test_passed{device="nvme0",type="short"}`, 1)
	absent(t, got, `zfs_smart_self_test_age_seconds{device="nvme0"`)
	absent(t, got, `zfs_smart_self_test_in_progress{device="nvme0"`)
}

func TestSmartSelfTestHelpers(t *testing.T) {
	for status, want := range map[int][2]bool{0: {true, true}, 1: {}, 2: {}, 3: {false, true}, 8: {false, true}, 9: {}, 249: {}} {
		if p, ok := ataOutcome(status); p != want[0] || ok != want[1] {
			t.Errorf("ataOutcome(%d) = %v, %v", status, p, ok)
		}
	}
	for result, want := range map[int][2]bool{0: {true, true}, 1: {}, 4: {}, 5: {false, true}, 7: {false, true}, 8: {}, 15: {}} {
		if p, ok := nvmeOutcome(result); p != want[0] || ok != want[1] {
			t.Errorf("nvmeOutcome(%d) = %v, %v", result, p, ok)
		}
	}
	for _, c := range []struct {
		hours, lifetime, want float64
	}{
		{38781, 38767, 14},
		{76920, 11370, 14},
		{65536, 0, 0},
		{70000, 4464, 0},
		{100, 101, 0},
	} {
		if got := ataSelfTestAgeHours(c.hours, c.lifetime); got != c.want {
			t.Errorf("ataSelfTestAgeHours(%v, %v) = %v, want %v", c.hours, c.lifetime, got, c.want)
		}
	}
}
