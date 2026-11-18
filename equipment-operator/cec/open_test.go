package cec_test

// These tests run the real handle on files that are not CEC adapters,
// so they check its error paths on any machine, including CI.

import (
	"errors"
	"testing"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"golang.org/x/sys/unix"
)

func TestOpenFailsWithENOENTForAMissingNode(t *testing.T) {
	_, err := cec.Open(t.TempDir() + "/cec0")

	if !errors.Is(err, unix.ENOENT) {
		t.Errorf("Open of a missing node returned %v, want ENOENT", err)
	}
}

// /dev/null fails every ioctl with ENOTTY and always polls as readable.
func TestTheHandleReturnsTheKernelsErrorAndPollResult(t *testing.T) {
	device, err := cec.Open("/dev/null")
	mustSucceed(t, err)
	defer device.Close()

	_, capsErr := device.Caps()
	ready, waitErr := device.Wait(10 * time.Millisecond)

	if !errors.Is(capsErr, unix.ENOTTY) || capsErr.Error() != "CEC_ADAP_G_CAPS: inappropriate ioctl for device" {
		t.Errorf("Caps returned %v, want ENOTTY with the ioctl name", capsErr)
	}
	mustSucceed(t, waitErr)
	if !ready.Message {
		t.Errorf("Wait returned %+v, want a readable message", ready)
	}
}
