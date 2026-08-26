package main

// The 802.11 stack encrypts with ciphers it instantiates at the
// moment the supplicant installs a key, and on an ordinary
// distribution those arrive by autoload: the kernel's crypto API
// asks userspace to run modprobe. liken ships no modprobe, so the
// request fails silently, the cipher never exists, and the key
// install fails with ENOENT after a handshake that succeeded. The
// supplicant reports that as WRONG_KEY, which reads as a wrong
// passphrase and would even park a radio-only machine. So the
// wireless bring-up loads the ciphers itself, before any supplicant
// starts (plans/62-wifi.md names the incident that proved this on
// metal).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// The three ciphers a join can need: ccm(aes) for CCMP, the WPA2 and
// WPA3 pairwise cipher; gcm(aes) for GCMP; and cmac(aes) for
// protected management frames. Which of them the kernel build ships
// as modules varies by build, and the pass below takes each as it
// finds it.
var wirelessCiphers = []string{"ccm", "gcm", "cmac"}

// loadCipherModule is the real load behind the pass, a variable so a
// test can run the pass against a fabricated tree with no kernel.
var loadCipherModule = func(base, name string, deps map[string][]string, done map[string]bool) error {
	_, err := loadModule(base, name, "", deps, done)
	return err
}

// loadWirelessCiphers loads the ciphers from the running kernel's
// own module tree. Only a spec that declares a wireless interface
// reaches this call (radios.go), which keeps the rule that nothing
// wireless runs unless the spec asks for it.
func loadWirelessCiphers() {
	loadWirelessCiphersFrom(filepath.Join("/lib/modules", kernelRelease()))
}

// loadWirelessCiphersFrom is the same pass with the module tree as a
// parameter, so a test can hand it a fabricated one. Every outcome
// is a report: a builtin is a skip, a name the build has neither way
// is a skip with its own line, and a refused load goes to stderr.
// Nothing here stops a boot; a missing cipher fails the join later,
// and the join's own reporting carries that.
func loadWirelessCiphersFrom(base string) []machine.ModuleStatus {
	deps, err := readModulesDep(filepath.Join(base, "modules.dep"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: wireless: %v\n", err)
		deps = map[string][]string{}
	}
	builtin, err := readModulesBuiltin(filepath.Join(base, "modules.builtin"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: wireless: %v\n", err)
	}

	done := map[string]bool{}
	// The ciphers ship with the release, not with the Machine, so
	// this pass declares no parameters; spec.moduleParameters covers
	// only modules the spec itself names.
	outcomes := declaredModuleOutcomes(wirelessCiphers, deps, builtin, nil, declaredPass{
		resident: func(name string) bool { return moduleIsResident(sysModuleDir, name) },
		load: func(name, parameters string) error {
			return loadCipherModule(base, name, deps, done)
		},
		readback: func(string) map[string]string { return nil },
	})
	for _, outcome := range outcomes {
		reportCipher(outcome)
	}
	return outcomes
}

// reportCipher writes one cipher's console line. The state prints in
// every case, so a skip reads as a deliberate skip and not as a
// spelling mistake in this file.
func reportCipher(outcome machine.ModuleStatus) {
	state := strings.ToLower(string(outcome.State))
	if outcome.State == machine.ModuleFailed {
		fmt.Fprintf(os.Stderr, "liken: wireless: cipher %s: %s: %s\n", outcome.Name, state, outcome.Message)
		return
	}
	// A Missing outcome carries a message too: the state word alone
	// says a cipher is absent and never says what the pass looked
	// for, and the console is where a person diagnoses a join that
	// failed on a cipher the build lacks.
	if outcome.Message != "" {
		fmt.Printf("liken: wireless: cipher %s: %s: %s\n", outcome.Name, state, outcome.Message)
		return
	}
	fmt.Printf("liken: wireless: cipher %s: %s\n", outcome.Name, state)
}
