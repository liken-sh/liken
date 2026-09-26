package cec_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
	"golang.org/x/sys/unix"
)

func TestCapsReadsTheDriverAndItsCapabilities(t *testing.T) {
	_, device := room().Adapter(cectest.Options{})

	caps, err := device.Caps()

	mustSucceed(t, err)
	if caps.Driver != "cectest" || caps.Name != "cectest-adapter" || caps.AvailableLogicalAddresses != 1 ||
		!caps.Capabilities.Has(cec.CapPhysAddr|cec.CapMonitorAll) {
		t.Errorf("caps = %+v", caps)
	}
}

func TestAClaimTakesTheFirstFreePlaybackAddress(t *testing.T) {
	bus := room()
	bus.Add(cectest.Peer{Logical: 4, Physical: 0x1500, PrimaryType: 4})
	_, device := joined(t, bus)

	held, err := device.Addresses()

	mustSucceed(t, err)
	if len(held.Logical) != 1 || held.Logical[0] != 8 || held.OSDName != "node-1" {
		t.Errorf("addresses = %+v, want 8 as node-1", held)
	}
}

func TestAClaimCutsTheOSDNameToFourteenBytes(t *testing.T) {
	_, device := room().Adapter(cectest.Options{Physical: 0x1300})
	mustSucceed(t, device.Follow())

	mustSucceed(t, device.Claim(cec.Claim{OSDName: "a-machine-name-longer-than-fourteen"}))

	held, err := device.Addresses()
	mustSucceed(t, err)
	if held.OSDName != "a-machine-name" {
		t.Errorf("osd name = %q", held.OSDName)
	}
}

func TestReleaseClearsTheAddresses(t *testing.T) {
	_, device := joined(t, room())

	mustSucceed(t, device.Release())

	held, err := device.Addresses()
	mustSucceed(t, err)
	if len(held.Logical) != 0 {
		t.Errorf("addresses after release = %+v", held)
	}
}

func TestSetPhysicalAddressSetsWhatTheAdapterAnnounces(t *testing.T) {
	_, device := joined(t, room())

	address, err := device.PhysicalAddress()

	mustSucceed(t, err)
	if address != 0x1300 {
		t.Errorf("physical address = %v", address)
	}
}

// The errors carry the ioctl's name and the kernel's own text, and
// errors.Is still finds the errno.
func TestTheKernelsRefusalsReachTheCaller(t *testing.T) {
	cases := []struct {
		name  string
		call  func(*cec.Device) error
		errno unix.Errno
		text  string
	}{
		{"an address on a video port", func(d *cec.Device) error { return d.SetPhysicalAddress(0x1000) }, unix.ENOTTY,
			"CEC_ADAP_S_PHYS_ADDR: inappropriate ioctl for device"},
		{"a monitor without CAP_NET_ADMIN", func(d *cec.Device) error { return d.Monitor(true) }, unix.EPERM,
			"CEC_S_MODE: operation not permitted"},
		{"a claim with no initiator", func(d *cec.Device) error { return d.Claim(cec.Claim{}) }, unix.EBUSY,
			"CEC_ADAP_S_LOG_ADDRS: device or resource busy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, device := room().Adapter(cectest.Options{Physical: 0x1000, Capabilities: cec.CapLogAddrs | cec.CapTransmit})

			err := c.call(device)

			if !errors.Is(err, c.errno) || err.Error() != c.text {
				t.Errorf("got %v, want %q", err, c.text)
			}
		})
	}
}

func TestMonitorTakesTheModeTheAdapterSupports(t *testing.T) {
	cases := []struct {
		name         string
		capabilities cec.Capability
		all          bool
	}{
		{"monitor-all", cec.CapTransmit | cec.CapMonitorAll, true},
		{"monitor", cec.CapTransmit, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, device := room().Adapter(cectest.Options{Capabilities: c.capabilities, Monitor: true})

			mustSucceed(t, device.Monitor(c.all))
		})
	}
}

func TestAPollIsAnsweredByTheAcknowledgeBit(t *testing.T) {
	cases := []struct {
		name   string
		to     cec.LogicalAddress
		acked  bool
		status string
	}{
		{"the TV", 0, true, ""},
		{"an empty address", 9, false, "NACK MAX_RETRIES"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, device := joined(t, room())

			result, err := device.Transmit(cec.Poll(4, c.to), 0, 0)

			mustSucceed(t, err)
			if result.Acked != c.acked || result.Status != c.status || result.Reply != nil {
				t.Errorf("result = %+v", result)
			}
		})
	}
}

func TestARequestWaitsForItsAnswer(t *testing.T) {
	_, device := joined(t, room())

	result, err := device.Transmit(cec.GiveOSDName(4, 0), cec.OpSetOSDName, time.Second)

	mustSucceed(t, err)
	if result.Reply == nil || string(result.Reply.Operands()) != "TV" {
		t.Errorf("result = %+v", result)
	}
}

func TestAFeatureAbortIsNoReply(t *testing.T) {
	_, device := joined(t, room())

	result, err := device.Transmit(cec.NewMessage(4, 0, cec.OpMenuRequest, 0), cec.OpMenuStatus, time.Second)

	mustSucceed(t, err)
	if !result.Acked || !result.Aborted || result.Reply != nil {
		t.Errorf("result = %+v", result)
	}
}

func TestAMessageLongerThanCECCarriesIsRefused(t *testing.T) {
	_, device := joined(t, room())

	_, err := device.Transmit(cec.SetOSDName(4, 0, strings.Repeat("x", 15)), 0, 0)

	if err == nil {
		t.Error("a sixteen-byte body was sent")
	}
}

func TestReceiveReadsWhatTheBusCarried(t *testing.T) {
	bus := room()
	_, device := joined(t, bus)
	bus.Send(cec.ActiveSource(8, 0x1500))

	message, err := device.Receive(time.Second)

	mustSucceed(t, err)
	if message.String() != cec.ActiveSource(8, 0x1500).String() {
		t.Errorf("received %v", message)
	}
	if _, err := device.Receive(time.Millisecond); !errors.Is(err, cec.ErrNothingReceived) {
		t.Errorf("an empty queue answered %v", err)
	}
}

func TestNextEventReadsTheAdaptersNewAddresses(t *testing.T) {
	_, device := joined(t, room())

	var last cec.Event
	for {
		ready, err := device.Wait(time.Millisecond)
		mustSucceed(t, err)
		if !ready.Event {
			break
		}
		last, err = device.NextEvent()
		mustSucceed(t, err)
	}

	if !last.StateChange || last.Physical != 0x1300 || last.LogicalMask != 1<<4 {
		t.Errorf("last event = %+v", last)
	}
}

func TestAnUnpluggedAdapterAnswersENODEV(t *testing.T) {
	adapter, device := joined(t, room())

	adapter.Unplug()

	_, err := device.Caps()
	_, waitErr := device.Wait(time.Millisecond)
	if !cec.IsGone(err) || !cec.IsGone(waitErr) {
		t.Errorf("caps %v, wait %v; want ENODEV", err, waitErr)
	}
	mustSucceed(t, device.Close())
}

func TestAClaimTakesTheAddressOfItsType(t *testing.T) {
	cases := []struct {
		kind cec.DeviceType
		want cec.LogicalAddress
	}{
		{cec.TypeTV, 0},
		{cec.TypePlayback, 4},
		{cec.TypeAudioSystem, 5},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			_, device := cectest.NewBus().Adapter(cectest.Options{Physical: 0x0000})
			mustSucceed(t, device.Initiate())

			mustSucceed(t, device.Claim(cec.Claim{Type: c.kind}))

			held, err := device.Addresses()
			mustSucceed(t, err)
			if len(held.Logical) != 1 || held.Logical[0] != c.want {
				t.Errorf("addresses = %+v, want %d", held, c.want)
			}
		})
	}
}

func TestAClaimOfATypeThisPackageDoesNotClaimIsRefused(t *testing.T) {
	_, device := room().Adapter(cectest.Options{Physical: 0x1300})
	mustSucceed(t, device.Initiate())

	if err := device.Claim(cec.Claim{Type: cec.TypeTuner}); err == nil {
		t.Error("a tuner claim was sent")
	}
}

// Leave makes the handle neither an initiator nor a follower, so a
// message on the bus no longer reaches it.
func TestLeaveStopsTheHandleFollowing(t *testing.T) {
	bus := room()
	_, device := joined(t, bus)
	mustSucceed(t, device.Release())

	mustSucceed(t, device.Leave())

	bus.Send(cec.ActiveSource(8, 0x1500))
	if _, err := device.Receive(time.Millisecond); !errors.Is(err, cec.ErrNothingReceived) {
		t.Errorf("a handle that left received a message: %v", err)
	}
	if _, err := device.Transmit(cec.Poll(4, 0), 0, 0); !errors.Is(err, unix.EBUSY) {
		t.Errorf("a handle that left may still send: %v", err)
	}
}
