package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	testCabinetURL = "https://cdn.example.test/downloads/dbx.cab"
	dbxID          = "362301da643102b9f38477387e2193e57abaa590"
	capsuleID      = "3fcf7d72a67e2a66a14eb28eac286cc8c3ace55c"
	dbxFile        = "dbx-" + imageSecurityGUID
	dbFile         = "db-" + imageSecurityGUID
)

var (
	testCabinet = []byte("cabinet bytes")
	// dbxBefore is a variable file as efivarfs shows it: the attributes
	// of a stored dbx, then its signature lists.
	dbxBefore = append([]byte{0x27, 0, 0, 0}, bytes.Repeat([]byte{0xaa}, 64)...)
	// authAppend is what fwupd appends to the copy for a payload of one
	// signature list: one authenticated write.
	authAppend = authWrite(signatureList(0xbb, 1))
)

// rehearsalRig is a rehearser over a machine's /sys/firmware in a
// temporary directory, with the catalog and the cabinet on the fake
// server. Its install appends authAppend to the copy's dbx, unless a
// test changes the effect.
type rehearsalRig struct {
	rehearser
	tool *tool
	web  server
}

func newRehearsalRig(t *testing.T) *rehearsalRig {
	t.Helper()
	base := newRig(t)
	host := t.TempDir()
	writeFile(t, filepath.Join(host, "efi", "efivars", dbxFile), dbxBefore)
	writeFile(t, filepath.Join(host, "efi", "efivars", dbFile), []byte{0x27, 0, 0, 0, 1, 2, 3})
	writeFile(t, filepath.Join(host, "dmi", "tables", "DMI"), []byte("smbios"))
	base.web[testCabinetURL] = testCabinet
	work := t.TempDir()
	r := &rehearsalRig{tool: base.tool, web: base.web, rehearser: rehearser{
		scanner:    base.scanner,
		cabinetURL: testCabinetURL, cabinetSHA256: sum(testCabinet), deviceID: dbxID,
		hostDir: host, copyDir: filepath.Join(work, "fw"), espDir: filepath.Join(work, "esp"),
		free: func(string) (int64, error) { return 65536, nil },
	}}
	r.tool.effects = map[string]func(){"install": func() { r.appendTo(t, dbxFile, authAppend) }}
	return r
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// appendTo plays fwupd's write to a variable in the copy, which is a
// plain file opened to append.
func (r *rehearsalRig) appendTo(t *testing.T, variable string, data []byte) {
	t.Helper()
	path := filepath.Join(r.copyDir, "efi", "efivars", variable)
	held, _ := os.ReadFile(path)
	writeFile(t, path, append(held, data...))
}

func (r *rehearsalRig) installed() bool {
	return slices.ContainsFunc(r.tool.calls, func(call []string) bool { return call[0] == "install" })
}

func TestARehearsalPassesOneAuthenticatedAppend(t *testing.T) {
	r := newRehearsalRig(t)

	got := r.rehearse(context.Background())

	want := RehearsalResult{
		Result: resultPassed, Variable: dbxFile, Bytes: int64(len(authAppend)), SHA256: sum(authAppend), Free: 65536,
		Message: "the install appends 128 bytes to " + dbxFile + ", and the variable store has 65536 bytes free",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

// secondAppend is a second payload of a cabinet, such as the option ROM
// db beside the third-party db, which fwupd writes after the first.
var secondAppend = authWrite(signatureList(0xcc, 2))

func TestARehearsalPassesSeveralAuthenticatedWrites(t *testing.T) {
	r := newRehearsalRig(t)
	r.tool.effects["install"] = func() {
		r.appendTo(t, dbxFile, authAppend)
		r.appendTo(t, dbxFile, secondAppend)
	}

	got := r.rehearse(context.Background())

	both := append(bytes.Clone(authAppend), secondAppend...)
	want := RehearsalResult{
		Result: resultPassed, Variable: dbxFile, Bytes: int64(len(both)), SHA256: sum(both), Free: 65536,
		Message: "the install appends 304 bytes to " + dbxFile + " in 2 authenticated writes, and the variable store has 65536 bytes free",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestARehearsalWritesNothingOnTheMachine(t *testing.T) {
	r := newRehearsalRig(t)

	r.rehearse(context.Background())

	if held, _ := os.ReadFile(filepath.Join(r.hostDir, "efi", "efivars", dbxFile)); !bytes.Equal(held, dbxBefore) {
		t.Errorf("the machine's dbx changed to %x", held)
	}
}

// fwupdtool install with a device ID plans the release for that device
// twice, so the rehearsal names only the cabinet.
func TestARehearsalInstallsThePinnedCabinetOnce(t *testing.T) {
	r := newRehearsalRig(t)

	r.rehearse(context.Background())

	want := []string{"install", "--assume-yes", "--no-reboot-check", filepath.Join(r.workDir, "update.cab")}
	if got := r.tool.calls[len(r.tool.calls)-1]; !slices.Equal(got, want) {
		t.Errorf("the last call is %q, want %q", got, want)
	}
}

func TestARehearsalAcceptsFwupdsReloadFailureAfterTheAppend(t *testing.T) {
	r := newRehearsalRig(t)
	r.tool.answers["install"] = answer{stderr: "failed to reload: failed to reload device: SignatureListSize invalid: 0x0\n", err: errors.New("exit status 1")}

	if got := r.rehearse(context.Background()); got.Result != resultPassed {
		t.Errorf("got %+v, want Passed", got)
	}
}

func TestARehearsalRefuses(t *testing.T) {
	cases := []struct {
		name   string
		writes func(t *testing.T, r *rehearsalRig)
		want   string
	}{
		{"another variable", func(t *testing.T, r *rehearsalRig) {
			r.appendTo(t, dbxFile, authAppend)
			r.appendTo(t, dbFile, authAppend)
		}, "the install changed " + dbFile + ", which is not " + dbxFile},
		{"a file on the ESP", func(t *testing.T, r *rehearsalRig) {
			r.appendTo(t, dbxFile, authAppend)
			writeFile(t, filepath.Join(r.espDir, "EFI", "fwupd", "fwupdx64.efi"), []byte("loader"))
		}, "the install wrote EFI/fwupd/fwupdx64.efi under the ESP path"},
		{"a new variable", func(t *testing.T, r *rehearsalRig) {
			r.appendTo(t, dbxFile, authAppend)
			r.appendTo(t, "BootNext-"+efiGlobalGUID, []byte{7, 0, 0, 0, 1, 0})
		}, "the install created BootNext-" + efiGlobalGUID},
		{"a rewrite", func(t *testing.T, r *rehearsalRig) {
			writeFile(t, filepath.Join(r.copyDir, "efi", "efivars", dbxFile), authAppend)
		}, "the install rewrote " + dbxFile + " instead of appending to it"},
		{"an append that is not authenticated", func(t *testing.T, r *rehearsalRig) {
			r.appendTo(t, dbxFile, append([]byte{0x47, 0, 0, 0}, 1, 2, 3))
		}, "the append to " + dbxFile + " has the attributes 0x47, which are not an authenticated append"},
		{"an append that is not a sequence of authenticated writes", func(t *testing.T, r *rehearsalRig) {
			r.appendTo(t, dbxFile, authAppend[:100])
		}, "the append to " + dbxFile + " is not a sequence of authenticated writes: the signature list at byte 52 of write 1 states 76 bytes, and 48 remain"},
		{"no change", func(*testing.T, *rehearsalRig) {}, "the install did not change " + dbxFile},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRehearsalRig(t)
			r.tool.effects["install"] = func() { c.writes(t, r) }

			got := r.rehearse(context.Background())

			if got.Result != resultRefused || got.Message != c.want {
				t.Errorf("got %+v, want Refused: %s", got, c.want)
			}
		})
	}
}

func TestARehearsalRefusesAnAppendTheStoreHasNoRoomFor(t *testing.T) {
	r := newRehearsalRig(t)
	r.free = func(string) (int64, error) { return 16, nil }

	got := r.rehearse(context.Background())

	if got.Result != resultRefused || got.Message != "the append to "+dbxFile+" needs 124 bytes, and the variable store has 16 free" {
		t.Errorf("got %+v, want Refused for the free space", got)
	}
}
func TestARehearsalFails(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, r *rehearsalRig)
		want      string
		installed bool
	}{
		{"another cabinet", func(t *testing.T, r *rehearsalRig) { r.web[testCabinetURL] = []byte("other bytes") },
			"the SHA-256 of the cabinet is " + sum([]byte("other bytes")) + ", and the approval pins " + sum(testCabinet), false},
		{"no cabinet", func(t *testing.T, r *rehearsalRig) { delete(r.web, testCabinetURL) },
			"downloading the cabinet: " + testCabinetURL + " answered Not Found", false},
		{"no device", func(t *testing.T, r *rehearsalRig) { r.deviceID = "0000" }, "fwupd reports no device 0000", false},
		{"a BIOS boot", func(t *testing.T, r *rehearsalRig) { os.RemoveAll(filepath.Join(r.hostDir, "efi")) },
			"copying the firmware tables: the machine has no UEFI variable store", false},
		{"an install error", func(t *testing.T, r *rehearsalRig) {
			r.tool.answers["install"] = answer{stderr: "the device is busy\n", err: errors.New("exit status 1")}
		}, "fwupdtool install: the device is busy", true},
		{"a reload failure with no append", func(t *testing.T, r *rehearsalRig) {
			r.tool.effects["install"] = func() {}
			r.tool.answers["install"] = answer{stderr: "SignatureListSize invalid: 0x0\n", err: errors.New("exit status 1")}
		}, "fwupdtool install: SignatureListSize invalid: 0x0", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRehearsalRig(t)
			c.setup(t, r)

			got := r.rehearse(context.Background())

			if got.Result != resultFailed || !strings.HasPrefix(got.Message, c.want) {
				t.Errorf("got %+v, want Failed: %s", got, c.want)
			}
			if r.installed() != c.installed {
				t.Errorf("the rehearsal ran fwupdtool install: %v, want %v", r.installed(), c.installed)
			}
		})
	}
}

// A jcat that names another file than the scan read means the
// catalog moved since the scan. The rehearsal answers CatalogMoved, as
// a scan does, so the operator reads the jcat again at once.
func TestARehearsalOfAMovedCatalogAnswersCatalogMoved(t *testing.T) {
	r := newRehearsalRig(t)
	r.web[testCatalogURL+".jcat"] = jcatFor(t, "firmware-09139-stable.xml.zst", sum(testCatalog))

	got := r.rehearse(context.Background())

	if got.Result != resultCatalogMoved || got.Message != "the jcat now names firmware-09139-stable.xml.zst" || r.installed() {
		t.Errorf("got %+v, and the install ran: %v; want CatalogMoved with the new file, and no install", got, r.installed())
	}
}

func TestARehearserReadsItsEnvironment(t *testing.T) {
	env := map[string]string{"CABINET_URL": testCabinetURL, "CABINET_SHA256": "c0ffee", "DEVICE_ID": dbxID}

	r := rehearserFromEnv(func(key string) string { return env[key] })

	if r.cabinetURL != testCabinetURL || r.cabinetSHA256 != "c0ffee" || r.deviceID != dbxID {
		t.Errorf("the rehearser is %+v, want the cabinet and the device from the environment", r)
	}
}
