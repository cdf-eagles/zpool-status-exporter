package collector

import (
	"math"
	"sort"
)

var (
	smartSelfTestPassed = gauge("smart_self_test_passed",
		"Whether the latest conclusive self-test of the type passed (1) or failed (0); aborted tests are skipped.",
		"device", "type")
	smartSelfTestAge = gauge("smart_self_test_age_seconds",
		"Device power-on time since the latest conclusive self-test of the type.",
		"device", "type")
	smartSelfTestRunning = gauge("smart_self_test_in_progress",
		"Whether the device is running a self-test (1) or not (0).", "device")
)

// ataLifetimeWrap is the range of the 16-bit lifetime hours field in the ATA
// self-test log.
const ataLifetimeWrap = 65536

type selfTestCode struct {
	Value int `json:"value"`
}

type ataSelfTestTable struct {
	Table []struct {
		Type          selfTestCode `json:"type"`
		Status        selfTestCode `json:"status"`
		LifetimeHours float64      `json:"lifetime_hours"`
	} `json:"table"`
}

type nvmeSelfTestLog struct {
	Current *selfTestCode `json:"current_self_test_operation"`
	Table   []struct {
		Code         selfTestCode `json:"self_test_code"`
		Result       selfTestCode `json:"self_test_result"`
		PowerOnHours float64      `json:"power_on_hours"`
	} `json:"table"`
}

type ataSelfTestLog struct {
	Extended *ataSelfTestTable `json:"extended"`
	Standard *ataSelfTestTable `json:"standard"`
}

type ataSmartData struct {
	SelfTest *struct {
		Status *selfTestCode `json:"status"`
	} `json:"self_test"`
}

// selfTestEntry is one finished self-test that passed or failed.
type selfTestEntry struct {
	typ    string
	passed bool
	age    float64
	hasAge bool
}

// ataSelfTestInProgress is the status nibble that means a test is running.
const ataSelfTestInProgress = 0xF

func ataTestType(v int) string {
	switch v & 0x7f {
	case 1:
		return "short"
	case 2:
		return "extended"
	case 3:
		return "conveyance"
	case 4:
		return "selective"
	}
	return "other"
}

func nvmeTestType(v int) string {
	switch v {
	case 1:
		return "short"
	case 2:
		return "extended"
	}
	return "other"
}

// ataOutcome classifies an ATA self-test status: 0 completed without error,
// 3 to 8 failed. Tests aborted by the host or a reset (1, 2) and reserved
// values say nothing about the disk.
func ataOutcome(status int) (passed, conclusive bool) {
	switch {
	case status == 0:
		return true, true
	case status >= 3 && status <= 8:
		return false, true
	}
	return false, false
}

// nvmeOutcome classifies an NVMe self-test result: 0 completed without
// error, 5 to 7 failed. The aborted results and the unused entry are skipped.
func nvmeOutcome(result int) (passed, conclusive bool) {
	switch {
	case result == 0:
		return true, true
	case result >= 5 && result <= 7:
		return false, true
	}
	return false, false
}

// ataSelfTestAgeHours is the time since a test, given the device's power-on
// hours and the test's wrapping lifetime hours.
func ataSelfTestAgeHours(powerOnHours, lifetimeHours float64) float64 {
	age := powerOnHours - lifetimeHours
	if age <= 0 {
		return 0
	}
	if powerOnHours >= ataLifetimeWrap {
		age = math.Mod(age, ataLifetimeWrap)
	}
	return age
}

// selfTests lists the finished self-tests in the ATA or NVMe self-test log.
func (s *smartJSON) selfTests() []selfTestEntry {
	var onSeconds float64
	hasOn := s.PowerOnTime != nil
	if hasOn {
		onSeconds = s.PowerOnTime.Hours*3600 + s.PowerOnTime.Minutes*60
	}
	var out []selfTestEntry
	if t := s.ataSelfTestTable(); t != nil {
		for _, e := range t.Table {
			passed, ok := ataOutcome(e.Status.Value)
			if !ok {
				continue
			}
			en := selfTestEntry{typ: ataTestType(e.Type.Value), passed: passed, hasAge: hasOn}
			if hasOn {
				en.age = ataSelfTestAgeHours(math.Floor(onSeconds/3600), e.LifetimeHours) * 3600
			}
			out = append(out, en)
		}
	}
	if l := s.NVMeSelfTestLog; l != nil {
		for _, e := range l.Table {
			passed, ok := nvmeOutcome(e.Result.Value)
			if !ok {
				continue
			}
			en := selfTestEntry{typ: nvmeTestType(e.Code.Value), passed: passed}
			if age := onSeconds - e.PowerOnHours*3600; hasOn && age >= 0 {
				en.age, en.hasAge = age, true
			}
			out = append(out, en)
		}
	}
	return out
}

func (s *smartJSON) ataSelfTestTable() *ataSelfTestTable {
	l := s.ATASelfTestLog
	switch {
	case l == nil:
		return nil
	case l.Extended != nil:
		return l.Extended
	}
	return l.Standard
}

// selfTestRunning reports whether the device is running a self-test, and
// whether it says so at all.
func (s *smartJSON) selfTestRunning() (running, known bool) {
	if d := s.ATAData; d != nil && d.SelfTest != nil && d.SelfTest.Status != nil {
		return d.SelfTest.Status.Value>>4 == ataSelfTestInProgress, true
	}
	if l := s.NVMeSelfTestLog; l != nil && l.Current != nil {
		return l.Current.Value != 0, true
	}
	return false, false
}

// addSelfTests exports the latest conclusive self-test of each type and
// whether a test is running.
func addSelfTests(b *batch, device string, s *smartJSON) {
	latest := map[string]selfTestEntry{}
	for _, e := range s.selfTests() {
		cur, seen := latest[e.typ]
		if !seen || (e.hasAge && cur.hasAge && e.age < cur.age) {
			latest[e.typ] = e
		}
	}
	types := make([]string, 0, len(latest))
	for typ := range latest {
		types = append(types, typ)
	}
	sort.Strings(types)
	for _, typ := range types {
		e := latest[typ]
		b.addBool(smartSelfTestPassed, e.passed, device, typ)
		if e.hasAge {
			b.add(smartSelfTestAge, e.age, device, typ)
		}
	}
	if running, known := s.selfTestRunning(); known {
		b.addBool(smartSelfTestRunning, running, device)
	}
}
