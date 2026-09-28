package cec_test

// The real handle on a node that is not a CEC adapter. The vivid tests
// prove the handle on a real adapter; these prove its error paths on
// any machine, CI included.

import (
	"errors"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"golang.org/x/sys/unix"
)

func TestOpenNamesANodeThatIsMissing(t *testing.T) {
	_, err := cec.Open(t.TempDir() + "/cec0")

	if !errors.Is(err, unix.ENOENT) {
		t.Errorf("got %v", err)
	}
}

// /dev/null answers every ioctl with ENOTTY and is always readable, so
// it shows that the handle passes the kernel's error and the poll
// result through.
func TestTheHandlePassesTheKernelsAnswersThrough(t *testing.T) {
	device, err := cec.Open("/dev/null")
	mustSucceed(t, err)
	defer device.Close()

	_, capsErr := device.Caps()
	ready, waitErr := device.Wait(10 * time.Millisecond)

	if !errors.Is(capsErr, unix.ENOTTY) || capsErr.Error() != "CEC_ADAP_G_CAPS: inappropriate ioctl for device" {
		t.Errorf("caps answered %v", capsErr)
	}
	mustSucceed(t, waitErr)
	if !ready.Message {
		t.Errorf("wait answered %+v", ready)
	}
}
