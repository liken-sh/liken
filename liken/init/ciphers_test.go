package main

// The cipher pass is a decision over depmod's two indexes, so these
// tests fabricate the indexes and script the load. Which ciphers a
// kernel build ships as modules varies by build, and the pass must
// take each name as it finds it; the cases below cover the three
// findings and the two failures.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// cipherTree fabricates the two indexes depmod writes: the modules a
// kernel build ships as files, and the modules it compiled in.
func cipherTree(t *testing.T, modules, builtin []string) string {
	t.Helper()
	dir := t.TempDir()
	var dep, built strings.Builder
	for _, name := range modules {
		fmt.Fprintf(&dep, "kernel/crypto/%s.ko.zst:\n", name)
	}
	for _, name := range builtin {
		fmt.Fprintf(&built, "kernel/crypto/%s.ko\n", name)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.dep"), []byte(dep.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.builtin"), []byte(built.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// recordedLoads stands in for the kernel: it records each module the
// pass asks for, and answers with the outcome the test scripts.
func recordedLoads(t *testing.T, answer error) *[]string {
	t.Helper()
	loaded := []string{}
	orig := loadCipherModule
	loadCipherModule = func(base, name string, deps map[string][]string, done map[string]bool) error {
		loaded = append(loaded, name)
		return answer
	}
	t.Cleanup(func() { loadCipherModule = orig })
	return &loaded
}

// states reduces the pass's report to one state per cipher.
func states(outcomes []machine.ModuleStatus) map[string]machine.ModuleState {
	byName := map[string]machine.ModuleState{}
	for _, outcome := range outcomes {
		byName[outcome.Name] = outcome.State
	}
	return byName
}

func TestTheCiphersLoadWhatTheKernelBuildShipsAsAModule(t *testing.T) {
	// The shipped kernel's own split, measured on metal 2026-08-26:
	// ccm and cmac are modules, gcm is built in, and without ccm the
	// four-way handshake succeeded and the key install failed with
	// ENOENT. The pass must load exactly the module half.
	loaded := recordedLoads(t, nil)
	got := states(loadWirelessCiphersFrom(cipherTree(t, []string{"ccm", "cmac"}, []string{"gcm"})))

	if strings.Join(*loaded, ",") != "ccm,cmac" {
		t.Errorf("the pass loaded %v", *loaded)
	}
	want := map[string]machine.ModuleState{
		"ccm": machine.ModuleLoaded, "cmac": machine.ModuleLoaded, "gcm": machine.ModuleBuiltin,
	}
	for name, state := range want {
		if got[name] != state {
			t.Errorf("%s is %q, want %q", name, got[name], state)
		}
	}
}

func TestACipherTheKernelBuiltInIsASkip(t *testing.T) {
	// A builtin cipher already exists in the kernel, and there is no
	// file to load; the pass must skip it and say so, not fail on
	// it.
	loaded := recordedLoads(t, nil)
	got := states(loadWirelessCiphersFrom(cipherTree(t, nil, []string{"ccm", "gcm", "cmac"})))

	if len(*loaded) != 0 {
		t.Errorf("the pass loaded %v", *loaded)
	}
	for _, name := range wirelessCiphers {
		if got[name] != machine.ModuleBuiltin {
			t.Errorf("%s is %q", name, got[name])
		}
	}
}

func TestACipherThisKernelHasNeitherWayIsASkip(t *testing.T) {
	// A cipher this kernel build has neither as a module nor built
	// in is a report, not a failure: the join may still work if the
	// network never asks for that cipher, and the join's own
	// reporting carries it when it does not.
	loaded := recordedLoads(t, nil)
	got := states(loadWirelessCiphersFrom(cipherTree(t, []string{"ccm"}, nil)))

	if strings.Join(*loaded, ",") != "ccm" {
		t.Errorf("the pass loaded %v", *loaded)
	}
	for _, name := range []string{"gcm", "cmac"} {
		if got[name] != machine.ModuleMissing {
			t.Errorf("%s is %q", name, got[name])
		}
	}
}

func TestACipherTheKernelRefusesIsReported(t *testing.T) {
	// A load the kernel refuses becomes a Failed outcome with the
	// error kept; nothing in the pass returns an error to its
	// caller, because nothing about a cipher may stop a boot.
	recordedLoads(t, fmt.Errorf("finit_module: no such device"))
	got := states(loadWirelessCiphersFrom(cipherTree(t, []string{"ccm", "gcm", "cmac"}, nil)))

	for _, name := range wirelessCiphers {
		if got[name] != machine.ModuleFailed {
			t.Errorf("%s is %q", name, got[name])
		}
	}
}

// capturedStdout collects what a function prints to the console.
func capturedStdout(t *testing.T, run func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = write
	collected := make(chan string, 1)
	go func() {
		raw, _ := io.ReadAll(read)
		collected <- string(raw)
	}()
	run()
	os.Stdout = orig
	write.Close()
	out := <-collected
	read.Close()
	return out
}

func TestACipherReportCarriesTheReasonForASkip(t *testing.T) {
	// A cipher this build has neither way carries the reason it is
	// missing, and the console line is where a person reads it. A
	// state word with the message dropped says a cipher is absent and
	// never says what the pass looked for.
	outcome := machine.ModuleStatus{
		Name: "gcm", State: machine.ModuleMissing,
		Message: "no gcm.ko in this kernel build and not compiled in",
	}
	line := capturedStdout(t, func() { reportCipher(outcome) })

	for _, want := range []string{"gcm", "missing", outcome.Message} {
		if !strings.Contains(line, want) {
			t.Errorf("the console line must carry %q: %q", want, line)
		}
	}
}

func TestACipherPassWithNoModuleTreeReportsEveryCipher(t *testing.T) {
	// An unreadable module tree must not lose the report: every
	// cipher still gets an outcome, so status can show what the
	// machine could not do and why.
	recordedLoads(t, nil)
	got := states(loadWirelessCiphersFrom(filepath.Join(t.TempDir(), "absent")))

	if len(got) != len(wirelessCiphers) {
		t.Fatalf("got %v", got)
	}
	for _, name := range wirelessCiphers {
		if got[name] != machine.ModuleMissing {
			t.Errorf("%s is %q", name, got[name])
		}
	}
}
