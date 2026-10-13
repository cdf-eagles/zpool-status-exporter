package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// TestHelperProcess is not a real test. Exec tests re-run the test binary
// with GO_WANT_HELPER_PROCESS=1 so it stands in for an external command.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	switch os.Getenv("HELPER_MODE") {
	case "echo":
		_, _ = fmt.Fprint(os.Stdout, os.Getenv("HELPER_STDOUT"))
		_, _ = fmt.Fprint(os.Stderr, os.Getenv("HELPER_STDERR"))
		code, _ := strconv.Atoi(os.Getenv("HELPER_EXIT"))
		os.Exit(code)
	case "env":
		_, _ = fmt.Fprint(os.Stdout, os.Getenv("LC_ALL"))
		os.Exit(0)
	case "sleep":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	os.Exit(3)
}

func helperCommand(t *testing.T, env ...string) CommandFunc {
	t.Helper()
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cs := append([]string{"-test.run=TestHelperProcess", "--", name}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = append([]string{"GO_WANT_HELPER_PROCESS=1"}, env...)
		return cmd
	}
}

func TestExecSuccess(t *testing.T) {
	e := &Exec{Command: helperCommand(t, "HELPER_MODE=echo", "HELPER_STDOUT=pool\tONLINE\n", "HELPER_EXIT=0")}
	res, err := e.Run(context.Background(), "zpool", "list", "-Hp")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(res.Stdout) != "pool\tONLINE\n" || res.ExitCode != 0 {
		t.Fatalf("got %q code %d", res.Stdout, res.ExitCode)
	}
}

func TestExecNonZeroExitKeepsOutput(t *testing.T) {
	e := &Exec{Command: helperCommand(t, "HELPER_MODE=echo", "HELPER_STDOUT={\"x\":1}", "HELPER_STDERR=warn line\nsecond", "HELPER_EXIT=64")}
	res, err := e.Run(context.Background(), "smartctl", "-j", "-x", "/dev/da0")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("want *ExitError, got %v", err)
	}
	if exitErr.Code != 64 || res.ExitCode != 64 || ExitCode(err) != 64 {
		t.Fatalf("exit code: err %d res %d", exitErr.Code, res.ExitCode)
	}
	if exitErr.Stderr != "warn line" {
		t.Fatalf("stderr first line: %q", exitErr.Stderr)
	}
	if string(res.Stdout) != `{"x":1}` {
		t.Fatalf("stdout lost: %q", res.Stdout)
	}
	if exitErr.Error() != "smartctl exited with code 64: warn line" {
		t.Fatalf("message: %q", exitErr.Error())
	}
}

func TestExecForcesCLocale(t *testing.T) {
	e := &Exec{Command: helperCommand(t, "HELPER_MODE=env")}
	res, err := e.Run(context.Background(), "zpool")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(res.Stdout) != "C" {
		t.Fatalf("LC_ALL = %q", res.Stdout)
	}
}

func TestExecTimeout(t *testing.T) {
	e := &Exec{Command: helperCommand(t, "HELPER_MODE=sleep"), WaitDelay: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := e.Run(ctx, "zpool", "iostat")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	if res.ExitCode != -1 {
		t.Fatalf("exit code %d", res.ExitCode)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("Run did not return promptly after the timeout")
	}
}

func TestExecMissingBinary(t *testing.T) {
	e := NewExec()
	_, err := e.Run(context.Background(), "/nonexistent/zpool-status-exporter-test")
	if err == nil {
		t.Fatal("want error for a missing binary")
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		t.Fatal("a missing binary must not look like an exit status")
	}
	if ExitCode(err) != -1 {
		t.Fatalf("ExitCode = %d", ExitCode(err))
	}
}

func TestExecNilCommandUsesExec(t *testing.T) {
	e := &Exec{}
	_, err := e.Run(context.Background(), "/nonexistent/zpool-status-exporter-test")
	if err == nil {
		t.Fatal("want error")
	}
}

func TestExitErrorWithoutStderr(t *testing.T) {
	e := &ExitError{Name: "zpool", Code: 1}
	if e.Error() != "zpool exited with code 1" {
		t.Fatalf("message: %q", e.Error())
	}
}

func TestFake(t *testing.T) {
	f := NewFake(map[string]Response{
		"zpool list -Hp": {Stdout: "ok"},
		"smartctl -x":    {Stdout: "{}", Stderr: "e1\ne2", ExitCode: 4},
		"broken":         {Err: errors.New("boom")},
	})
	res, err := f.Run(context.Background(), "zpool", "list", "-Hp")
	if err != nil || string(res.Stdout) != "ok" {
		t.Fatalf("got %q %v", res.Stdout, err)
	}
	res, err = f.Run(context.Background(), "smartctl", "-x")
	if ExitCode(err) != 4 || string(res.Stdout) != "{}" {
		t.Fatalf("exit: %v %q", err, res.Stdout)
	}
	if _, err = f.Run(context.Background(), "broken"); err == nil || err.Error() != "boom" {
		t.Fatalf("err: %v", err)
	}
	if _, err = f.Run(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown command must fail")
	}
	f.Set("late", Response{Stdout: "x"})
	if _, err = f.Run(context.Background(), "late"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = f.Run(ctx, "late"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	want := []string{"zpool list -Hp", "smartctl -x", "broken", "unknown", "late", "late"}
	got := f.Calls()
	if len(got) != len(want) {
		t.Fatalf("calls %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d = %q, want %q", i, got[i], want[i])
		}
	}
	var empty Fake
	empty.Set("a", Response{})
	if _, err := empty.Run(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
}
