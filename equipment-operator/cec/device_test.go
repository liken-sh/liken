package cec_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
	"golang.org/x/sys/unix"
)

func TestCapsReadsTheDriverAndItsCapabilities(t *testing.T) {
	_, device := room().Adapter(cectest.Options{})

	caps, err := device.Caps()

	mustSucceed(t, err)
	if caps.Driver != "cectest" || caps.Name != "cectest-adapter" || caps.AvailableLogicalAddresses != 1 ||
		!caps.Capabilities.Has(cec.CapPhysAddr|cec.CapMonitorAll) {
		t.Errorf("Caps() = %+v, want driver cectest, name cectest-adapter, 1 available logical address, and the CapPhysAddr and CapMonitorAll capabilities", caps)
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
		t.Errorf("the OSD name is %q, want it cut to \"a-machine-name\"", held.OSDName)
	}
}

func TestReleaseClearsTheAddresses(t *testing.T) {
	_, device := joined(t, room())

	mustSucceed(t, device.Release())

	held, err := device.Addresses()
	mustSucceed(t, err)
	if len(held.Logical) != 0 {
		t.Errorf("addresses after Release() = %+v, want none", held)
	}
}

func TestSetPhysicalAddressSetsWhatTheAdapterAnnounces(t *testing.T) {
	_, device := joined(t, room())

	address, err := device.PhysicalAddress()

	mustSucceed(t, err)
	if address != 0x1300 {
		t.Errorf("PhysicalAddress() = %v, want 0x1300", address)
	}
}

// Each error names the ioctl and includes the kernel's error text, and
// errors.Is still matches the errno.
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
				t.Errorf("the error is %q, want %q wrapping %v", err, c.text, c.errno)
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
				t.Errorf("Transmit(poll) = %+v, want acked %t and status %q with no reply", result, c.acked, c.status)
			}
		})
	}
}

func TestARequestWaitsForItsAnswer(t *testing.T) {
	_, device := joined(t, room())

	result, err := device.Transmit(cec.GiveOSDName(4, 0), cec.OpSetOSDName, time.Second)

	mustSucceed(t, err)
	if result.Reply == nil || string(result.Reply.Operands()) != "TV" {
		t.Errorf("Transmit(GiveOSDName) = %+v, want a reply with the operands \"TV\"", result)
	}
}

func TestAFeatureAbortIsNoReply(t *testing.T) {
	_, device := joined(t, room())

	result, err := device.Transmit(cec.NewMessage(4, 0, cec.OpMenuRequest, 0), cec.OpMenuStatus, time.Second)

	mustSucceed(t, err)
	if !result.Acked || !result.Aborted || result.Reply != nil {
		t.Errorf("Transmit(MenuRequest) = %+v, want acked and aborted with no reply", result)
	}
}

func TestAMessageLongerThanCECCarriesIsRefused(t *testing.T) {
	_, device := joined(t, room())

	_, err := device.Transmit(cec.SetOSDName(4, 0, strings.Repeat("x", 15)), 0, 0)

	if err == nil {
		t.Error("Transmit sent a message with a 16-byte body, want an error")
	}
}

func TestReceiveReadsWhatTheBusCarried(t *testing.T) {
	bus := room()
	_, device := joined(t, bus)
	bus.Send(cec.ActiveSource(8, 0x1500))

	message, err := device.Receive(time.Second)

	mustSucceed(t, err)
	if message.String() != cec.ActiveSource(8, 0x1500).String() {
		t.Errorf("Receive() = %v, want %v", message, cec.ActiveSource(8, 0x1500))
	}
	if _, err := device.Receive(time.Millisecond); !errors.Is(err, cec.ErrNothingReceived) {
		t.Errorf("Receive() on an empty queue returned %v, want ErrNothingReceived", err)
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
		t.Errorf("the last event is %+v, want a state change with physical address 0x1300 and logical address 4", last)
	}
}

func TestAnUnpluggedAdapterAnswersENODEV(t *testing.T) {
	adapter, device := joined(t, room())

	adapter.Unplug()

	_, err := device.Caps()
	_, waitErr := device.Wait(time.Millisecond)
	if !cec.IsGone(err) || !cec.IsGone(waitErr) {
		t.Errorf("after the adapter was unplugged, Caps() returned %v and Wait() returned %v, want ENODEV from both", err, waitErr)
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
				t.Errorf("addresses = %+v, want one logical address, %d", held, c.want)
			}
		})
	}
}

func TestAClaimOfATypeThisPackageDoesNotClaimIsRefused(t *testing.T) {
	_, device := room().Adapter(cectest.Options{Physical: 0x1300})
	mustSucceed(t, device.Initiate())

	if err := device.Claim(cec.Claim{Type: cec.TypeTuner}); err == nil {
		t.Error("Claim of a tuner returned no error, want a refusal")
	}
}

// After Leave, a message on the bus no longer reaches the handle, and
// the handle can no longer send.
func TestLeaveStopsTheHandleFollowing(t *testing.T) {
	bus := room()
	_, device := joined(t, bus)
	mustSucceed(t, device.Release())

	mustSucceed(t, device.Leave())

	bus.Send(cec.ActiveSource(8, 0x1500))
	if _, err := device.Receive(time.Millisecond); !errors.Is(err, cec.ErrNothingReceived) {
		t.Errorf("after Leave(), Receive() returned %v, want ErrNothingReceived", err)
	}
	if _, err := device.Transmit(cec.Poll(4, 0), 0, 0); !errors.Is(err, unix.EBUSY) {
		t.Errorf("after Leave(), Transmit() returned %v, want EBUSY", err)
	}
}

// A program that starts on an adapter that a previous run claimed can
// read the claim's type and passthrough back, and keep the claim when
// it matches the one the program would make.
func TestAddressesReadTheClaimsTypeAndPassthrough(t *testing.T) {
	cases := []struct {
		name  string
		claim cec.Claim
		want  cec.DeviceType
	}{
		{"a playback claim with passthrough", cec.Claim{OSDName: "den", Passthrough: true}, cec.TypePlayback},
		{"an audio system claim without passthrough", cec.Claim{Type: cec.TypeAudioSystem, OSDName: "den"}, cec.TypeAudioSystem},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, device := room().Adapter(cectest.Options{Physical: 0x1300})
			mustSucceed(t, device.Follow())
			mustSucceed(t, device.Claim(c.claim))

			held, err := device.Addresses()

			mustSucceed(t, err)
			if held.Type != c.want || held.Passthrough != c.claim.Passthrough {
				t.Errorf("addresses = %+v, want type %s and passthrough %t", held, c.want, c.claim.Passthrough)
			}
		})
	}
}
