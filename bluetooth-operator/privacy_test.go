package main

// These tests cover Low Energy privacy on the operator's side: the
// Adapter's status reports the value bluetoothd started with, and the
// operator deletes its own pod once when spec.privacy differs from that
// value, after the bonds are stored, with one PrivacyChanged Event.

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// settingsHolding answers a settings directory whose privacy file holds
// value, the way bondfetch leaves it.
func settingsHolding(t *testing.T, value string) string {
	t.Helper()
	settings := t.TempDir()
	if err := os.WriteFile(filepath.Join(settings, "privacy"), []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return settings
}

// status.privacy is the value in the settings file, and a pod with no
// file started bluetoothd with privacy off.
func TestReconcileReportsThePrivacyBluetoothdStartedWith(t *testing.T) {
	cases := []struct {
		name     string
		settings func(t *testing.T) string
		want     string
	}{
		{name: "device", settings: func(t *testing.T) string { return settingsHolding(t, "device") }, want: "device"},
		{name: "off", settings: func(t *testing.T) string { return settingsHolding(t, "off") }, want: "off"},
		{name: "no file", settings: func(t *testing.T) string { return t.TempDir() }, want: "off"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newAPIFixture()
			inventory := testInventory(t, fixture, testRadio(t))
			inventory.settings = c.settings(t)

			inventory.reconcile()

			adapter := read[Adapter](t, fixture, testAdapterObjectPath())
			if adapter.Status.Privacy != c.want {
				t.Errorf("status.privacy = %q, want %q", adapter.Status.Privacy, c.want)
			}
		})
	}
}

// The pass hands on the Adapter and the value in the file, which the
// restart and the identity store read.
func TestReconcileHandsOnTheAdapterAndTheStartedPrivacy(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t))
	inventory.settings = settingsHolding(t, "network")

	pass := inventory.reconcile()

	if pass.adapter == nil || pass.adapter.Metadata.Name != testAdapterName {
		t.Errorf("adapter = %+v", pass.adapter)
	}
	if pass.privacy != "network" {
		t.Errorf("privacy = %q, want network", pass.privacy)
	}
}

// testPod is the operator's own pod, which the restart deletes.
const (
	testPodName = "bluetooth-operator-x7k2p"
	testPodPath = "/api/v1/namespaces/liken-system/pods/" + testPodName
)

// restartFixture wires a restart to the fake API server, which holds
// the operator's pod, and to a fake of the events collection. The
// caller runs in a synctest bubble.
func restartFixture(t *testing.T) (*privacyRestart, *apiFixture, *eventstest.Events) {
	t.Helper()
	fixture := newAPIFixture()
	fixture.put(t, testPodPath, map[string]any{"kind": "Pod", "metadata": map[string]any{"name": testPodName}})
	recorded := &eventstest.Events{}
	server := apiservertest.Start(t, recorded.Around(fixture.handler(t)))
	client := apiclient.New(apiservertest.Host, server.Client(), "")
	restart := &privacyRestart{
		client:    client,
		namespace: "liken-system",
		pod:       testPodName,
		recorder:  events.New(t.Context(), client, component, events.Options{Instance: "liken-1", Log: io.Discard}),
	}
	return restart, fixture, recorded
}

// adapterWithPrivacy is the test radio's Adapter with spec.privacy set.
func adapterWithPrivacy(privacy string) *Adapter {
	return &Adapter{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Metadata:   ObjectMeta{Name: testAdapterName, UID: "uid-" + testAdapterName},
		Spec:       AdapterSpec{Privacy: privacy},
	}
}

// deletes counts the requests that deleted the operator's pod.
func deletes(fixture *apiFixture) int {
	count := 0
	for _, request := range fixture.requests {
		if request == "DELETE "+testPodPath {
			count++
		}
	}
	return count
}

// A field that differs from the file deletes the pod once, with one
// Event that names both values. A second pass before the pod ends
// deletes nothing more and posts nothing more.
func TestADifferentPrivacyDeletesThePodOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		restart, fixture, recorded := restartFixture(t)
		adapter := adapterWithPrivacy("device")

		restart.apply(adapter, "off", true)
		restart.apply(adapter, "off", true)

		if got := deletes(fixture); got != 1 {
			t.Errorf("the pod was deleted %d times, want once: %v", got, fixture.requests)
		}
		assertPosted(t, postedAbout(recorded, adapterKind, testAdapterName),
			posted{"Normal", reasonPrivacyChanged, "privacy changes from off to device; deleting pod " + testPodName + " so that bluetoothd starts with the new value"},
		)
	})
}

// The restart waits for a pass that stored every bond, because the
// bonds tree goes with the pod.
func TestThePodStaysUntilTheBondsAreStored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		restart, fixture, recorded := restartFixture(t)

		restart.apply(adapterWithPrivacy("device"), "off", false)

		if got := deletes(fixture); got != 0 {
			t.Errorf("the pod was deleted %d times before the bonds were stored", got)
		}
		assertPosted(t, postedAbout(recorded, adapterKind, testAdapterName))
	})
}

// Nothing happens when the field agrees with the file, when the file is
// absent, or before the pass has read the Adapter. An empty field is
// off.
func TestAnAgreeingPrivacyLeavesThePodAlone(t *testing.T) {
	cases := []struct {
		name    string
		adapter *Adapter
		started string
	}{
		{name: "both device", adapter: adapterWithPrivacy("device"), started: "device"},
		{name: "an empty field and off", adapter: adapterWithPrivacy(""), started: "off"},
		{name: "no settings file", adapter: adapterWithPrivacy("device"), started: ""},
		{name: "no Adapter", adapter: nil, started: "off"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				restart, fixture, recorded := restartFixture(t)

				restart.apply(c.adapter, c.started, true)

				if got := deletes(fixture); got != 0 {
					t.Errorf("the pod was deleted %d times", got)
				}
				assertPosted(t, postedAbout(recorded, adapterKind, testAdapterName))
			})
		})
	}
}

// A delete that fails is sent again on the next pass, and the Event is
// not posted again.
func TestAFailedDeleteIsSentAgainWithNoSecondEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		restart, fixture, recorded := restartFixture(t)
		adapter := adapterWithPrivacy("network")

		fixture.failWrites = 500
		restart.apply(adapter, "off", true)
		fixture.failWrites = 0
		restart.apply(adapter, "off", true)
		restart.apply(adapter, "off", true)

		if got := deletes(fixture); got != 2 {
			t.Errorf("the pod was deleted %d times, want a failed delete and one that landed: %v", got, fixture.requests)
		}
		assertPosted(t, postedAbout(recorded, adapterKind, testAdapterName),
			posted{"Normal", reasonPrivacyChanged, "privacy changes from off to network; deleting pod " + testPodName + " so that bluetoothd starts with the new value"},
		)
	})
}
