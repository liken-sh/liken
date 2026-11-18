package main

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

// settingsHolding returns a settings directory whose privacy file
// contains value, as bondfetch writes it.
func settingsHolding(t *testing.T, value string) string {
	t.Helper()
	settings := t.TempDir()
	if err := os.WriteFile(filepath.Join(settings, "privacy"), []byte(value+"\n"), 0o644); err != nil {
		t.Fatalf("writing the privacy file: %v", err)
	}
	return settings
}

// A pod with no settings file started bluetoothd with privacy off.
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

// privacyRestart and the identity store read the Adapter and the
// started value from the result of the pass.
func TestReconcileReturnsTheAdapterAndTheStartedPrivacy(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t))
	inventory.settings = settingsHolding(t, "network")

	pass := inventory.reconcile()

	if pass.adapter == nil || pass.adapter.Metadata.Name != testAdapterName {
		t.Errorf("the pass read adapter %+v, want %s", pass.adapter, testAdapterName)
	}
	if pass.privacy != "network" {
		t.Errorf("privacy = %q, want network", pass.privacy)
	}
}

// The operator's own pod, which privacyRestart deletes.
const (
	testPodName = "bluetooth-operator-x7k2p"
	testPodPath = "/api/v1/namespaces/liken-system/pods/" + testPodName
)

// restartFixture returns a privacyRestart connected to a fake API
// server that holds the operator's pod, and a recorder of the Events
// it posts. The caller runs in a synctest bubble.
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

// adapterWithPrivacy returns the test radio's Adapter with
// spec.privacy set.
func adapterWithPrivacy(privacy string) *Adapter {
	return &Adapter{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Metadata:   ObjectMeta{Name: testAdapterName, UID: "uid-" + testAdapterName},
		Spec:       AdapterSpec{Privacy: privacy},
	}
}

// deletes counts the DELETE requests for the operator's pod.
func deletes(fixture *apiFixture) int {
	count := 0
	for _, request := range fixture.requests {
		if request == "DELETE "+testPodPath {
			count++
		}
	}
	return count
}

// A second pass before the pod stops sends no delete and posts no
// Event.
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
			posted{"Normal", reasonPrivacyChanged, "spec.privacy changed from off to device, so the operator is deleting pod " + testPodName + " to start bluetoothd with the new value"},
		)
	})
}

// The bonds directory is deleted with the pod, so the delete waits for
// a pass that stored every bond.
func TestThePodStaysUntilTheBondsAreStored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		restart, fixture, recorded := restartFixture(t)

		restart.apply(adapterWithPrivacy("device"), "off", false)

		if got := deletes(fixture); got != 0 {
			t.Errorf("the pod was deleted %d times before the bonds were stored, want 0", got)
		}
		assertPosted(t, postedAbout(recorded, adapterKind, testAdapterName))
	})
}

func TestAMatchingPrivacyLeavesThePodAlone(t *testing.T) {
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
					t.Errorf("the pod was deleted %d times, want 0", got)
				}
				assertPosted(t, postedAbout(recorded, adapterKind, testAdapterName))
			})
		})
	}
}

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
			t.Errorf("the pod was deleted %d times, want a failed delete and then one that succeeded: %v", got, fixture.requests)
		}
		assertPosted(t, postedAbout(recorded, adapterKind, testAdapterName),
			posted{"Normal", reasonPrivacyChanged, "spec.privacy changed from off to network, so the operator is deleting pod " + testPodName + " to start bluetoothd with the new value"},
		)
	})
}
