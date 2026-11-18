package main

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/liken/machine"
)

// The boot publishes the firmware facts before the promotion rewrites
// BootOrder and BootNext, so the watch must publish them again.
func TestThePromotionRepublishesTheFirmwareFacts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		// A trial boot of slot B, with BootOrder still on slot A first.
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
			t.Fatalf("reading the firmware facts: %v", err)
		}
		wantOrder := []string{"Boot0003 (liken slot B)", "Boot0002 (liken slot A)"}
		if !slices.Equal(facts.Firmware.BootOrder, wantOrder) {
			t.Errorf("status.firmware.bootOrder is %q, want the promoted slot first", facts.Firmware.BootOrder)
		}
		if want := "Boot0003 (liken slot B, pinned at the proven slot)"; facts.Firmware.BootNext != want {
			t.Errorf("status.firmware.bootNext is %q, want %q", facts.Firmware.BootNext, want)
		}
	})
}
