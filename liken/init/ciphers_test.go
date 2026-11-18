package main

// The cipher pass reads modules.dep and modules.builtin, so these
// tests write both files and replace the load. A kernel build can ship
// each cipher as a module, built in, or not at all.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// cipherTree writes modules.dep for the modules a kernel build ships as
// files, and modules.builtin for the modules compiled in.
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
		t.Fatalf("writing the module list: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.builtin"), []byte(built.String()), 0o644); err != nil {
		t.Fatalf("writing the module list: %v", err)
	}
	return dir
}

// recordedLoads replaces the kernel load. It records each module name
// and returns answer for every load.
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

func states(outcomes []machine.ModuleStatus) map[string]machine.ModuleState {
	byName := map[string]machine.ModuleState{}
	for _, outcome := range outcomes {
		byName[outcome.Name] = outcome.State
	}
	return byName
}

func TestTheCiphersLoadWhatTheKernelBuildShipsAsAModule(t *testing.T) {
	// The pass loads the ciphers that are modules and leaves the
	// built-in one alone.
	loaded := recordedLoads(t, nil)
	got := states(loadWirelessCiphersFrom(cipherTree(t, []string{"ccm", "cmac"}, []string{"gcm"})))

	if strings.Join(*loaded, ",") != "ccm,cmac" {
		t.Errorf("loaded modules are %v, want ccm,cmac", *loaded)
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

func TestABuiltInCipherIsSkippedWithoutALoad(t *testing.T) {
	loaded := recordedLoads(t, nil)
	got := states(loadWirelessCiphersFrom(cipherTree(t, nil, []string{"ccm", "gcm", "cmac"})))

	if len(*loaded) != 0 {
		t.Errorf("loaded modules are %v, want none", *loaded)
	}
	for _, name := range wirelessCiphers {
		if got[name] != machine.ModuleBuiltin {
			t.Errorf("%s is %q, want %q", name, got[name], machine.ModuleBuiltin)
		}
	}
}

func TestACipherThatIsNeitherAModuleNorBuiltInIsMissing(t *testing.T) {
	// Missing is not Failed, because the join can still work when the
	// network does not use that cipher.
	loaded := recordedLoads(t, nil)
	got := states(loadWirelessCiphersFrom(cipherTree(t, []string{"ccm"}, nil)))

	if strings.Join(*loaded, ",") != "ccm" {
		t.Errorf("loaded modules are %v, want ccm", *loaded)
	}
	for _, name := range []string{"gcm", "cmac"} {
		if got[name] != machine.ModuleMissing {
			t.Errorf("%s is %q, want %q", name, got[name], machine.ModuleMissing)
		}
	}
}

func TestACipherThatTheKernelRefusesIsFailed(t *testing.T) {
	recordedLoads(t, fmt.Errorf("finit_module: no such device"))
	got := states(loadWirelessCiphersFrom(cipherTree(t, []string{"ccm", "gcm", "cmac"}, nil)))

	for _, name := range wirelessCiphers {
		if got[name] != machine.ModuleFailed {
			t.Errorf("%s is %q, want %q", name, got[name], machine.ModuleFailed)
		}
	}
}

// capturedStdout returns what run writes to os.Stdout.
func capturedStdout(t *testing.T, run func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating the pipe: %v", err)
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

func TestTheConsoleLineForAMissingCipherIncludesTheMessage(t *testing.T) {
	outcome := machine.ModuleStatus{
		Name: "gcm", State: machine.ModuleMissing,
		Message: "no gcm.ko in this kernel build and not compiled in",
	}
	line := capturedStdout(t, func() { reportCipher(outcome) })

	for _, want := range []string{"gcm", "missing", outcome.Message} {
		if !strings.Contains(line, want) {
			t.Errorf("the console line is %q, want it to contain %q", line, want)
		}
	}
}

func TestAnUnreadableModuleTreeReportsEveryCipherMissing(t *testing.T) {
	// Every cipher still gets an outcome, so status shows what the
	// machine could not load.
	recordedLoads(t, nil)
	got := states(loadWirelessCiphersFrom(filepath.Join(t.TempDir(), "absent")))

	if len(got) != len(wirelessCiphers) {
		t.Fatalf("outcomes are %v, want one for each of %v", got, wirelessCiphers)
	}
	for _, name := range wirelessCiphers {
		if got[name] != machine.ModuleMissing {
			t.Errorf("%s is %q, want %q", name, got[name], machine.ModuleMissing)
		}
	}
}
