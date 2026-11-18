package main

// These tests serve a fake LVFS and read what the operator writes into
// a FirmwareCatalog's status: the file the jcat lists, its SHA-256 and
// ETag, and the Ready condition. The operator reads the jcat when the
// FirmwareCatalog changes and once per interval, which the test world
// sets to an hour.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// catalogWorld runs the operator with a FirmwareCatalog named lvfs, an
// LVFS that serves one file, and no machine.
func catalogWorld(t *testing.T) *world {
	w := startWorld(t)
	w.lvfs.publish("firmware-09138-stable.xml.zst", frameworkBIOS("0.0.4.2"))
	w.api.putCatalog("lvfs", lvfsURL)
	w.settle()
	return w
}

// A jcat that names a file puts the file, its SHA-256, and the jcat's
// ETag in the FirmwareCatalog's status and sets Ready to True.
func TestANewJcatFileUpdatesTheStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := catalogWorld(t)
		sum := sha256Hex(zstdOf(appStreamOf(frameworkBIOS("0.0.4.2"))))

		status := w.api.catalog(t, "lvfs").Status
		if status.File != "firmware-09138-stable.xml.zst" || status.SHA256 != sum || status.ETag == "" || status.Checked == "" {
			t.Errorf("the status is %+v, want the file, its SHA-256, an ETag, and a time", status)
		}
		if r := status.ready(); r.Status != "True" || r.Reason != reasonFetched || r.ObservedGeneration != 1 {
			t.Errorf("Ready = %+v, want True, %s, at generation 1", r, reasonFetched)
		}
	})
}

// A file whose SHA-256 differs from the jcat's sets Ready to False and
// keeps the last good file in status. No machine is scanned while Ready
// is False.
func TestASHA256ThatDiffersSetsReadyFalse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := catalogWorld(t)
		good := sha256Hex(zstdOf(appStreamOf(frameworkBIOS("0.0.4.2"))))
		w.lvfs.publish("firmware-09139-stable.xml.zst", frameworkBIOS("0.0.4.3"))
		w.lvfs.serve(directoryOf(lvfsURL)+"firmware-09139-stable.xml.zst", zstdOf(appStreamOf(frameworkBIOS("0.0.4.3")))[1:])
		time.Sleep(time.Hour)
		synctest.Wait()
		w.api.putFleetMachine("node-2", machineReady, firstBoot)
		w.settle()

		status := w.api.catalog(t, "lvfs").Status
		if r := status.ready(); r.Status != "False" || r.Reason != reasonChecksumMismatch {
			t.Errorf("Ready = %+v, want False, %s", r, reasonChecksumMismatch)
		}
		if status.File != "firmware-09138-stable.xml.zst" || status.SHA256 != good {
			t.Errorf("the status has file %s and SHA-256 %s, want the last good file", status.File, status.SHA256)
		}
		if got := len(w.api.scans(t)); got != 0 {
			t.Errorf("%d scan pods, want none while the FirmwareCatalog is not Ready", got)
		}
		want := []posted{{"Normal", reasonFetched}, {"Warning", reasonChecksumMismatch}}
		if got := w.postedAboutCatalog("lvfs"); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}

// An hour after a read, the operator reads the jcat again with its
// ETag. An unchanged jcat returns 304, and the operator downloads no
// file and changes only the time of the check.
func TestAnUnchangedETagChangesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := catalogWorld(t)
		before := w.api.catalog(t, "lvfs").Status
		time.Sleep(time.Hour)
		synctest.Wait()

		after := w.api.catalog(t, "lvfs").Status
		if got := w.lvfs.read(lvfsURL + ".jcat"); got != 2 {
			t.Errorf("the jcat was read %d times, want 2", got)
		}
		if got := w.lvfs.read(directoryOf(lvfsURL) + "firmware-09138-stable.xml.zst"); got != 1 {
			t.Errorf("the file was read %d times, want 1", got)
		}
		after.Checked, before.Checked = "", ""
		if !statusEqual(after, before) {
			t.Errorf("the status went from %+v to %+v, want no change", before, after)
		}
	})
}

func statusEqual(a, b catalogStatus) bool {
	return string(mustJSON(a)) == string(mustJSON(b))
}

func TestTheHourlyReadFindsANewFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := catalogWorld(t)
		_, sum := w.lvfs.publish("firmware-09139-stable.xml.zst", frameworkBIOS("0.0.4.3"))
		time.Sleep(59 * time.Minute)
		synctest.Wait()
		if got := w.api.catalog(t, "lvfs").Status.File; got != "firmware-09138-stable.xml.zst" {
			t.Errorf("before the hour the status has file %s, want the first file", got)
		}
		time.Sleep(time.Minute)
		synctest.Wait()

		status := w.api.catalog(t, "lvfs").Status
		if status.File != "firmware-09139-stable.xml.zst" || status.SHA256 != sum {
			t.Errorf("after the hour the status has file %s and SHA-256 %s, want the new file", status.File, status.SHA256)
		}
	})
}

// A jcat or a file that cannot be read sets Ready to False, with the
// reason JcatUnreadable or FileUnreadable.
func TestAReadThatFailsSetsReadyFalse(t *testing.T) {
	cases := []struct {
		name  string
		serve func(l *fakeLVFS)
		want  string
	}{
		{"no jcat", func(*fakeLVFS) {}, reasonJcatUnreadable},
		{"a jcat that is not gzip", func(l *fakeLVFS) { l.serve(lvfsURL+".jcat", []byte("{}")) }, reasonJcatUnreadable},
		{"no file", func(l *fakeLVFS) {
			l.serve(lvfsURL+".jcat", jcatOf("firmware-1.xml.zst", strings.Repeat("ab", 32)))
		}, reasonFileUnreadable},
		{"a file that is not zstd", func(l *fakeLVFS) {
			l.serve(directoryOf(lvfsURL)+"firmware-1.xml.zst", []byte("plain"))
			l.serve(lvfsURL+".jcat", jcatOf("firmware-1.xml.zst", sha256Hex([]byte("plain"))))
		}, reasonFileUnreadable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.serve(w.lvfs)
				w.api.putCatalog("lvfs", lvfsURL)
				w.settle()

				if r := w.api.catalog(t, "lvfs").Status.ready(); r.Status != "False" || r.Reason != c.want || r.Message == "" {
					t.Errorf("Ready = %+v, want False, %s, with the error", r, c.want)
				}
			})
		})
	}
}

func TestAChangedCatalogReadsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := catalogWorld(t)
		mirror := "https://mirror.example/lvfs/firmware.xml.zst"
		body := zstdOf(appStreamOf(frameworkBIOS("0.0.4.2")))
		w.lvfs.serve("https://mirror.example/lvfs/firmware-1.xml.zst", body)
		w.lvfs.serve(mirror+".jcat", jcatOf("firmware-1.xml.zst", sha256Hex(body)))
		w.api.putCatalog("lvfs", mirror)
		w.settle()

		status := w.api.catalog(t, "lvfs").Status
		if status.File != "firmware-1.xml.zst" || status.ready().ObservedGeneration != 2 {
			t.Errorf("the status is %+v, want the mirror's file at generation 2", status)
		}
	})
}

func TestAJcatThatIsTooLargeIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.lvfs.serve(lvfsURL+".jcat", make([]byte, maxJcatSize+1))
		w.api.putCatalog("lvfs", lvfsURL)
		w.settle()

		r := w.api.catalog(t, "lvfs").Status.ready()
		if r.Status != "False" || r.Reason != reasonJcatUnreadable || !strings.Contains(r.Message, "larger than") {
			t.Errorf("Ready = %+v, want False because the jcat is too large", r)
		}
	})
}

func TestADeletedCatalogStartsNoScan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := catalogWorld(t)
		w.api.deleteObject(catalogsPath, "lvfs")
		w.settle()
		w.api.putFleetMachine("node-2", machineReady, firstBoot)
		w.settle()

		if got := len(w.api.scans(t)); got != 0 {
			t.Errorf("%d scan pods, want none with no FirmwareCatalog", got)
		}
	})
}
