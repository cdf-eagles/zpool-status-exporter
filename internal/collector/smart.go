package collector

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

var (
	smartInfo = gauge("smart_device_info",
		"Identity of a device read by smartctl; always 1.",
		"device", "disk", "model", "serial", "wwn", "firmware", "protocol")
	smartUp = gauge("smart_device_up",
		"Whether smartctl returned SMART data for the device (1) or not (0).", "device")
	smartStandby = gauge("smart_device_standby",
		"Whether the device was in standby or sleep and was not woken (1) or not (0).", "device")
	smartHealthy = gauge("smart_healthy",
		"Whether the SMART overall health self-assessment passed (1) or failed (0).", "device")
	smartExitBit = gauge("smart_exit_status_bit",
		"smartctl exit status bits 2 to 7; 1 when the bit is set.", "device", "bit", "meaning")
	smartTemp       = gauge("smart_temperature_celsius", "Current device temperature.", "device")
	smartPowerOn    = gauge("smart_power_on_seconds", "Device power-on time.", "device")
	smartPowerCycle = gauge("smart_power_cycles", "Device power cycle count.", "device")
	smartErrorLog   = gauge("smart_error_log_entries", "Entries in the device error log.", "device")
	smartAttrValue  = gauge("smart_attribute_value",
		"Normalized value of an ATA SMART attribute.", "device", "id", "attribute")
	smartAttrWorst = gauge("smart_attribute_worst",
		"Worst normalized value of an ATA SMART attribute.", "device", "id", "attribute")
	smartAttrThresh = gauge("smart_attribute_threshold",
		"Failure threshold of an ATA SMART attribute.", "device", "id", "attribute")
	smartAttrRaw = gauge("smart_attribute_raw_value",
		"Raw value of an ATA SMART attribute.", "device", "id", "attribute")
	smartNVMeFamily = dynamic("smart_nvme",
		"NVM Express (NVMe) SMART health log field", "device")
)

// smartExitMeanings names smartctl exit status bits 2 to 7. Bits 0 and 1
// mean the command itself failed.
var smartExitMeanings = []string{
	2: "command_failed",
	3: "disk_failing",
	4: "prefail_attribute_at_threshold",
	5: "attribute_at_threshold_in_past",
	6: "error_log_has_errors",
	7: "self_test_log_has_errors",
}

// nvmeSkipFields are NVMe health log fields that are not exported as
// zfs_smart_nvme_* because they are identifiers or have a common metric.
var nvmeSkipFields = map[string]bool{
	"nsid":                true,
	"temperature":         true,
	"power_on_hours":      true,
	"power_cycles":        true,
	"num_err_log_entries": true,
}

type smartTarget struct {
	path string
	typ  string
}

// label names the device in metrics. Several disks behind one controller
// node (megaraid,N, cciss,N, areca,N) share a path, so the type is added.
func (t smartTarget) label() string {
	base := path.Base(t.path)
	if strings.Contains(t.typ, ",") {
		return base + ":" + t.typ
	}
	return base
}

var nvmeController = regexp.MustCompile(`^nvme[0-9]+$`)

// parseScanOpen reads smartctl --scan-open output and keeps the devices
// that are disks: names in the disk inventory and NVMe controllers.
// Enclosure devices (ses) are listed by smartctl but are not disks.
func parseScanOpen(data []byte, disks map[string]Disk) []smartTarget {
	var out []smartTarget
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "/dev/") {
			continue
		}
		f := strings.Fields(line)
		t := smartTarget{path: f[0]}
		if len(f) >= 3 && f[1] == "-d" {
			t.typ = f[2]
		}
		base := path.Base(t.path)
		_, known := disks[base]
		switch {
		case known, nvmeController.MatchString(base):
		case len(disks) == 0 && !strings.HasPrefix(base, "ses"):
		default:
			continue
		}
		out = append(out, t)
	}
	return dedupeTargets(out)
}

func dedupeTargets(in []smartTarget) []smartTarget {
	seen := map[string]bool{}
	out := in[:0]
	for _, t := range in {
		if seen[t.label()] {
			continue
		}
		seen[t.label()] = true
		out = append(out, t)
	}
	return out
}

type smartJSON struct {
	Smartctl struct {
		ExitStatus int `json:"exit_status"`
	} `json:"smartctl"`
	Device struct {
		Name     string `json:"name"`
		Protocol string `json:"protocol"`
	} `json:"device"`
	PowerMode *struct {
		Name string `json:"name"`
	} `json:"power_mode"`
	ModelName       string `json:"model_name"`
	SerialNumber    string `json:"serial_number"`
	FirmwareVersion string `json:"firmware_version"`
	WWN             *struct {
		NAA uint64 `json:"naa"`
		OUI uint64 `json:"oui"`
		ID  uint64 `json:"id"`
	} `json:"wwn"`
	SmartStatus *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current *float64 `json:"current"`
	} `json:"temperature"`
	PowerOnTime *struct {
		Hours   float64 `json:"hours"`
		Minutes float64 `json:"minutes"`
	} `json:"power_on_time"`
	PowerCycleCount *float64 `json:"power_cycle_count"`
	ATAAttributes   *struct {
		Table []struct {
			ID     int     `json:"id"`
			Name   string  `json:"name"`
			Value  float64 `json:"value"`
			Worst  float64 `json:"worst"`
			Thresh float64 `json:"thresh"`
			Raw    struct {
				Value float64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	ATAErrorLog *struct {
		Extended *struct {
			Count *float64 `json:"count"`
		} `json:"extended"`
		Summary *struct {
			Count *float64 `json:"count"`
		} `json:"summary"`
	} `json:"ata_smart_error_log"`
	NVMeLog map[string]json.RawMessage `json:"nvme_smart_health_information_log"`
}

func (s *smartJSON) wwn() string {
	if s.WWN == nil {
		return ""
	}
	return fmt.Sprintf("%x%06x%09x", s.WWN.NAA, s.WWN.OUI, s.WWN.ID)
}

func (s *smartJSON) asleep() bool {
	if s.PowerMode == nil || s.SmartStatus != nil {
		return false
	}
	mode := strings.ToUpper(s.PowerMode.Name)
	return strings.Contains(mode, "STANDBY") || strings.Contains(mode, "SLEEP")
}

func (s *smartJSON) errorLogEntries() (float64, bool) {
	if l := s.ATAErrorLog; l != nil {
		if l.Extended != nil && l.Extended.Count != nil {
			return *l.Extended.Count, true
		}
		if l.Summary != nil && l.Summary.Count != nil {
			return *l.Summary.Count, true
		}
	}
	if raw, ok := s.NVMeLog["num_err_log_entries"]; ok {
		var v float64
		if json.Unmarshal(raw, &v) == nil {
			return v, true
		}
	}
	return 0, false
}

// smartJob reads SMART data with smartctl. Devices in standby are not woken.
type smartJob struct {
	run        runner.Runner
	smartctl   string
	platform   Platform
	cmdTimeout time.Duration
	logger     *slog.Logger

	// The device scan opens every device and can wake sleeping disks, so
	// its result is reused for scanMaxAge.
	scanMaxAge time.Duration
	now        func() time.Time
	targets    []smartTarget
	scannedAt  time.Time
}

// defaultScanMaxAge is how long a smartctl --scan-open result is reused.
const defaultScanMaxAge = 6 * time.Hour

func (j *smartJob) scan(ctx context.Context, disks map[string]Disk) ([]smartTarget, error) {
	now := time.Now
	if j.now != nil {
		now = j.now
	}
	maxAge := j.scanMaxAge
	if maxAge == 0 {
		maxAge = defaultScanMaxAge
	}
	if len(j.targets) > 0 && now().Sub(j.scannedAt) < maxAge {
		return j.targets, nil
	}
	res, err := j.runCmd(ctx, "--scan-open")
	if err != nil {
		return nil, err
	}
	targets := parseScanOpen(res.Stdout, disks)
	if len(targets) == 0 {
		return nil, errors.New("smartctl --scan-open found no disks")
	}
	j.targets, j.scannedAt = targets, now()
	return targets, nil
}

func (j *smartJob) Name() string { return "smart" }

func (j *smartJob) runCmd(ctx context.Context, args ...string) (runner.Result, error) {
	if j.cmdTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, j.cmdTimeout)
		defer cancel()
	}
	return j.run.Run(ctx, j.smartctl, args...)
}

func (j *smartJob) Collect(ctx context.Context) ([]prometheus.Metric, error) {
	devices, err := j.platform.Devices(ctx)
	if err != nil {
		j.logger.Warn("device inventory failed; SMART devices are not matched to disks", "err", err)
	}
	targets, err := j.scan(ctx, devices.Disks)
	if err != nil {
		return nil, err
	}
	var b batch
	for _, t := range targets {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		j.collectDevice(ctx, &b, t, devices)
	}
	return b.metrics, b.err()
}

func (j *smartJob) collectDevice(ctx context.Context, b *batch, t smartTarget, devices DeviceMap) {
	device := t.label()
	args := []string{"-j", "-x", "-n", "standby"}
	if t.typ != "" {
		args = append(args, "-d", t.typ)
	}
	args = append(args, t.path)
	res, runErr := j.runCmd(ctx, args...)
	code := res.ExitCode
	if runErr != nil && runner.ExitCode(runErr) < 0 {
		j.logger.Warn("smartctl failed", "device", t.path, "err", runErr)
		b.add(smartUp, 0, device)
		return
	}
	var s smartJSON
	if err := json.Unmarshal(res.Stdout, &s); err != nil {
		j.logger.Warn("smartctl output is not JSON", "device", t.path, "exit_code", code, "err", err)
		b.add(smartUp, 0, device)
		return
	}
	if s.SerialNumber != "" || s.ModelName != "" {
		disk := ""
		if _, ok := devices.Disks[path.Base(t.path)]; ok {
			disk = path.Base(t.path)
		} else if d, ok := devices.DiskBySerial(s.SerialNumber); ok {
			disk = d.Name
		}
		b.add(smartInfo, 1, device, disk, s.ModelName, s.SerialNumber, s.wwn(), s.FirmwareVersion, s.Device.Protocol)
	}
	if s.asleep() {
		b.add(smartStandby, 1, device)
		b.add(smartUp, 0, device)
		return
	}
	b.add(smartStandby, 0, device)
	if code&3 != 0 {
		j.logger.Warn("smartctl could not read the device", "device", t.path, "exit_code", code)
		b.add(smartUp, 0, device)
		return
	}
	b.add(smartUp, 1, device)
	for bit := 2; bit <= 7; bit++ {
		b.addBool(smartExitBit, code&(1<<bit) != 0, device, strconv.Itoa(bit), smartExitMeanings[bit])
	}
	if s.SmartStatus != nil {
		b.addBool(smartHealthy, s.SmartStatus.Passed, device)
	}
	if s.Temperature != nil && s.Temperature.Current != nil {
		b.add(smartTemp, *s.Temperature.Current, device)
	}
	if s.PowerOnTime != nil {
		b.add(smartPowerOn, s.PowerOnTime.Hours*3600+s.PowerOnTime.Minutes*60, device)
	}
	if s.PowerCycleCount != nil {
		b.add(smartPowerCycle, *s.PowerCycleCount, device)
	}
	if v, ok := s.errorLogEntries(); ok {
		b.add(smartErrorLog, v, device)
	}
	if s.ATAAttributes != nil {
		for _, a := range s.ATAAttributes.Table {
			id := strconv.Itoa(a.ID)
			b.add(smartAttrValue, a.Value, device, id, a.Name)
			b.add(smartAttrWorst, a.Worst, device, id, a.Name)
			b.add(smartAttrThresh, a.Thresh, device, id, a.Name)
			b.add(smartAttrRaw, a.Raw.Value, device, id, a.Name)
		}
	}
	for _, field := range sortedKeys(s.NVMeLog) {
		var v float64
		if nvmeSkipFields[field] || json.Unmarshal(s.NVMeLog[field], &v) != nil {
			continue
		}
		b.addDynamic(smartNVMeFamily, field, prometheus.GaugeValue, v, device)
	}
}
