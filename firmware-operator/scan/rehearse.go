package main

// A rehearsal runs an approved install against a copy of the machine's
// firmware variables and reports what the install would write.
// fwupdtool has no dry run, because `fwupdtool install` writes the
// device. But fwupd reads the variable store, the ESRT, and the DMI
// tables through one directory, which FWUPD_SYSFSFWDIR moves. So the
// rehearsal points fwupd at an in-memory copy of those three trees, and
// the install writes to the copy and not to the machine's store. The
// pod also mounts /sys/firmware read-only, so a rehearsal that goes
// wrong still cannot write the real store.
//
// A rehearsal covers Secure Boot database updates and UEFI capsules. A
// database update is one or more authenticated appends to the
// database's own variable. The firmware verifies each append against
// the KEK or the PK and merges it into the list (authwrites.go). A
// capsule update writes a boot entry, BootNext, and files on the ESP of
// the next boot (capsule.go).

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The vendor GUIDs in the efivarfs file names of the Secure Boot
// variables. PK and KEK use the EFI global variable GUID, and db and
// dbx use the image security database GUID.
const (
	efiGlobalGUID     = "8be4df61-93ca-11d2-aa0d-00e098032b8c"
	imageSecurityGUID = "d719b2cb-3d3a-4596-a3bc-dad00e67656f"
)

// databaseVariables maps each fwupd plugin that updates a Secure Boot
// database to the efivarfs file name of the variable it writes.
var databaseVariables = map[string]string{
	"uefi_dbx": "dbx-" + imageSecurityGUID,
	"uefi_db":  "db-" + imageSecurityGUID,
	"uefi_kek": "KEK-" + efiGlobalGUID,
}

// An authenticated append sets both of these attributes. Without
// append-write, the write replaces the list. Without time-based
// authentication, the firmware does not check a signature.
const (
	attrTimeBasedAuth = 0x20
	attrAppendWrite   = 0x40
)

// reloadFailure is the error that fwupd's database install ends with
// against a copy. The copy is a plain file, so it stores the raw
// authenticated append. Firmware would have verified the append and
// merged its entries into the list, so fwupd's read of the variable
// after its write fails. The rehearsal accepts this error only when
// the variables show an append (rehearseAppend).
const reloadFailure = "SignatureListSize invalid"

// The results of a rehearsal besides Failed and CatalogMoved. Passed
// means the install made only the writes that a rehearsal allows, and
// there is room for them. Refused means the rehearsal does not cover the
// device, the install made a write that it does not allow, or there is
// no room for the writes.
const (
	resultPassed  = "Passed"
	resultRefused = "Refused"
)

// RehearsalResult is the line of JSON that a rehearsal prints. Bytes
// and SHA256 describe the append as the apply writes it: for each
// authenticated write in order, its four attribute bytes and then its
// payload.
type RehearsalResult struct {
	Result   string `json:"result"`
	Message  string `json:"message"`
	Variable string `json:"variable,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Free     int64  `json:"free,omitempty"`
	// Writes lists a capsule's variable and file writes, and ESPFree is
	// the free space of the ESP that they go to. For a capsule, Bytes is
	// the size of the files, and SHA256 is the digest of the writes
	// (writesDigest).
	Writes  []Write `json:"writes,omitempty"`
	ESPFree int64   `json:"espFree,omitempty"`
}

func rehearsalFailed(format string, args ...any) RehearsalResult {
	return RehearsalResult{Result: resultFailed, Message: fmt.Sprintf(format, args...)}
}

func rehearsalRefused(format string, args ...any) RehearsalResult {
	return RehearsalResult{Result: resultRefused, Message: fmt.Sprintf(format, args...)}
}

// rehearser runs a rehearsal in the rehearsal pod. Its scanner loads
// the catalog, and its fwupdtool runs against the copy.
type rehearser struct {
	scanner
	cabinetURL    string // CABINET_URL, the approved cabinet
	cabinetSHA256 string // CABINET_SHA256, the approval's pin
	deviceID      string // DEVICE_ID, fwupd's ID of the device
	hostDir       string // the machine's /sys/firmware, mounted read-only
	copyDir       string // the copy that FWUPD_SYSFSFWDIR names
	espDir        string // the directory that FWUPD_UEFI_ESP_PATH names
	espSource     string // the ESP of the next boot, mounted read-only, when the machine has one
	// partitionOf returns the partition mounted at a path. The repair
	// of the updater's boot entry needs it (updaterentry.go).
	partitionOf func(mount string) (partition, error)
	free        func(path string) (int64, error)
}

func rehearserFromEnv(getenv func(string) string) rehearser {
	return rehearser{
		cabinetURL:    getenv("CABINET_URL"),
		cabinetSHA256: getenv("CABINET_SHA256"),
		deviceID:      getenv("DEVICE_ID"),
	}
}

// rehearse returns a RehearsalResult for every outcome, a failure
// included, because the operator reads the result from the pod's log.
func (r rehearser) rehearse(ctx context.Context) RehearsalResult {
	result, _ := r.rehearseAppend(ctx)
	return result
}

// rehearsedAppend holds the bytes of a passed rehearsal: the variable
// as the rehearsal copied it, and the authenticated writes that the
// install appended to it, in order.
//
// For a capsule, cabinet is the path of the cabinet that the rehearsal
// saved, which the apply installs.
type rehearsedAppend struct {
	before  []byte
	writes  [][]byte
	cabinet string
}

// rehearseAppend runs the steps of a rehearsal in order and stops at
// the first that fails. It returns the bytes of a passed rehearsal too,
// which the apply writes (apply.go). The result keeps only the append's
// length and SHA-256, so the bytes leave the pod only when the apply
// writes them.
func (r rehearser) rehearseAppend(ctx context.Context) (RehearsalResult, rehearsedAppend) {
	if err := copyFirmware(r.hostDir, r.copyDir); err != nil {
		return rehearsalFailed("copying the firmware tables: %v", err), rehearsedAppend{}
	}
	// When the jcat lists a different file than the scan read, the
	// rehearsal ends as CatalogMoved, the same as a scan does. The
	// operator then reads the jcat again, and the machine scans the new
	// file.
	if end := r.loadCatalog(ctx); end != nil {
		return RehearsalResult{Result: end.Result, Message: end.Message}, rehearsedAppend{}
	}
	devices, err := query(ctx, r.fwupdtool, "get-devices")
	if err != nil {
		return rehearsalFailed("%v", err), rehearsedAppend{}
	}
	i := slices.IndexFunc(devices, func(d fwupdDevice) bool { return d.DeviceID == r.deviceID })
	if i < 0 {
		return rehearsalFailed("fwupd reports no device %s", r.deviceID), rehearsedAppend{}
	}
	if devices[i].Plugin == capsulePlugin {
		result, cabinet := r.rehearseCapsule(ctx, devices[i])
		return result, rehearsedAppend{cabinet: cabinet}
	}
	variable, database := databaseVariables[devices[i].Plugin]
	if !database {
		return rehearsalRefused("fwupd's %s plugin updates %s, and a rehearsal covers only the Secure Boot databases and UEFI capsules", devices[i].Plugin, devices[i].Name), rehearsedAppend{}
	}
	cabPath, end := r.fetchCabinet(ctx)
	if end != nil {
		return *end, rehearsedAppend{}
	}

	varsDir := filepath.Join(r.copyDir, "efi", "efivars")
	before, err := readVariables(varsDir)
	if err != nil {
		return rehearsalFailed("reading the copy before the install: %v", err), rehearsedAppend{}
	}
	stderr, installErr := r.install(ctx, cabPath)
	after, err := readVariables(varsDir)
	if err != nil {
		return rehearsalFailed("reading the copy after the install: %v", err), rehearsedAppend{}
	}
	written, err := filesUnder(r.espDir)
	if err != nil {
		return rehearsalFailed("reading the ESP path: %v", err), rehearsedAppend{}
	}

	writes, refusal := judge(variable, before, after, written)
	if installErr != nil && (writes == nil || !strings.Contains(lastLine(stderr), reloadFailure)) {
		return rehearsalFailed("fwupdtool install: %s", lastLine(stderr)), rehearsedAppend{}
	}
	if refusal != "" {
		return rehearsalRefused("%s", refusal), rehearsedAppend{}
	}
	free, err := r.free(filepath.Join(r.hostDir, "efi", "efivars"))
	if err != nil {
		return rehearsalFailed("reading the free space of the variable store: %v", err), rehearsedAppend{}
	}
	appended := bytes.Join(writes, nil)
	result := RehearsalResult{Variable: variable, Bytes: int64(len(appended)), SHA256: sum(appended), Free: free}
	if payload := int64(len(appended) - 4*len(writes)); payload > free {
		result.Result = resultRefused
		result.Message = fmt.Sprintf("the append to %s needs %d bytes, and the variable store has %d free", variable, payload, free)
		return result, rehearsedAppend{}
	}
	result.Result = resultPassed
	result.Message = fmt.Sprintf("the install appends %d bytes to %s, and the variable store has %d bytes free", len(appended), variable, free)
	if len(writes) > 1 {
		result.Message = fmt.Sprintf("the install appends %d bytes to %s in %d authenticated writes, and the variable store has %d bytes free",
			len(appended), variable, len(writes), free)
	}
	return result, rehearsedAppend{before: before[variable], writes: writes}
}

// install runs `fwupdtool install` on the cabinet with no device ID.
// With a device ID, fwupdtool plans the release for the device twice:
// once as a member of the device's composite group, which contains the
// device, and once as the device itself. fwupd then installs a database
// update twice, and refuses the second install of a capsule because
// the first install hides the device until the next boot. Without a
// device ID, fwupd plans each device that the cabinet matches once. The
// rehearsal then refuses any write to something other than the approved
// device's variable or capsule.
func (r rehearser) install(ctx context.Context, cabPath string) ([]byte, error) {
	_, stderr, err := r.fwupdtool(ctx, "install", "--assume-yes", "--no-reboot-check", cabPath)
	return stderr, err
}

// fetchCabinet downloads the approved cabinet, checks its SHA-256
// against the approval's pin, and saves it for the install. It returns
// a non-nil RehearsalResult when the rehearsal must end.
func (r rehearser) fetchCabinet(ctx context.Context) (string, *RehearsalResult) {
	end := func(result RehearsalResult) (string, *RehearsalResult) { return "", &result }
	cab, err := get(ctx, r.client, r.cabinetURL)
	if err != nil {
		return end(rehearsalFailed("downloading the cabinet: %v", err))
	}
	if got := sum(cab); got != r.cabinetSHA256 {
		return end(rehearsalFailed("the SHA-256 of the cabinet is %s, and the approval pins %s", got, r.cabinetSHA256))
	}
	cabPath := filepath.Join(r.workDir, "update.cab")
	if err := os.WriteFile(cabPath, cab, 0o644); err != nil {
		return end(rehearsalFailed("saving the cabinet: %v", err))
	}
	return cabPath, nil
}

// judge compares the copy's variables before and after the install with
// what a Secure Boot database update may write: authenticated appends
// to its own variable, and nothing else. It returns the appended
// writes, or a refusal message that says what else the install wrote.
func judge(variable string, before, after map[string][]byte, written []string) ([][]byte, string) {
	if len(written) > 0 {
		return nil, fmt.Sprintf("the install wrote %s under the ESP path", strings.Join(written, ", "))
	}
	for _, name := range sortedNames(before, after) {
		was, existed := before[name]
		is, remains := after[name]
		switch {
		case !remains:
			return nil, fmt.Sprintf("the install removed %s", name)
		case !existed:
			return nil, fmt.Sprintf("the install created %s", name)
		case name != variable && !bytes.Equal(was, is):
			return nil, fmt.Sprintf("the install changed %s, which is not %s", name, variable)
		}
	}
	was, is := before[variable], after[variable]
	if len(is) <= len(was) || !bytes.HasPrefix(is, was) {
		if bytes.Equal(was, is) {
			return nil, fmt.Sprintf("the install did not change %s", variable)
		}
		return nil, fmt.Sprintf("the install rewrote %s instead of appending to it", variable)
	}
	appended := is[len(was):]
	if len(appended) <= 4 {
		return nil, fmt.Sprintf("the install appended %d bytes to %s, which is too few to hold a payload", len(appended), variable)
	}
	attributes := binary.LittleEndian.Uint32(appended)
	if attributes&attrAppendWrite == 0 || attributes&attrTimeBasedAuth == 0 {
		return nil, fmt.Sprintf("the append to %s has the attributes 0x%x, which are not an authenticated append", variable, attributes)
	}
	writes, err := splitWrites(appended)
	if err != nil {
		return nil, fmt.Sprintf("the append to %s is not a sequence of authenticated writes: %v", variable, err)
	}
	return writes, ""
}

func sortedNames(sets ...map[string][]byte) []string {
	var names []string
	for _, set := range sets {
		for name := range set {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names
}

// copyFirmware copies the three trees that fwupd reads through
// FWUPD_SYSFSFWDIR. It fails on a machine that booted in BIOS mode,
// because that machine has no variable store to rehearse against. The
// ESRT and the DMI tables are optional: a virtual machine can lack the
// ESRT, and fwupd reads its DMI facts from another source when the
// tables are missing.
func copyFirmware(host, copy string) error {
	if _, err := os.Stat(filepath.Join(host, "efi", "efivars")); err != nil {
		return fmt.Errorf("the machine has no UEFI variable store: %w", err)
	}
	for _, tree := range []string{"efi/efivars", "efi/esrt", "dmi/tables"} {
		if err := copyTree(filepath.Join(host, tree), filepath.Join(copy, tree)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	// fwupd's capsule install reads the files directly under efi/, such
	// as fw_platform_size, which says whether the firmware runs 64-bit
	// code.
	entries, err := os.ReadDir(filepath.Join(host, "efi"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(host, "efi", e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(copy, "efi", e.Name()), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// copyTree copies the regular files and directories under src to dst.
// It reads each file to its end, because sysfs reports a size that is
// not the file's size, and efivarfs refuses a seek. It skips symbolic
// links, which in sysfs point at other kernel objects.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o644)
		}
		return nil
	})
}

func readVariables(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	variables := map[string][]byte{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		variables[e.Name()] = data
	}
	return variables, nil
}

// filesUnder returns the paths of the files under dir, relative to dir.
// A directory that does not exist has none.
func filesUnder(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.SkipAll
		}
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		files = append(files, rel)
		return nil
	})
	return files, err
}
