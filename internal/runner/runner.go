// Package runner runs external commands behind an interface so collectors can
// be tested without executing real binaries.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Result holds the captured output of a finished command.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner runs a command and returns its output.
//
// A command that exits non-zero returns its Result together with an
// *ExitError, so callers that treat exit codes as data (smartctl) can still
// read the output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
}

// ExitError reports a command that ran and exited with a non-zero code.
type ExitError struct {
	Name   string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("%s exited with code %d", e.Name, e.Code)
	}
	return fmt.Sprintf("%s exited with code %d: %s", e.Name, e.Code, e.Stderr)
}

// CommandFunc builds the command to run. It matches exec.CommandContext.
type CommandFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

// Exec runs commands with os/exec.
type Exec struct {
	// Command builds each command; nil means exec.CommandContext.
	Command CommandFunc
	// WaitDelay bounds how long Run waits for output after the context ends.
	WaitDelay time.Duration
}

// NewExec returns an Exec that uses exec.CommandContext.
func NewExec() *Exec {
	return &Exec{Command: exec.CommandContext, WaitDelay: 5 * time.Second}
}

// Run executes name with args. The locale is forced to C so number and date
// formats are stable.
func (e *Exec) Run(ctx context.Context, name string, args ...string) (Result, error) {
	command := e.Command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(ctx, name, args...)
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C", "LANG=C")
	cmd.WaitDelay = e.WaitDelay

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return res, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		res.ExitCode = -1
		return res, fmt.Errorf("%s: %w", name, ctxErr)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, &ExitError{Name: name, Code: res.ExitCode, Stderr: firstLine(stderr.String())}
	}
	res.ExitCode = -1
	return res, fmt.Errorf("run %s: %w", name, err)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ExitCode returns the exit code carried by err, or -1 when err is not an
// *ExitError.
func ExitCode(err error) int {
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return -1
}
