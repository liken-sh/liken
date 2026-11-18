package main

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

// traceSettings returns a settings directory whose btmon file contains
// value, as bondfetch writes it. An empty value writes no file.
func traceSettings(t *testing.T, value string) string {
	t.Helper()
	settings := t.TempDir()
	if value == "" {
		return settings
	}
	if err := os.WriteFile(filepath.Join(settings, "btmon"), []byte(value+"\n"), 0o644); err != nil {
		t.Fatalf("writing the btmon file: %v", err)
	}
	return settings
}

func traceFile(t *testing.T, settings string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(settings, "btmon"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("reading the btmon file: %v", err)
	}
	return string(contents)
}

// When spec.btmon differs from the file, the pass writes spec.btmon into
// the file, posts one Event, and reports the value in status. A missing
// file reads as false.
func TestADifferentTraceSettingWritesTheFile(t *testing.T) {
	cases := []struct {
		name    string
		spec    bool
		file    string
		want    string
		message string
	}{
		{name: "on", spec: true, file: "false", want: "true\n", message: "spec.btmon is now true, so the btmon container traces this radio, and its log contains key material in plain text"},
		{name: "on with no file", spec: true, file: "", want: "true\n", message: "spec.btmon is now true, so the btmon container traces this radio, and its log contains key material in plain text"},
		{name: "off", spec: false, file: "true", want: "false\n", message: "spec.btmon is now false, so the btmon container stops its trace"},
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

// traceInode returns the inode of the btmon file, and 0 when there is
// no file. writeBtmon renames a new file over the old one, so each
// write changes the inode.
func traceInode(t *testing.T, settings string) uint64 {
	t.Helper()
	info, err := os.Stat(filepath.Join(settings, "btmon"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("reading the status of the btmon file: %v", err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

// A second pass finds the file equal to spec.btmon, so it writes nothing
// and posts no second Event.
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

// When spec.btmon equals the file, the pass leaves the file alone, posts
// nothing, and reports the file's value. A false spec.btmon equals a
// missing file.
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

// The privacy file records the value bluetoothd started with, so a
// change to spec.privacy leaves it as bondfetch wrote it.
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
		t.Fatalf("reading the privacy file: %v", err)
	}
	if string(contents) != "off\n" {
		t.Errorf("the privacy file holds %q, want the value bondfetch wrote", contents)
	}
}

// A write that fails posts nothing, and the pass reports the failure so
// that the loop retries.
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
			t.Error("status.btmon = true, want false, the value in the btmon file")
		}
		assertPosted(t, postedAbout(recorded, adapterKind, testAdapterName))
	})
}

// status.btmon is false, not absent, when the trace is off, so the
// printer column shows false for a radio whose trace was never on. The
// test also removes the field from a written status, and the next pass
// writes it again.
func TestAnOffTraceShowsInStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture()
		fixture.put(t, testAdapterObjectPath(), adapterWithTrace(false))
		inventory, _ := eventInventory(t, fixture, testRadio(t))
		inventory.settings = traceSettings(t, "false")
		inventory.reconcile()

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
