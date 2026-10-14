package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Response is the canned outcome of one command in a Fake.
type Response struct {
	Stdout   string
	Stderr   string
	ExitCode int
	// Err, when set, is returned as-is instead of an exit status.
	Err error
}

// Fake is a Runner for tests. It answers commands from Responses, keyed by
// the command line joined with single spaces, and records every call.
type Fake struct {
	mu        sync.Mutex
	Responses map[string]Response
	calls     []string
}

// NewFake returns a Fake with the given responses.
func NewFake(responses map[string]Response) *Fake {
	return &Fake{Responses: responses}
}

// Set adds or replaces the response for a command line.
func (f *Fake) Set(cmdline string, r Response) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Responses == nil {
		f.Responses = map[string]Response{}
	}
	f.Responses[cmdline] = r
}

// Calls returns the command lines run so far, in order.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Run implements Runner.
func (f *Fake) Run(ctx context.Context, name string, args ...string) (Result, error) {
	cmdline := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	f.calls = append(f.calls, cmdline)
	r, ok := f.Responses[cmdline]
	f.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("%s: %w", name, err)
	}
	if !ok {
		return Result{ExitCode: -1}, fmt.Errorf("fake runner: unexpected command %q", cmdline)
	}
	if r.Err != nil {
		return Result{ExitCode: -1}, r.Err
	}
	res := Result{Stdout: []byte(r.Stdout), Stderr: []byte(r.Stderr), ExitCode: r.ExitCode}
	if r.ExitCode != 0 {
		return res, &ExitError{Name: name, Code: r.ExitCode, Stderr: firstLine(r.Stderr)}
	}
	return res, nil
}
