package main

// These tests cover the trace setting on the operator's side: a pass
// writes the btmon file when the Adapter's spec.btmon differs from it,
// with one BtmonChanged Event, writes nothing when they agree, and
// reports the file's value in status.btmon.

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"testing/synctest"
)

// adapterWithTrace is the test radio's Adapter with spec.btmon set.
func adapterWithTrace(btmon bool) *Adapter {
	return &Adapter{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Metadata:   ObjectMeta{Name: testAdapterName, Finalizers: []string{adapterFinalizer}},
		Spec:       AdapterSpec{Btmon: btmon},
	}
}

// traceSettings answers a settings directory whose btmon file holds
// value, the way bondfetch leaves it. An empty value writes no file.
func traceSettings(t *testing.T, value string) string {
	t.Helper()
	settings := t.TempDir()
	if value == "" {
		return settings
	}
	if err := os.WriteFile(filepath.Join(settings, "btmon"), []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return settings
}

// traceFile answers what the btmon file holds, and "" when there is
// no file.
func traceFile(t *testing.T, settings string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(settings, "btmon"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

// A field that differs from the file writes the field's value into the
// file, posts one Event that names it, and reports it in status. A
// missing file is false.
func TestADifferentTraceSettingWritesTheFile(t *testing.T) {
	cases := []struct {
		name    string
		spec    bool
		file    string
		want    string
		message string
	}{
		{name: "on", spec: true, file: "false", want: "true\n", message: "btmon changes to true; the btmon container traces this radio, and its log holds key material in plain text"},
		{name: "on with no file", spec: true, file: "", want: "true\n", message: "btmon changes to true; the btmon container traces this radio, and its log holds key material in plain text"},
		{name: "off", spec: false, file: "true", want: "false\n", message: "btmon changes to false; the btmon container stops its trace"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := newAPIFixture()
				fixture.put(t, testAdapterObjectPath(), adapterWithTrace(c.spec))
				inventory, recorded := eventInventory(t, fixture, testRadio(t))
				inventory.settings = traceSettings(t, c.file)

				pass := inventory.reconcile()

				if got := traceFile(t, inventory.settings); got != c.want {
					t.Errorf("the btmon file holds %q, want %q", got, c.want)
				}
				if adapter := read[Adapter](t, fixture, testAdapterObjectPath()); adapter.Status.Btmon == nil || *adapter.Status.Btmon != c.spec {
					t.Errorf("status.btmon = %v, want %v", adapter.Status.Btmon, c.spec)
				}
				if !pass.ok {
					t.Error("the pass reported a failure")
				}
				assertPosted(t, postedAbout(recorded, adapterKind, testAdapterName),
					posted{"Normal", reasonBtmonChanged, c.message},
				)
			})
		})
	}
}

// traceInode answers the inode of the btmon file, and 0 when there is
// no file. The operator writes the file by renaming a new copy over
// it, so a write changes the inode.
func traceInode(t *testing.T, settings string) uint64 {
	t.Helper()
	info, err := os.Stat(filepath.Join(settings, "btmon"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

// A second pass finds the file in agreement, and writes and posts
// nothing more.
func TestATraceSettingIsWrittenOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture()
		fixture.put(t, testAdapterObjectPath(), adapterWithTrace(true))
		inventory, recorded := eventInventory(t, fixture, testRadio(t))
		inventory.settings = traceSettings(t, "false")

		inventory.reconcile()
		written := traceInode(t, inventory.settings)
		inventory.reconcile()

		if after := traceInode(t, inventory.settings); after != written {
			t.Errorf("the btmon file is inode %d after the second pass, want %d", after, written)
		}
		if got := postedAbout(recorded, adapterKind, testAdapterName); len(got) != 1 {
			t.Errorf("Events = %+v, want one", got)
		}
	})
}

// A field that agrees with the file leaves the file as it is, posts
// nothing, and reports the file's value. An empty field and a missing
// file agree.
func TestAnAgreeingTraceSettingWritesNothing(t *testing.T) {
	cases := []struct {
		name string
		spec bool
		file string
	}{
		{name: "both true", spec: true, file: "true"},
		{name: "both false", spec: false, file: "false"},
		{name: "an empty field and no file", spec: false, file: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := newAPIFixture()
				fixture.put(t, testAdapterObjectPath(), adapterWithTrace(c.spec))
				inventory, recorded := eventInventory(t, fixture, testRadio(t))
				inventory.settings = traceSettings(t, c.file)
				before := traceInode(t, inventory.settings)

				inventory.reconcile()

				if after := traceInode(t, inventory.settings); after != before {
					t.Errorf("the btmon file is inode %d after the pass, want %d as before it", after, before)
				}
				if adapter := read[Adapter](t, fixture, testAdapterObjectPath()); adapter.Status.Btmon == nil || *adapter.Status.Btmon != c.spec {
					t.Errorf("status.btmon = %v, want %v", adapter.Status.Btmon, c.spec)
				}
				assertPosted(t, postedAbout(recorded, adapterKind, testAdapterName))
			})
		})
	}
}

// The operator writes only the btmon file. The privacy file records
// the value bluetoothd started with, and stays as bondfetch left it.
func TestTheTraceSettingLeavesThePrivacyFileAlone(t *testing.T) {
	fixture := newAPIFixture()
	adapter := adapterWithTrace(true)
	adapter.Spec.Privacy = "device"
	fixture.put(t, testAdapterObjectPath(), adapter)
	inventory := testInventory(t, fixture, testRadio(t))
	inventory.settings = settingsHolding(t, "off")

	inventory.reconcile()

	contents, err := os.ReadFile(filepath.Join(inventory.settings, "privacy"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "off\n" {
		t.Errorf("the privacy file holds %q, want the value bondfetch wrote", contents)
	}
}

// A write that fails posts nothing, and the pass reports the failure,
// so the loop runs a pass again.
func TestAFailedTraceWriteIsAFailedPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture()
		fixture.put(t, testAdapterObjectPath(), adapterWithTrace(true))
		inventory, recorded := eventInventory(t, fixture, testRadio(t))
		inventory.settings = filepath.Join(t.TempDir(), "missing")

		pass := inventory.reconcile()

		if pass.ok {
			t.Error("the pass reported success with no btmon file written")
		}
		if adapter := read[Adapter](t, fixture, testAdapterObjectPath()); adapter.Status.Btmon != nil && *adapter.Status.Btmon {
			t.Error("status.btmon = true, want the false that the volume holds")
		}
		assertPosted(t, postedAbout(recorded, adapterKind, testAdapterName))
	})
}

// An off trace shows in status as false, not as an absent field, so
// the printer column reads false on a radio whose trace was never on,
// including a radio whose status an older operator wrote.
func TestAnOffTraceShowsInStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture()
		fixture.put(t, testAdapterObjectPath(), adapterWithTrace(false))
		inventory, _ := eventInventory(t, fixture, testRadio(t))
		inventory.settings = traceSettings(t, "false")
		inventory.reconcile()

		// The status an older operator wrote, with no btmon field.
		written := read[map[string]any](t, fixture, testAdapterObjectPath())
		delete((*written)["status"].(map[string]any), "btmon")
		fixture.put(t, testAdapterObjectPath(), written)
		synctest.Wait()
		inventory.reconcile()

		object := read[map[string]any](t, fixture, testAdapterObjectPath())
		status, _ := (*object)["status"].(map[string]any)
		if btmon, found := status["btmon"]; !found || btmon != false {
			t.Errorf("status.btmon = %v (present %v), want false", btmon, found)
		}
	})
}
