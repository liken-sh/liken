package main

// This file tests the proving watch: what a proving boot does when
// the operator promotes the release it is trying.

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/liken/machine"
)

// The boot publishes the firmware's facts before the promotion, and
// the promotion rewrites BootOrder and BootNext. The Machine's
// status.firmware must report the firmware as the promotion left it,
// not as the boot found it.
func TestThePromotionRepublishesTheFirmwareFacts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		// The trial boot of slot B: the firmware consumed the one-shot,
		// and the install's order still puts slot A first.
		dir := fakeFirmwareVars(t, map[string][]byte{
			"Boot0002":    encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"}),
			"Boot0003":    encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot B"}),
			"BootOrder":   u16le(0x0002, 0x0003),
			"BootCurrent": u16le(0x0003),
		})
		tree := machine.FactsTree{Dir: t.TempDir()}
		tree.WriteFirmware(firmwareFacts(dir))
		provenRelease(t, root, "0.2.0", "B")

		trial := machine.SystemRelease{Version: "0.2.0", Slot: "B"}
		provingWatch(efiActuator{dir: dir}, trial, root, tree)(t.Context())

		facts, err := tree.Read()
		if err != nil {
			t.Fatal(err)
		}
		wantOrder := []string{"Boot0003 (liken slot B)", "Boot0002 (liken slot A)"}
		if !slices.Equal(facts.Firmware.BootOrder, wantOrder) {
			t.Errorf("status.firmware.bootOrder must lead with the promoted slot: %q", facts.Firmware.BootOrder)
		}
		if want := "Boot0003 (liken slot B, pinned at the proven slot)"; facts.Firmware.BootNext != want {
			t.Errorf("status.firmware.bootNext must report the pin at the promoted slot: %q", facts.Firmware.BootNext)
		}
	})
}
