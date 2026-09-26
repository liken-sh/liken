package main

// The node workload against a bus in memory and the fake API server:
// discovery, the two modes, the follower's answers, and the exit when
// the adapter leaves.

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// A room's CEC wire: a TV and a receiver. The values are invented.
func cecRoom() *cectest.Bus {
	bus := cectest.NewBus()
	bus.Add(cectest.Peer{Logical: 0, Physical: 0x0000, PrimaryType: 0, OSDName: "TV", Vendor: 0x00e091, Version: cec.Version14, Power: cec.PowerStandby})
	bus.Add(cectest.Peer{Logical: 5, Physical: 0x1000, PrimaryType: 5, OSDName: "AVR", Vendor: 0x0005cd, Version: cec.Version14, Power: cec.PowerOn})
	return bus
}

// startNode runs the node workload for one machine until the test
// ends, and answers the channel its exit error arrives on.
func startNode(t *testing.T, api *cecAPI, machine string, device *cec.Device) <-chan error {
	t.Helper()
	node, err := newCECNode(api.client, machine, device)
	mustSucceed(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		done <- node.run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(testTimeout):
			t.Error("the node workload did not stop")
		}
	})
	return done
}

// usbAdapter is a Pulse-Eight on the room's wire that may take a
// monitor mode.
func usbAdapter(bus *cectest.Bus) (*cectest.Adapter, *cec.Device) {
	return bus.Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress, Monitor: true})
}

func controlBus(name string, adapters ...CECBusAdapter) CECBus {
	return CECBus{Metadata: ObjectMeta{Name: name}, Spec: CECBusSpec{Mode: CECControl, Adapters: adapters}}
}

func TestANewAdapterMakesABusInListen(t *testing.T) {
	api := startCECAPI(t)
	_, device := usbAdapter(cecRoom())

	startNode(t, api, "node-1", device)

	entry := api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	bus, _ := api.bus("node-1")
	mustMatch(t, bus.Metadata.Labels[discoveredLabel], "cec")
	mustMatch(t, bus.Spec.Mode, CECListen)
	mustDeepEqual(t, bus.Spec.Adapters, []CECBusAdapter{{Machine: "node-1"}})
	mustMatch(t, entry.Driver, "cectest")
	if entry.LogicalAddress != nil {
		t.Errorf("a listening adapter holds logical address %d", *entry.LogicalAddress)
	}
}

func TestAListeningAdapterReportsWhatItHears(t *testing.T) {
	api := startCECAPI(t)
	wire := cecRoom()
	_, device := usbAdapter(wire)
	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

	wire.Send(cec.ActiveSource(8, 0x1500))
	wire.Send(cec.GiveDevicePowerStatus(0, 5))

	entry := api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return len(entry.Devices) == 2 })
	mustDeepEqual(t, entry.Devices, []CECDevice{
		{LogicalAddress: 0, Type: "TV"},
		{LogicalAddress: 8, Type: "Playback", PhysicalAddress: "1.5.0.0"},
	})
	if sent := wire.Sent(); len(sent) != 0 {
		t.Errorf("a listening adapter sent %v", sent)
	}
}

func TestAListeningAdapterWithoutCapNetAdminIsRefused(t *testing.T) {
	api := startCECAPI(t)
	_, device := cecRoom().Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress})

	startNode(t, api, "node-1", device)

	entry := api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterRefused })
	mustMatch(t, entry.Message, "CEC_S_MODE: operation not permitted; the kernel allows a monitor only to a process with CAP_NET_ADMIN")
}

func TestAPersonsBusTakesOverFromTheDiscoveredOne(t *testing.T) {
	api := startCECAPI(t)
	_, device := usbAdapter(cecRoom())
	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

	api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})

	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	api.waitUntil(t, "the discovered bus to go", func() bool { return slices.Contains(api.deletedNames(), "node-1") })
}

func TestABusOfTheMachinesNameThatNamesAnotherMachineIsLeftAlone(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(CECBus{Metadata: ObjectMeta{Name: "node-1"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-2"}}}})
	_, device := usbAdapter(cecRoom())
	node, err := newCECNode(api.client, "node-1", device)
	mustSucceed(t, err)
	list, err := ListCECBuses(api.client)
	mustSucceed(t, err)

	chosen := node.choose(list)

	if chosen != nil {
		t.Errorf("chose %s", chosen.Metadata.Name)
	}
	bus, _ := api.bus("node-1")
	mustDeepEqual(t, bus.Spec.Adapters, []CECBusAdapter{{Machine: "node-2"}})
}

// A machine that a bus stops naming removes its entry from that bus.
func TestAMachineThatLeavesABusRemovesItsEntry(t *testing.T) {
	api := startCECAPI(t)
	_, device := usbAdapter(cecRoom())
	api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

	api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-2"}}}})

	api.waitUntil(t, "the entry to go", func() bool {
		_, held := api.entry("den", "node-1")
		return !held
	})
}

// A USB adapter that is unplugged leaves the handle useless, so the
// node workload ends and the kubelet restarts it.
func TestTheNodeWorkloadEndsWhenTheAdapterLeaves(t *testing.T) {
	api := startCECAPI(t)
	adapter, device := usbAdapter(cecRoom())
	done := startNode(t, api, "node-1", device)
	api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

	adapter.Unplug()

	select {
	case err := <-done:
		if !cec.IsGone(err) {
			t.Errorf("ended with %v, want ENODEV", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("the node workload did not end")
	}
}

func TestFindAdapterTakesTheNodeTheClaimDelivered(t *testing.T) {
	cases := []struct {
		name    string
		files   []string
		setting string
		want    string
	}{
		{"one node", []string{"cec0"}, "", "cec0"},
		{"the first of two", []string{"cec1", "cec0"}, "", "cec0"},
		{"a node named by hand", []string{"cec0"}, "/dev/cec7", "/dev/cec7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range c.files {
				mustSucceed(t, writeEmpty(dir+"/"+name))
			}
			t.Setenv(cecDeviceVariable, c.setting)

			found, err := findAdapter(dir)

			mustSucceed(t, err)
			if !strings.HasSuffix(found, c.want) {
				t.Errorf("found %s, want %s", found, c.want)
			}
		})
	}
}

func TestFindAdapterFailsWithNoNode(t *testing.T) {
	t.Setenv(cecDeviceVariable, "")

	_, err := findAdapter(t.TempDir())

	mustFail(t, err)
}

func TestTheOSDNameIsTheMachineCutToFourteenBytes(t *testing.T) {
	mustMatch(t, osdName("node-1"), "node-1")
	mustMatch(t, osdName("a-machine-name-longer"), "a-machine-name")
}

// Each failure before the node workload runs is the process's last
// log line, so each one names its cause.
func TestServeCECStopsOnWhatItLacks(t *testing.T) {
	withNode := t.TempDir()
	mustSucceed(t, writeEmpty(withNode+"/cec0"))
	cases := []struct {
		name    string
		node    string
		devices string
		message string
	}{
		{"no node name", "", withNode, "NODE_NAME is unset; the DaemonSet must state the node"},
		{"no adapter", "node-1", t.TempDir(), "the pod's claim delivered no cec node"},
		{"no cluster", "node-1", withNode, "in-cluster config: not running in a cluster: KUBERNETES_SERVICE_HOST unset"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(nodeNameVariable, c.node)
			t.Setenv(cecDeviceVariable, "")
			t.Setenv("KUBERNETES_SERVICE_HOST", "")

			err := serveCEC(t.Context(), c.devices)

			if err == nil || !strings.Contains(err.Error(), c.message) {
				t.Errorf("got %v, want %q", err, c.message)
			}
		})
	}
}

func TestServeCECNamesAnAdapterItCannotOpen(t *testing.T) {
	t.Setenv(nodeNameVariable, "node-1")
	t.Setenv(cecDeviceVariable, t.TempDir()+"/cec9")

	err := serveCEC(t.Context(), "/dev")

	if err == nil || !strings.Contains(err.Error(), "cec9: no such file or directory") {
		t.Errorf("got %v", err)
	}
}

// Two of a person's buses that name one machine are a mistake the node
// workload cannot settle, so it follows the first by name and reports
// to that bus alone.
func TestTheAdapterFollowsTheFirstOfTwoPersonsBuses(t *testing.T) {
	api := startCECAPI(t)
	for _, name := range []string{"study", "den"} {
		api.putBus(CECBus{Metadata: ObjectMeta{Name: name}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
	}
	_, device := usbAdapter(cecRoom())

	startNode(t, api, "node-1", device)

	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	if _, held := api.entry("study", "node-1"); held {
		t.Error("the adapter reports to two buses")
	}
}

// An adapter whose driver has no monitor-all mode listens to the
// broadcasts, and its entry says so.
func TestAnAdapterWithoutMonitorAllHearsBroadcasts(t *testing.T) {
	api := startCECAPI(t)
	_, device := cecRoom().Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress, Monitor: true, Capabilities: cec.CapLogAddrs | cec.CapTransmit})

	startNode(t, api, "node-1", device)

	entry := api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	mustMatch(t, entry.Message, "the adapter's driver has no monitor-all mode, so the adapter hears only broadcasts")
}

func TestTheNodeWorkloadStopsWhereItCannotStart(t *testing.T) {
	t.Run("an adapter that left before the start", func(t *testing.T) {
		adapter, device := usbAdapter(cecRoom())
		adapter.Unplug()

		_, err := newCECNode(startCECAPI(t).client, "node-1", device)

		if !cec.IsGone(err) {
			t.Errorf("got %v, want ENODEV", err)
		}
	})
	t.Run("a cluster with no CECBus definition", func(t *testing.T) {
		_, device := usbAdapter(cecRoom())
		node, err := newCECNode(testAPIClient(t, (&cannedAPI{}).handler()), "node-1", device)
		mustSucceed(t, err)

		err = node.run(t.Context())

		if err == nil || !strings.HasPrefix(err.Error(), "listing CECBuses: ") {
			t.Errorf("got %v", err)
		}
	})
}

// An API server that refuses the list and the entry for a while delays
// the report, and the report arrives once the server answers again.
func TestTheEntryArrivesAfterTheAPIServerRecovers(t *testing.T) {
	api := startCECAPI(t)
	wire := cecRoom()
	_, device := usbAdapter(wire)
	api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	api.refuse(true)

	wire.Send(cec.ActiveSource(8, 0x1500))
	time.Sleep(50 * time.Millisecond)
	api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
	time.Sleep(50 * time.Millisecond)
	api.refuse(false)
	wire.Send(cec.ReportPowerStatus(8, 15, cec.PowerOn))

	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool {
		return len(entry.Devices) == 1 && entry.Devices[0].PhysicalAddress == "1.5.0.0" && entry.Devices[0].Power == "On"
	})
}
