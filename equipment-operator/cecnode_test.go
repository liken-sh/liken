package main

// Tests of the node workload against an in-memory CEC bus and the fake
// API server: the choice of CECBus, Listen mode, and the exit when the
// adapter is unplugged.

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// fakeCECBus returns a CEC bus with a TV and an A/V receiver.
func fakeCECBus() *cectest.Bus {
	bus := cectest.NewBus()
	bus.Add(cectest.Peer{Logical: 0, Physical: 0x0000, PrimaryType: 0, OSDName: "TV", Vendor: 0x00e091, Version: cec.Version14, Power: cec.PowerStandby})
	bus.Add(cectest.Peer{Logical: 5, Physical: 0x1000, PrimaryType: 5, OSDName: "AVR", Vendor: 0x0005cd, Version: cec.Version14, Power: cec.PowerOn})
	return bus
}

// startNode runs the node workload for machine until the test ends,
// and returns a channel that receives its exit error.
func startNode(t *testing.T, api *cecAPI, machine string, device *cec.Device) <-chan error {
	t.Helper()
	node, err := newCECNode(api.client, testNamespace, machine, device)
	mustSucceed(t, err)
	node.recorder = testRecorder(t, api.client)
	return runCECNode(t, node)
}

// runCECNode runs node until the test ends, and returns a channel
// that receives its exit error.
func runCECNode(t *testing.T, node *cecNode) <-chan error {
	t.Helper()
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
			t.Error("the node workload kept running after its context ended")
		}
	})
	return done
}

// usbAdapter returns a Pulse-Eight style USB adapter on bus that can
// enter monitor mode.
func usbAdapter(bus *cectest.Bus) (*cectest.Adapter, *cec.Device) {
	return bus.Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress, Monitor: true})
}

func controlBus(name string, adapters ...CECBusAdapter) CECBus {
	return CECBus{Metadata: ObjectMeta{Name: name}, Spec: CECBusSpec{Mode: CECControl, Adapters: adapters}}
}

func TestANewAdapterMakesABusInListen(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		_, device := usbAdapter(fakeCECBus())

		startNode(t, api, "node-1", device)

		entry := api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
		bus, _ := api.bus("node-1")
		mustMatch(t, bus.Metadata.Labels[discoveredLabel], "cec")
		mustMatch(t, bus.Spec.Mode, CECListen)
		mustDeepEqual(t, bus.Spec.Adapters, []CECBusAdapter{{Machine: "node-1"}})
		mustMatch(t, entry.Driver, "cectest")
		if entry.LogicalAddress != nil {
			t.Errorf("a listening adapter holds logical address %d, want none", *entry.LogicalAddress)
		}
	})
}

func TestAListeningAdapterReportsWhatItHears(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := fakeCECBus()
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
			t.Errorf("a listening adapter sent %v, want nothing", sent)
		}
	})
}

func TestAListeningAdapterWithoutCapNetAdminIsRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		_, device := fakeCECBus().Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress})

		startNode(t, api, "node-1", device)

		entry := api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterRefused })
		mustMatch(t, entry.Message, "CEC_S_MODE: operation not permitted: the kernel allows a monitor only to a process with CAP_NET_ADMIN")
	})
}

func TestAPersonsBusTakesOverFromTheDiscoveredOne(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		_, device := usbAdapter(fakeCECBus())
		startNode(t, api, "node-1", device)
		api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

		api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})

		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
		api.waitUntil(t, "the discovered bus to go", func() bool { return slices.Contains(api.deletedNames(), "node-1") })
	})
}

func TestABusOfTheMachinesNameThatNamesAnotherMachineIsLeftAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putBus(CECBus{Metadata: ObjectMeta{Name: "node-1"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-2"}}}})
		_, device := usbAdapter(fakeCECBus())
		node, err := newCECNode(api.client, testNamespace, "node-1", device)
		mustSucceed(t, err)
		list, err := ListCECBuses(api.client)
		mustSucceed(t, err)

		chosen := node.choose(list)

		if chosen != nil {
			t.Errorf("choose returned CECBus %s, want nil", chosen.Metadata.Name)
		}
		bus, _ := api.bus("node-1")
		mustDeepEqual(t, bus.Spec.Adapters, []CECBusAdapter{{Machine: "node-2"}})
	})
}

// The watch cache can lag behind a bus that a person created a moment
// ago, so the list that choose receives can lack it. choose must not
// take over a bus of the machine's name that the list lacks.
func TestABusOfTheMachinesNameThatAListMissedIsLeftAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putBus(CECBus{Metadata: ObjectMeta{Name: "node-1"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-2"}}}})
		_, device := usbAdapter(fakeCECBus())
		node, err := newCECNode(api.client, testNamespace, "node-1", device)
		mustSucceed(t, err)

		chosen := node.choose(&CECBusList{})

		if chosen != nil {
			t.Errorf("choose returned CECBus %s, want nil", chosen.Metadata.Name)
		}
		bus, _ := api.bus("node-1")
		mustDeepEqual(t, bus.Spec.Adapters, []CECBusAdapter{{Machine: "node-2"}})
	})
}

// A person can create a bus of the machine's name just after the pass
// read that none exists, so a GET of that name also returns 404. The
// pass's create of its own bus fails with a conflict, and the person's
// bus stays as it is.
func TestABusCreatedAfterTheReadIsLeftAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putBus(CECBus{Metadata: ObjectMeta{Name: "node-1"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-2"}}}})
		api.mutex.Lock()
		api.createdUnseen = "node-1"
		api.mutex.Unlock()
		_, device := usbAdapter(fakeCECBus())
		node, err := newCECNode(api.client, testNamespace, "node-1", device)
		mustSucceed(t, err)

		chosen := node.choose(&CECBusList{})

		if chosen != nil {
			t.Errorf("choose returned CECBus %s, want nil", chosen.Metadata.Name)
		}
		bus, _ := api.bus("node-1")
		mustDeepEqual(t, bus.Spec.Adapters, []CECBusAdapter{{Machine: "node-2"}})
	})
}

func TestAMachineThatLeavesABusRemovesItsEntry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		_, device := usbAdapter(fakeCECBus())
		api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
		startNode(t, api, "node-1", device)
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

		api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-2"}}}})

		api.waitUntil(t, "the entry to go", func() bool {
			_, held := api.entry("den", "node-1")
			return !held
		})
	})
}

// Unplugging a USB adapter makes its handle fail with ENODEV, so the
// node workload ends, and the kubelet restarts it.
func TestTheNodeWorkloadEndsWhenTheAdapterLeaves(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		adapter, device := usbAdapter(fakeCECBus())
		done := startNode(t, api, "node-1", device)
		api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

		adapter.Unplug()

		select {
		case err := <-done:
			if !cec.IsGone(err) {
				t.Errorf("ended with %v, want ENODEV", err)
			}
		case <-time.After(testTimeout):
			t.Fatal("the node workload kept running after the adapter was unplugged")
		}
	})
}

func TestFindAdapterReturnsTheNodeOfTheClaim(t *testing.T) {
	cases := []struct {
		name    string
		files   []string
		setting string
		want    string
	}{
		{"one node", []string{"cec0"}, "", "cec0"},
		{"the first of two", []string{"cec1", "cec0"}, "", "cec0"},
		{"a node set by CEC_DEVICE", []string{"cec0"}, "/dev/cec7", "/dev/cec7"},
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

// The CRD defaults spec.osdName to liken, and osdName returns the same
// for a bus stored before the field existed.
func TestTheOSDNameIsTheBusesOrLiken(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mustMatch(t, osdName(CECBusSpec{OSDName: "Den TV"}), "Den TV")
		mustMatch(t, osdName(CECBusSpec{}), "liken")
	})
}

// A failure at start is the process's last log line, so each error
// must state what is missing.
func TestServeCECStopsOnWhatItLacks(t *testing.T) {
	withNode := t.TempDir()
	mustSucceed(t, writeEmpty(withNode+"/cec0"))
	cases := []struct {
		name      string
		node      string
		namespace string
		devices   string
		message   string
	}{
		{"no node name", "", testNamespace, withNode, "NODE_NAME is unset, and the DaemonSet must set it to the node name"},
		{"no namespace", "node-1", "", withNode, "POD_NAMESPACE is unset, and the DaemonSet must set it to the pod's namespace"},
		{"no adapter", "node-1", testNamespace, t.TempDir(), "no cec* device in"},
		{"no cluster", "node-1", testNamespace, withNode, "not running in a cluster"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(nodeNameVariable, c.node)
			t.Setenv(podNamespaceVariable, c.namespace)
			t.Setenv(cecDeviceVariable, "")
			t.Setenv("KUBERNETES_SERVICE_HOST", "")

			err := serveCEC(t.Context(), c.devices)

			if err == nil || !strings.Contains(err.Error(), c.message) {
				t.Errorf("serveCEC returned %v, want an error that contains %q", err, c.message)
			}
		})
	}
}

func TestServeCECFailsWithThePathOfAnAdapterItCannotOpen(t *testing.T) {
	t.Setenv(nodeNameVariable, "node-1")
	t.Setenv(podNamespaceVariable, testNamespace)
	t.Setenv(cecDeviceVariable, t.TempDir()+"/cec9")

	err := serveCEC(t.Context(), "/dev")

	if err == nil || !strings.Contains(err.Error(), "cec9: no such file or directory") {
		t.Errorf("serveCEC returned %v, want an error that names cec9: no such file or directory", err)
	}
}

// Two buses created by a person that list one machine are a mistake
// that the node workload cannot resolve, so it follows the first by
// name and reports only to that bus.
func TestTheAdapterFollowsTheFirstOfTwoPersonsBuses(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		for _, name := range []string{"study", "den"} {
			api.putBus(CECBus{Metadata: ObjectMeta{Name: name}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
		}
		_, device := usbAdapter(fakeCECBus())

		startNode(t, api, "node-1", device)

		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
		if _, held := api.entry("study", "node-1"); held {
			t.Error("the adapter wrote an entry to CECBus study, want entries only in CECBus den")
		}
	})
}

func TestAnAdapterWithoutMonitorAllHearsBroadcasts(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		_, device := fakeCECBus().Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress, Monitor: true, Capabilities: cec.CapLogAddrs | cec.CapTransmit})

		startNode(t, api, "node-1", device)

		entry := api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
		mustMatch(t, entry.Message, "the adapter's driver has no monitor-all mode, so the adapter hears only broadcasts")
	})
}

func TestTheNodeWorkloadFailsWhenItCannotStart(t *testing.T) {
	t.Parallel()
	t.Run("an adapter that left before the start", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			adapter, device := usbAdapter(fakeCECBus())
			adapter.Unplug()

			_, err := newCECNode(startCECAPI(t).client, testNamespace, "node-1", device)

			if !cec.IsGone(err) {
				t.Errorf("newCECNode returned %v, want ENODEV", err)
			}
		})
	})
	t.Run("a cluster with no CECBus definition", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			_, device := usbAdapter(fakeCECBus())
			node, err := newCECNode(testAPIClient(t, (&cannedAPI{}).handler()), testNamespace, "node-1", device)
			mustSucceed(t, err)

			err = node.run(t.Context())

			if err == nil || !strings.HasPrefix(err.Error(), "listing CECBuses: ") {
				t.Errorf("run returned %v, want an error that starts with \"listing CECBuses: \"", err)
			}
		})
	})
}

// While the API server refuses requests, the entry is not written.
// When it accepts them again, the entry arrives with what the adapter
// heard in the meantime.
func TestTheEntryArrivesAfterTheAPIServerRecovers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := fakeCECBus()
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
	})
}
