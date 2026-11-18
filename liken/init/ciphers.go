package main

// Init loads the kernel crypto modules that Wi-Fi needs.
//
// The 802.11 stack creates a cipher when the supplicant installs a
// key, and the kernel's crypto API loads a missing cipher module by
// running modprobe. liken ships no modprobe, so the load fails without
// a message, and the key install fails with ENOENT after a successful
// handshake. The supplicant reports this as WRONG_KEY, the same as a
// wrong passphrase, and a machine with no other route to its cluster
// waits for that radio (radioToWaitFor). So the wireless bring-up loads
// the ciphers before it starts a supplicant.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/liken/machine"
)

// wirelessCiphers are the ciphers a join can need: ccm(aes) for CCMP,
// the WPA2 and WPA3 pairwise cipher, gcm(aes) for GCMP, and cmac(aes)
// for protected management frames.
var wirelessCiphers = []string{"ccm", "gcm", "cmac"}

// loadCipherModule is a variable so that a test can run the pass
// without a kernel.
var loadCipherModule = func(base, name string, deps map[string][]string, done map[string]bool) error {
	_, err := loadModule(base, name, "", deps, done)
	return err
}

// loadWirelessCiphers loads the ciphers from the running kernel's
// module tree. radios.go calls it only when the spec declares a
// wireless interface.
func loadWirelessCiphers() {
	loadWirelessCiphersFrom(filepath.Join("/lib/modules", kernelRelease()))
}

// loadWirelessCiphersFrom loads the ciphers from the module tree at
// base and returns one outcome for each: Loaded, Builtin, Missing when
// the cipher is neither a module nor built in, or Failed when the
// kernel refuses the load. It never stops the boot, because a missing
// cipher fails the join later and the join reports it.
func loadWirelessCiphersFrom(base string) []machine.ModuleStatus {
	deps, err := readModulesDep(filepath.Join(base, "modules.dep"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: wireless: reading modules.dep: %v\n", err)
		deps = map[string][]string{}
	}
	builtin, err := readModulesBuiltin(filepath.Join(base, "modules.builtin"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: wireless: reading modules.builtin: %v\n", err)
	}

	done := map[string]bool{}
	// No parameters apply, because spec.moduleParameters accepts only
	// the modules that spec.modules names, and the ciphers are not in
	// spec.modules.
	outcomes := declaredModuleOutcomes(wirelessCiphers, deps, builtin, nil, declaredPass{
		resident: func(name string) bool { return moduleIsResident(sysModuleDir, name) },
		load: func(name, parameters string) error {
			return loadCipherModule(base, name, deps, done)
		},
		readParameters: func(string) map[string]string { return nil },
	})
	for _, outcome := range outcomes {
		reportCipher(outcome)
	}
	return outcomes
}

// reportCipher prints one cipher's outcome on the console.
func reportCipher(outcome machine.ModuleStatus) {
	state := strings.ToLower(string(outcome.State))
	if outcome.State == machine.ModuleFailed {
		fmt.Fprintf(os.Stderr, "liken: wireless: cipher %s: %s: %s\n", outcome.Name, state, outcome.Message)
		return
	}
	// A Missing outcome has a message too, and a person reads it here
	// when a join fails.
	if outcome.Message != "" {
		fmt.Printf("liken: wireless: cipher %s: %s: %s\n", outcome.Name, state, outcome.Message)
		return
	}
	fmt.Printf("liken: wireless: cipher %s: %s\n", outcome.Name, state)
}
