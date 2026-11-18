package cectest_test

// A test that passes against the in-memory bus proves nothing unless
// the bus behaves like the kernel's CEC core. These tests check the
// kernel rules that the bus copies. The vivid tests in the cec package
// check the same rules against the real core.

import (
	"errors"
	"testing"
	"time"
	"unsafe"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
	"golang.org/x/sys/unix"
)

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("a call that should succeed returned: %v", err)
	}
}

// playing returns an adapter at a physical address, with a device that
// follows and has claimed a playback logical address.
func playing(t *testing.T, bus *cectest.Bus, address cec.PhysicalAddress, name string) (*cectest.Adapter, *cec.Device) {
	t.Helper()
	adapter, device := bus.Adapter(cectest.Options{Physical: address})
	mustSucceed(t, device.Follow())
	mustSucceed(t, device.Claim(cec.Claim{OSDName: name}))
	return adapter, device
}

func TestTwoAdaptersClaimTwoAddresses(t *testing.T) {
	bus := cectest.NewBus()
	first, _ := playing(t, bus, 0x1300, "node-1")
	second, _ := playing(t, bus, 0x1400, "node-2")

	if got := first.Logical(); len(got) != 1 || got[0] != 4 {
		t.Errorf("the first adapter holds logical addresses %v, want [4]", got)
	}
	if got := second.Logical(); len(got) != 1 || got[0] != 8 {
		t.Errorf("the second adapter holds logical addresses %v, want [8]", got)
	}
	if first.Physical() != 0x1300 || first.Claims() != 1 {
		t.Errorf("the first adapter has physical address %v and %d claims, want 0x1300 and 1", first.Physical(), first.Claims())
	}
}

// The kernel replies to identity requests for its own adapter, so one
// adapter can read another's OSD name while no program replies.
func TestTheKernelAnswersForItsOwnAdapter(t *testing.T) {
	bus := cectest.NewBus()
	_, asker := playing(t, bus, 0x1300, "node-1")
	playing(t, bus, 0x1400, "node-2")
	cases := []struct {
		request cec.Message
		reply   cec.Opcode
		want    string
	}{
		{cec.GiveOSDName(4, 8), cec.OpSetOSDName, "8->4 47 6e 6f 64 65 2d 32"},
		{cec.GivePhysicalAddress(4, 8), cec.OpReportPhysicalAddr, "8->f 84 14 00 04"},
		{cec.GetCECVersion(4, 8), cec.OpCECVersion, "8->4 9e 05"},
	}
	for _, c := range cases {
		t.Run(c.request.String(), func(t *testing.T) {
			result, err := asker.Transmit(c.request, c.reply, time.Second)

			mustSucceed(t, err)
			if result.Reply == nil || result.Reply.String() != c.want {
				t.Errorf("the result is %+v, want the reply %s", result, c.want)
			}
		})
	}
}

// A request that the kernel does not answer reaches the follower, and
// the sender's wait for a reply times out.
func TestAFollowerReceivesWhatTheKernelDoesNotAnswer(t *testing.T) {
	bus := cectest.NewBus()
	_, asker := playing(t, bus, 0x1300, "node-1")
	_, follower := playing(t, bus, 0x1400, "node-2")

	result, err := asker.Transmit(cec.GiveDevicePowerStatus(4, 8), cec.OpReportPowerStatus, time.Second)
	mustSucceed(t, err)
	received, receiveErr := follower.Receive(time.Second)

	mustSucceed(t, receiveErr)
	if !result.Acked || result.Reply != nil || received.String() != "4->8 8f" {
		t.Errorf("the result is %+v and the follower received %v, want an ack with no reply and 4->8 8f", result, received)
	}
}

// When a peer sends an identity request to an adapter, the kernel's
// reply goes on the wire, and a monitor-all adapter receives both.
func TestAMonitorHearsARequestAndItsAnswer(t *testing.T) {
	bus := cectest.NewBus()
	playing(t, bus, 0x1300, "node-1")
	_, monitor := bus.Adapter(cectest.Options{Monitor: true})
	mustSucceed(t, monitor.Monitor(true))

	bus.Send(cec.GiveOSDName(0, 4))

	first, err := monitor.Receive(time.Second)
	mustSucceed(t, err)
	second, err := monitor.Receive(time.Second)
	mustSucceed(t, err)
	if first.String() != "0->4 46" || second.String() != "4->0 47 6e 6f 64 65 2d 31" {
		t.Errorf("the monitor heard %v then %v, want 0->4 46 then 4->0 47 6e 6f 64 65 2d 31", first, second)
	}
}

func TestTheKernelRulesTheBusCopies(t *testing.T) {
	cases := []struct {
		name    string
		options cectest.Options
		call    func(*cectest.Adapter, *cec.Device) error
		errno   unix.Errno
	}{
		{"a monitor that may also send", cectest.Options{Monitor: true}, func(a *cectest.Adapter, _ *cec.Device) error {
			mode := uint32(0xf1)
			return a.Ioctl(cec.RequestSetMode, unsafe.Pointer(&mode))
		}, unix.EINVAL},
		{"monitor-all on an adapter that cannot", cectest.Options{Monitor: true, Capabilities: cec.CapTransmit}, func(_ *cectest.Adapter, d *cec.Device) error {
			return d.Monitor(true)
		}, unix.EINVAL},
		{"a transmit before a claim", cectest.Options{Physical: 0x1300}, func(_ *cectest.Adapter, d *cec.Device) error {
			mustSucceed(t, d.Initiate())
			_, err := d.Transmit(cec.Poll(4, 0), 0, 0)
			return err
		}, unix.EPERM},
		{"a transmit with no initiator", cectest.Options{Physical: 0x1300}, func(_ *cectest.Adapter, d *cec.Device) error {
			_, err := d.Transmit(cec.Poll(4, 0), 0, 0)
			return err
		}, unix.EBUSY},
		{"an address with no initiator", cectest.Options{}, func(_ *cectest.Adapter, d *cec.Device) error {
			return d.SetPhysicalAddress(0x1300)
		}, unix.EBUSY},
		{"a second claim", cectest.Options{Physical: 0x1300}, func(_ *cectest.Adapter, d *cec.Device) error {
			mustSucceed(t, d.Follow())
			mustSucceed(t, d.Claim(cec.Claim{}))
			return d.Claim(cec.Claim{})
		}, unix.EBUSY},
		{"no event queued", cectest.Options{}, func(_ *cectest.Adapter, d *cec.Device) error {
			_, err := d.NextEvent()
			return err
		}, unix.EAGAIN},
		{"a request the CEC API does not have", cectest.Options{}, func(a *cectest.Adapter, _ *cec.Device) error {
			var mode uint32
			return a.Ioctl(cec.RequestGetMode, unsafe.Pointer(&mode))
		}, unix.ENOTTY},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adapter, device := cectest.NewBus().Adapter(c.options)

			err := c.call(adapter, device)

			if !errors.Is(err, c.errno) {
				t.Errorf("the call returned %v, want %v", err, c.errno)
			}
		})
	}
}

// With no physical address, the kernel stores a claim and the adapter
// holds no logical address. A USB adapter is in this state until
// something sets its physical address.
func TestAClaimWithNoPhysicalAddressHoldsNothing(t *testing.T) {
	adapter, device := cectest.NewBus().Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress})
	mustSucceed(t, device.Follow())

	mustSucceed(t, device.Claim(cec.Claim{OSDName: "node-1"}))

	if got := adapter.Logical(); len(got) != 0 {
		t.Errorf("the adapter holds logical addresses %v, want none", got)
	}
	mustSucceed(t, device.Close())
}

func TestAnUnpluggedAdapterHearsNothing(t *testing.T) {
	bus := cectest.NewBus()
	adapter, device := playing(t, bus, 0x1300, "node-1")
	adapter.Unplug()

	bus.Send(cec.ActiveSource(0, 0x0000))

	if _, err := device.Receive(time.Millisecond); !errors.Is(err, unix.ENODEV) {
		t.Errorf("Receive on an unplugged adapter returned %v, want ENODEV", err)
	}
	if sent := bus.Sent(); len(sent) != 0 {
		t.Errorf("the bus sent %v, want no messages", sent)
	}
}

func TestAPeerAnswersFromItsFields(t *testing.T) {
	bus := cectest.NewBus()
	bus.Add(cectest.Peer{Logical: 0, Physical: 0x0000, PrimaryType: 0, OSDName: "TV", Vendor: 0x00e091, Version: cec.Version14, Power: cec.PowerStandby})
	_, device := playing(t, bus, 0x1300, "node-1")
	cases := []struct {
		request cec.Message
		reply   cec.Opcode
		want    string
	}{
		{cec.GivePhysicalAddress(4, 0), cec.OpReportPhysicalAddr, "0->f 84 00 00 00"},
		{cec.GiveOSDName(4, 0), cec.OpSetOSDName, "0->4 47 54 56"},
		{cec.GiveDeviceVendorID(4, 0), cec.OpDeviceVendorID, "0->f 87 00 e0 91"},
		{cec.GetCECVersion(4, 0), cec.OpCECVersion, "0->4 9e 05"},
		{cec.GiveDevicePowerStatus(4, 0), cec.OpReportPowerStatus, "0->4 90 01"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			result, err := device.Transmit(c.request, c.reply, time.Second)

			mustSucceed(t, err)
			if result.Reply == nil || result.Reply.String() != c.want {
				t.Errorf("result %+v, want %s", result, c.want)
			}
		})
	}
}

func TestAPeerThatCannotAnswerDoesNot(t *testing.T) {
	cases := []struct {
		name    string
		peer    cectest.Peer
		request cec.Message
		reply   cec.Opcode
		aborted bool
	}{
		{"a mute peer", cectest.Peer{Logical: 0, Mute: true}, cec.GiveOSDName(4, 0), cec.OpSetOSDName, false},
		{"an unknown request", cectest.Peer{Logical: 0}, cec.NewMessage(4, 0, cec.OpMenuRequest, 0), cec.OpMenuStatus, true},
		{"a reply to another question", cectest.Peer{Logical: 0}, cec.GiveOSDName(4, 0), cec.OpCECVersion, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus := cectest.NewBus()
			bus.Add(c.peer)
			_, device := playing(t, bus, 0x1300, "node-1")

			result, err := device.Transmit(c.request, c.reply, time.Second)

			mustSucceed(t, err)
			if !result.Acked || result.Reply != nil || result.Aborted != c.aborted {
				t.Errorf("the result is %+v, want an ack with no reply and Aborted %t", result, c.aborted)
			}
		})
	}
}

// A broadcast reaches every follower, and a transmit that waits for no
// reply returns as soon as the message is on the wire.
func TestABroadcastReachesEveryFollower(t *testing.T) {
	bus := cectest.NewBus()
	_, sender := playing(t, bus, 0x1300, "node-1")
	_, follower := playing(t, bus, 0x1400, "node-2")

	result, err := sender.Transmit(cec.ActiveSource(4, 0x1300), 0, 0)
	mustSucceed(t, err)
	received, receiveErr := follower.Receive(time.Second)

	mustSucceed(t, receiveErr)
	if !result.Acked || received.String() != "4->f 82 13 00" {
		t.Errorf("the result is %+v and the follower received %v, want an ack and 4->f 82 13 00", result, received)
	}
	if sent := bus.Sent(); len(sent) != 1 {
		t.Errorf("the bus sent %v, want one message", sent)
	}
}

func TestWaitEndsAtItsTimeout(t *testing.T) {
	_, device := cectest.NewBus().Adapter(cectest.Options{})

	ready, err := device.Wait(time.Millisecond)

	mustSucceed(t, err)
	if ready.Message || ready.Event {
		t.Errorf("Wait returned %+v, want no message and no event", ready)
	}
}

// The node workload runs this sequence of calls on a USB adapter.
func TestAUSBAdapterRunsTheNodeWorkloadsSequence(t *testing.T) {
	adapter, device := cectest.NewBus().Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress})

	caps, err := device.Caps()
	mustSucceed(t, err)
	mustSucceed(t, device.Follow())
	mustSucceed(t, device.SetPhysicalAddress(0x1300))
	physical, err := device.PhysicalAddress()
	mustSucceed(t, err)
	mustSucceed(t, device.Claim(cec.Claim{OSDName: "node-1"}))
	held, err := device.Addresses()
	mustSucceed(t, err)
	event, err := device.NextEvent()
	mustSucceed(t, err)
	mustSucceed(t, device.Release())

	if caps.Driver != "cectest" || physical != 0x1300 || len(held.Logical) != 1 || held.OSDName != "node-1" {
		t.Errorf("driver %q, physical address %v, held %+v, want driver cectest, 0x1300, and one address named node-1", caps.Driver, physical, held)
	}
	if !event.StateChange || event.Physical != 0x1300 {
		t.Errorf("the event is %+v, want a state change at physical address 0x1300", event)
	}
	if len(adapter.Logical()) != 0 {
		t.Errorf("a released adapter holds %v, want no logical addresses", adapter.Logical())
	}
	mustSucceed(t, device.Leave())
	if adapter.Follows() {
		t.Error("an adapter whose handle left still follows, want it not to follow")
	}
}

func TestADisconnectDropsBothAddresses(t *testing.T) {
	adapter, device := playing(t, cectest.NewBus(), 0x1300, "node-1")

	adapter.Disconnect()

	held, err := device.Addresses()
	mustSucceed(t, err)
	if len(held.Logical) != 0 || adapter.Physical() != cec.InvalidPhysicalAddress {
		t.Errorf("after Disconnect the device holds %+v and the adapter has physical address %v, want no logical addresses and an invalid physical address", held, adapter.Physical())
	}
}

// The kernel stores a claim made while the adapter has no physical
// address, and completes it when the adapter gets one. An adapter on a
// video port gets one when its hotplug signal returns.
func TestAStoredClaimCompletesWhenTheAddressArrives(t *testing.T) {
	adapter, device := cectest.NewBus().Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress, Capabilities: cec.CapLogAddrs | cec.CapTransmit})
	mustSucceed(t, device.Follow())
	mustSucceed(t, device.Claim(cec.Claim{OSDName: "node-1"}))

	adapter.Connect(0x2000)

	held, err := device.Addresses()
	mustSucceed(t, err)
	if len(held.Logical) != 1 || held.Logical[0] != 4 || adapter.Physical() != 0x2000 {
		t.Errorf("after Connect the device holds %+v at %v, want logical address 4 at 0x2000", held, adapter.Physical())
	}
}

func TestReleaseForgetsAStoredClaim(t *testing.T) {
	adapter, device := cectest.NewBus().Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress})
	mustSucceed(t, device.Follow())
	mustSucceed(t, device.Claim(cec.Claim{OSDName: "node-1"}))
	mustSucceed(t, device.Release())

	adapter.Connect(0x2000)

	if held := adapter.Logical(); len(held) != 0 {
		t.Errorf("after Release and Connect the adapter holds %v, want no logical addresses", held)
	}
}

// A transmission to a garbled peer fails with a lost arbitration, and
// a transmission to the address of a removed peer is NACKed.
func TestAWireErrorIsNotANack(t *testing.T) {
	bus := cectest.NewBus()
	bus.Add(cectest.Peer{Logical: 0, Garbled: true})
	bus.Add(cectest.Peer{Logical: 5})
	_, device := playing(t, bus, 0x1300, "node-1")
	bus.Remove(5)

	garbled, err := device.Transmit(cec.Poll(4, 0), 0, 0)
	mustSucceed(t, err)
	removed, err := device.Transmit(cec.Poll(4, 5), 0, 0)
	mustSucceed(t, err)

	if garbled.Acked || garbled.Nacked || garbled.Status != "ARB_LOST MAX_RETRIES" {
		t.Errorf("the transmit to the garbled peer returned %+v, want neither Acked nor Nacked, with status ARB_LOST MAX_RETRIES", garbled)
	}
	if removed.Acked || !removed.Nacked {
		t.Errorf("the transmit to the removed peer returned %+v, want Nacked and not Acked", removed)
	}
}
