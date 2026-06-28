package main

import (
	"bytes"
	"strings"
	"testing"
)

// fakeMigrator records calls and returns canned results.
type fakeMigrator struct {
	calls   []string
	version uint
	dirty   bool
	applied bool
	err     error
}

func (f *fakeMigrator) Up() error      { f.calls = append(f.calls, "Up"); return f.err }
func (f *fakeMigrator) DownAll() error { f.calls = append(f.calls, "DownAll"); return f.err }
func (f *fakeMigrator) Version() (uint, bool, bool, error) {
	f.calls = append(f.calls, "Version")
	return f.version, f.dirty, f.applied, f.err
}
func (f *fakeMigrator) Force(v int) error { f.calls = append(f.calls, "Force"); return f.err }
func (f *fakeMigrator) Steps(n int) error { f.calls = append(f.calls, "Steps"); return f.err }
func (f *fakeMigrator) Goto(v uint) error { f.calls = append(f.calls, "Goto"); return f.err }

func runWith(args []string, isTTY bool, stdin string, m migrator) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut, strings.NewReader(stdin), isTTY, m)
	return code, out.String(), errOut.String()
}

func TestUpRoutesToUp(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"up"}, false, "", m)
	if code != 0 || len(m.calls) != 1 || m.calls[0] != "Up" {
		t.Fatalf("up: code=%d calls=%v", code, m.calls)
	}
}

func TestUnknownSubcommandIsUsageError(t *testing.T) {
	m := &fakeMigrator{}
	code, _, errOut := runWith([]string{"frobnicate"}, false, "", m)
	if code != 2 {
		t.Fatalf("unknown subcommand: code=%d, want 2", code)
	}
	if !strings.Contains(errOut, "usage") {
		t.Fatalf("expected usage on stderr, got %q", errOut)
	}
	if len(m.calls) != 0 {
		t.Fatalf("expected no migrator calls, got %v", m.calls)
	}
}

func TestNoArgsIsUsageError(t *testing.T) {
	code, _, _ := runWith([]string{}, false, "", &fakeMigrator{})
	if code != 2 {
		t.Fatalf("no args: code=%d, want 2", code)
	}
}

func TestStepsRequiresInteger(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"steps", "abc"}, false, "", m)
	if code != 2 || len(m.calls) != 0 {
		t.Fatalf("steps abc: code=%d calls=%v, want code 2 / no calls", code, m.calls)
	}
}

func TestForceRequiresArg(t *testing.T) {
	code, _, _ := runWith([]string{"force"}, false, "", &fakeMigrator{})
	if code != 2 {
		t.Fatalf("force without arg: code=%d, want 2", code)
	}
}

func TestVersionAppliedOutput(t *testing.T) {
	m := &fakeMigrator{version: 2, applied: true}
	code, out, _ := runWith([]string{"version"}, false, "", m)
	if code != 0 || !strings.Contains(out, "version: 2") || !strings.Contains(out, "clean") {
		t.Fatalf("version: code=%d out=%q", code, out)
	}
}

func TestVersionNotAppliedOutput(t *testing.T) {
	m := &fakeMigrator{applied: false}
	code, out, _ := runWith([]string{"version"}, false, "", m)
	if code != 0 || !strings.Contains(out, "no migrations applied") {
		t.Fatalf("version (none): code=%d out=%q", code, out)
	}
}

func TestStepsForwardNeedsNoConfirm(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"steps", "1"}, true, "", m) // TTY but forward = not destructive
	if code != 0 || len(m.calls) != 1 || m.calls[0] != "Steps" {
		t.Fatalf("steps 1: code=%d calls=%v", code, m.calls)
	}
}

func TestDestructiveAbortsOnTTYNo(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"down"}, true, "n\n", m) // TTY, answer no
	if code == 0 {
		t.Fatalf("declined down should be non-zero, got 0")
	}
	for _, c := range m.calls {
		if c == "DownAll" {
			t.Fatalf("DownAll must not run when declined; calls=%v", m.calls)
		}
	}
}

func TestDestructiveProceedsOnTTYYes(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"down"}, true, "y\n", m)
	if code != 0 || len(m.calls) != 1 || m.calls[0] != "DownAll" {
		t.Fatalf("confirmed down: code=%d calls=%v", code, m.calls)
	}
}

func TestDestructiveNonTTYProceedsUnattended(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"down"}, false, "", m) // no TTY = CI, proceed
	if code != 0 || len(m.calls) != 1 || m.calls[0] != "DownAll" {
		t.Fatalf("non-tty down: code=%d calls=%v", code, m.calls)
	}
}

func TestYesFlagSkipsConfirm(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"force", "2", "--yes"}, true, "", m)
	if code != 0 || len(m.calls) == 0 || m.calls[len(m.calls)-1] != "Force" {
		t.Fatalf("force --yes: code=%d calls=%v", code, m.calls)
	}
}

func TestGotoDownIsDestructive(t *testing.T) {
	m := &fakeMigrator{version: 2, applied: true} // current=2; goto 1 reverts
	code, _, _ := runWith([]string{"goto", "1"}, true, "n\n", m)
	if code == 0 {
		t.Fatalf("declined goto-down should be non-zero, got 0")
	}
	for _, c := range m.calls {
		if c == "Goto" {
			t.Fatalf("Goto must not run when declined; calls=%v", m.calls)
		}
	}
}

func TestGotoUpNeedsNoConfirm(t *testing.T) {
	m := &fakeMigrator{version: 1, applied: true} // current=1; goto 2 is forward
	code, _, _ := runWith([]string{"goto", "2"}, true, "", m)
	if code != 0 || m.calls[len(m.calls)-1] != "Goto" {
		t.Fatalf("goto up: code=%d calls=%v", code, m.calls)
	}
}

func TestRuntimeErrorExitsOne(t *testing.T) {
	m := &fakeMigrator{err: errOops}
	code, _, errOut := runWith([]string{"up"}, false, "", m)
	if code != 1 {
		t.Fatalf("runtime error: code=%d, want 1", code)
	}
	if !strings.Contains(errOut, "oops") {
		t.Fatalf("expected error on stderr, got %q", errOut)
	}
}

// --- fix #1 / #2: extra positional args for up/down/version should exit 2 ---

func TestUpRejectsExtraArgs(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"up", "junk"}, false, "", m)
	if code != 2 {
		t.Fatalf("up junk: code=%d, want 2", code)
	}
	if len(m.calls) != 0 {
		t.Fatalf("up junk: migrator must not be called, got %v", m.calls)
	}
}

func TestVersionRejectsExtraArgs(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"version", "junk"}, false, "", m)
	if code != 2 {
		t.Fatalf("version junk: code=%d, want 2", code)
	}
	if len(m.calls) != 0 {
		t.Fatalf("version junk: migrator must not be called, got %v", m.calls)
	}
}

func TestDownRejectsExtraArgs(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"down", "junk"}, false, "", m)
	if code != 2 {
		t.Fatalf("down junk: code=%d, want 2", code)
	}
	if len(m.calls) != 0 {
		t.Fatalf("down junk: migrator must not be called, got %v", m.calls)
	}
}

// --- fix #3: coverage gaps ---

func TestStepsNegativeTTYNo(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"steps", "-1"}, true, "n\n", m)
	if code == 0 {
		t.Fatalf("declined steps -1 should be non-zero, got 0")
	}
	for _, c := range m.calls {
		if c == "Steps" {
			t.Fatalf("Steps must not run when declined; calls=%v", m.calls)
		}
	}
}

func TestHelpFlagExitsZero(t *testing.T) {
	m := &fakeMigrator{}
	code, out, _ := runWith([]string{"-h"}, false, "", m)
	if code != 0 {
		t.Fatalf("-h: code=%d, want 0", code)
	}
	if !strings.Contains(out, "usage") {
		t.Fatalf("-h: expected usage on stdout, got %q", out)
	}
}

var errOops = stringError("oops")

type stringError string

func (e stringError) Error() string { return string(e) }
