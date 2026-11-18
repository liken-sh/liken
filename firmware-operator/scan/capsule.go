package main

// A capsule update installs new firmware for a device, such as a
// board's UEFI. fwupd stages the capsule, and the firmware applies it
// at the next boot, before any kernel loads. fwupd stages it in one of
// two modes. In NVRAM mode it writes its updater and the capsule to the
// ESP, creates a boot entry for the updater, sets BootNext to that
// entry, and sets a status variable of its own. At the next boot the
// updater hands the capsule to the firmware. In capsule-on-disk mode it
// writes the capsule to \EFI\UpdateCapsule and sets a bit in
// OsIndications, and the firmware reads the file itself. Under OVMF,
// fwupd 2.1.1 and 2.1.6 wrote exactly these. With some firmware, fwupd
// also appends the updater's entry to BootOrder.
//
// A rehearsal runs the install against a copy of the variables and a
// copy of the ESP's \EFI directory, and passes when the install wrote
// only the variables and files above. It cannot show whether the
// firmware accepts the capsule, because the firmware checks it at the
// next boot and a copy has no firmware. The ESP is the one that the
// machine reads at its next boot. `liken` links it at
// /run/liken/proven-esp, and the operator mounts it read-only at
// espSource.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"
)

const (
	capsulePlugin = "uefi_capsule"
	// fwupdVendorGUID is the vendor GUID of fwupd's own variables, which
	// record each staged capsule's path and status for the updater.
	fwupdVendorGUID = "0abba7dc-e516-4167-bbf5-4d9d1c739416"
	// fileCapsuleDelivery is the bit of OsIndications that asks the
	// firmware to read capsules from the ESP (capsule-on-disk).
	fileCapsuleDelivery = 0x4
)

var (
	bootEntryVariable = regexp.MustCompile(`^Boot([0-9A-F]{4})-` + efiGlobalGUID + `$`)
	// capsuleFiles match the files that fwupd writes on the ESP: the
	// updater and the capsule in the directory that fwupd names for
	// the operating system, and a capsule-on-disk file.
	capsuleFiles = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^EFI/[^/]+/fwupdx64\.efi$`),
		regexp.MustCompile(`(?i)^EFI/[^/]+/fw/[^/]+\.cap$`),
		regexp.MustCompile(`(?i)^EFI/UpdateCapsule/[^/]+$`),
	}
)

// Write is one variable or file that a capsule's install wrote.
type Write struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

const (
	writeKindVariable = "variable"
	writeKindFile     = "file"
)

// rehearseCapsule rehearses the install of a capsule on device. It also
// returns the path of the cabinet it downloaded, which the apply
// installs.
func (r rehearser) rehearseCapsule(ctx context.Context, device fwupdDevice) (RehearsalResult, string) {
	result := r.rehearseCapsuleIn(ctx, device)
	return result, filepath.Join(r.workDir, "update.cab")
}

func (r rehearser) rehearseCapsuleIn(ctx context.Context, device fwupdDevice) RehearsalResult {
	if info, err := os.Stat(r.espSource); r.espSource == "" || err != nil || !info.IsDir() {
		return rehearsalRefused("the machine has no ESP for its next boot, because liken has recorded no proven boot slot on it, so %s has nowhere to stage a capsule", device.Name)
	}
	if err := copyTree(filepath.Join(r.espSource, "EFI"), filepath.Join(r.espDir, "EFI")); err != nil && !os.IsNotExist(err) {
		return rehearsalFailed("copying the ESP's EFI directory: %v", err)
	}
	cabPath, end := r.fetchCabinet(ctx)
	if end != nil {
		return *end
	}

	varsDir := filepath.Join(r.copyDir, "efi", "efivars")
	before, err := readVariables(varsDir)
	if err != nil {
		return rehearsalFailed("reading the copy before the install: %v", err)
	}
	espBefore, err := readESPFiles(r.espDir)
	if err != nil {
		return rehearsalFailed("reading the ESP's copy before the install: %v", err)
	}
	stderr, installErr := r.install(ctx, cabPath)
	if installErr != nil {
		return rehearsalFailed("fwupdtool install: %s", lastLine(stderr))
	}
	if err := r.repairUpdater(varsDir, plainVariableWriter); err != nil {
		return rehearsalFailed("%v", err)
	}
	after, err := readVariables(varsDir)
	if err != nil {
		return rehearsalFailed("reading the copy after the install: %v", err)
	}
	espAfter, err := readESPFiles(r.espDir)
	if err != nil {
		return rehearsalFailed("reading the ESP's copy after the install: %v", err)
	}

	writes, refusal := judgeCapsule(before, after, espBefore, espAfter)
	if refusal != "" {
		return rehearsalRefused("%s", refusal)
	}
	espFree, err := r.free(r.espSource)
	if err != nil {
		return rehearsalFailed("reading the free space of the ESP: %v", err)
	}
	var fileBytes int64
	variables := 0
	for _, w := range writes {
		if w.Kind == writeKindFile {
			fileBytes += w.Bytes
		} else {
			variables++
		}
	}
	result := RehearsalResult{Writes: writes, Bytes: fileBytes, SHA256: writesDigest(writes), ESPFree: espFree}
	if fileBytes > espFree {
		result.Result = resultRefused
		result.Message = fmt.Sprintf("the capsule's files need %d bytes on the ESP, and it has %d free", fileBytes, espFree)
		return result
	}
	result.Result = resultPassed
	result.Message = fmt.Sprintf("the install stages a capsule with %d variables and %d files, %d bytes on the ESP, which has %d bytes free",
		variables, len(writes)-variables, fileBytes, espFree)
	return result
}

// judgeCapsule compares the variables and ESP files before and after an
// install with what a capsule install may write. It returns each write,
// sorted by kind and name. When the install did something else, it
// returns a refusal message that names the first such change.
func judgeCapsule(before, after map[string][]byte, espBefore, espAfter map[string][]byte) ([]Write, string) {
	var writes []Write
	var entries []string
	for _, name := range sortedNames(before, after) {
		was, existedBefore := before[name]
		is, existsAfter := after[name]
		switch {
		case !existsAfter:
			return nil, fmt.Sprintf("the install removed %s", name)
		case existedBefore && bytes.Equal(was, is):
			continue
		case bootEntryVariable.MatchString(name):
			// Either a new entry for the updater, or the entry from an
			// earlier update, which fwupd writes again.
			path, ok := loadOptionPath(is[4:])
			if !ok || !isUpdater(path) {
				if existedBefore {
					return nil, fmt.Sprintf("the install changed %s, which a capsule does not write", name)
				}
				return nil, fmt.Sprintf("the install created %s, which boots %s, not fwupd's updater", name, path)
			}
			if !existedBefore {
				entries = append(entries, name)
			}
		case name == "BootNext-"+efiGlobalGUID, name == "BootOrder-"+efiGlobalGUID:
			// judgeBootVariables checks these against the new entry.
		case strings.HasPrefix(name, "fwupd-") && strings.HasSuffix(name, "-"+fwupdVendorGUID):
		case name == "OsIndications-"+efiGlobalGUID:
			old, now := osIndications(was), osIndications(is)
			if now != old|fileCapsuleDelivery {
				return nil, fmt.Sprintf("the install set OsIndications to %#x, which requests more than capsule-on-disk", now)
			}
		default:
			if existedBefore {
				return nil, fmt.Sprintf("the install changed %s, which a capsule does not write", name)
			}
			return nil, fmt.Sprintf("the install created %s, which a capsule does not write", name)
		}
		writes = append(writes, Write{Kind: writeKindVariable, Name: name, Bytes: int64(len(is)), SHA256: sum(is)})
	}
	if len(entries) > 1 {
		return nil, fmt.Sprintf("the install created %d boot entries, and a capsule needs one", len(entries))
	}
	if refusal := judgeBootVariables(before, after); refusal != "" {
		return nil, refusal
	}

	for _, name := range sortedNames(espBefore, espAfter) {
		was, existedBefore := espBefore[name]
		is, existsAfter := espAfter[name]
		switch {
		case !existsAfter:
			return nil, fmt.Sprintf("the install removed %s from the ESP", name)
		case existedBefore && bytes.Equal(was, is):
			continue
		case !slices.ContainsFunc(capsuleFiles, func(re *regexp.Regexp) bool { return re.MatchString(name) }):
			return nil, fmt.Sprintf("the install wrote %s on the ESP, where a capsule writes nothing", name)
		}
		writes = append(writes, Write{Kind: writeKindFile, Name: name, Bytes: int64(len(is)), SHA256: sum(is)})
	}
	if len(writes) == 0 {
		return nil, "the install wrote nothing"
	}
	slices.SortFunc(writes, func(a, b Write) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return writes, ""
}

// judgeBootVariables returns a refusal message unless the install left
// BootNext and BootOrder as fwupd leaves them. BootNext, when changed,
// must name one entry that boots fwupd's updater. BootOrder, when
// changed, must keep its old entries in order and add only an entry for
// the updater at the end.
func judgeBootVariables(before, after map[string][]byte) string {
	updater := func(n uint16) bool {
		entry, ok := after[fmt.Sprintf("Boot%04X-%s", n, efiGlobalGUID)]
		if !ok || len(entry) < 4 {
			return false
		}
		path, ok := loadOptionPath(entry[4:])
		return ok && isUpdater(path)
	}
	next := "BootNext-" + efiGlobalGUID
	if is, ok := after[next]; ok && !bytes.Equal(before[next], is) {
		got := bootNumbers(is)
		if len(got) != 1 || !updater(got[0]) {
			return fmt.Sprintf("the install set BootNext to %s, not to the updater's entry", bootIDs(got))
		}
	}
	order := "BootOrder-" + efiGlobalGUID
	if is, ok := after[order]; ok && !bytes.Equal(before[order], is) {
		old, now := bootNumbers(before[order]), bootNumbers(is)
		if len(now) != len(old)+1 || !slices.Equal(now[:len(old)], old) || !updater(now[len(old)]) {
			return "the install changed BootOrder by more than adding the updater's entry at its end"
		}
	}
	return ""
}

// isUpdater reports whether a load option's file is fwupd's updater.
func isUpdater(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), `\fwupdx64.efi`)
}

// bootNumbers returns the boot entry numbers in BootNext or BootOrder,
// which follow the four bytes of variable attributes.
func bootNumbers(variable []byte) []uint16 {
	var numbers []uint16
	for i := 4; i+2 <= len(variable); i += 2 {
		numbers = append(numbers, binary.LittleEndian.Uint16(variable[i:]))
	}
	return numbers
}

func bootIDs(numbers []uint16) string {
	var ids []string
	for _, n := range numbers {
		ids = append(ids, fmt.Sprintf("Boot%04X", n))
	}
	return strings.Join(ids, ",")
}

// osIndications returns the 64-bit value of OsIndications, which follows
// the four bytes of variable attributes. It returns 0 for a variable
// that is absent or too short.
func osIndications(variable []byte) uint64 {
	if len(variable) < 12 {
		return 0
	}
	return binary.LittleEndian.Uint64(variable[4:])
}

// loadOptionPath returns the file that an EFI_LOAD_OPTION boots, which
// is the path in the first file path node of its device path.
func loadOptionPath(option []byte) (string, bool) {
	_, list, ok := devicePath(option)
	if !ok {
		return "", false
	}
	for len(list) >= 4 {
		nodeType, subType, length := list[0], list[1], int(binary.LittleEndian.Uint16(list[2:]))
		if length < 4 || length > len(list) {
			return "", false
		}
		if nodeType == 0x04 && subType == 0x04 {
			var units []uint16
			for j := 4; j+2 <= length; j += 2 {
				if u := binary.LittleEndian.Uint16(list[j:]); u != 0 {
					units = append(units, u)
				}
			}
			return string(utf16.Decode(units)), true
		}
		list = list[length:]
	}
	return "", false
}

// readESPFiles reads every file under an ESP's \EFI directory, keyed by
// its path from the ESP's root, such as EFI/BOOT/BOOTX64.EFI. It skips
// the rest of the ESP, because the ESP of a slot also holds a whole
// release, and fwupd writes only under \EFI.
func readESPFiles(esp string) (map[string][]byte, error) {
	files, err := readFiles(filepath.Join(esp, "EFI"))
	if err != nil {
		return nil, err
	}
	rooted := map[string][]byte{}
	for name, data := range files {
		rooted["EFI/"+name] = data
	}
	return rooted, nil
}

// readFiles reads every file under dir, keyed by its path relative to
// dir with forward slashes. It returns no files when dir does not exist.
func readFiles(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return fs.SkipAll
		}
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	return files, err
}

// writesDigest returns one SHA-256 over a capsule's writes. The operator
// records it from the rehearsal, and the apply compares it with the
// digest of what fwupd wrote.
func writesDigest(writes []Write) string {
	var b strings.Builder
	for _, w := range writes {
		fmt.Fprintf(&b, "%s %s %d %s\n", w.Kind, w.Name, w.Bytes, w.SHA256)
	}
	return sum([]byte(b.String()))
}

// stageCapsule runs fwupd's install on the machine itself: the real
// variable store, and the ESP of the next boot, which the pod mounts
// read-write. It runs fwupd instead of replaying the rehearsal's
// writes, because fwupd makes its writes in its own order. Then it
// checks fwupd's writes with judgeCapsule, as the rehearsal checked the
// copies. It returns Written only when the digest of the writes equals
// the rehearsal's digest, and only then does the reboot keep the
// BootNext that fwupd set. After any other write it returns Uncertain,
// and the reboot uses the BootNext that `liken` sets, so fwupd's
// updater does not run.
func (a applier) stageCapsule(ctx context.Context, p prepared) ApplyResult {
	vars := filepath.Join(a.hostDir, "efi", "efivars")
	before, err := readVariables(vars)
	if err != nil {
		return applyFailed("reading the variable store before the install: %v, so nothing was written", err)
	}
	espBefore, err := readESPFiles(a.espSource)
	if err != nil {
		return applyFailed("reading the ESP before the install: %v, so nothing was written", err)
	}
	if err := a.releaseStatusVariables(vars, before); err != nil {
		return applyFailed("%v, so nothing was written", err)
	}
	// The install passes no device ID, because fwupd derives the ID from
	// the path of the firmware tables, and the rehearsal's ID came from
	// copies at another path. fwupd installs the cabinet on the device
	// whose GUID the cabinet lists.
	_, stderr, installErr := a.installer(ctx, "install", "--assume-yes", "--no-reboot-check", p.cabinet)
	if installErr == nil {
		if err := a.repairUpdater(vars, a.store.replace); err != nil {
			return ApplyResult{Result: resultUncertain, Message: fmt.Sprintf("%v, so the reboot keeps liken's BootNext and the capsule does not run", err)}
		}
	}
	after, err := readVariables(vars)
	if err != nil {
		return ApplyResult{Result: resultUncertain, Message: fmt.Sprintf("reading the variable store after the install: %v", err)}
	}
	espAfter, err := readESPFiles(a.espSource)
	if err != nil {
		return ApplyResult{Result: resultUncertain, Message: fmt.Sprintf("reading the ESP after the install: %v", err)}
	}
	writes, refusal := judgeCapsule(before, after, espBefore, espAfter)
	wrote := refusal != "the install wrote nothing"
	switch {
	case installErr != nil && !wrote:
		return applyFailed("fwupdtool install: %s, and it wrote nothing", lastLine(stderr))
	case installErr != nil:
		return ApplyResult{Result: resultUncertain, Message: fmt.Sprintf("fwupdtool install: %s, after it wrote to the machine, so the reboot keeps liken's BootNext and the capsule does not run", lastLine(stderr))}
	case refusal != "":
		return ApplyResult{Result: resultUncertain, Message: refusal + ", so the reboot keeps liken's BootNext and the capsule does not run"}
	}
	if got := writesDigest(writes); got != a.appendSHA256 {
		return ApplyResult{Result: resultUncertain, Message: fmt.Sprintf("fwupd's writes differ from the rehearsal's: their digest is %s, and the rehearsal recorded %s, so the reboot keeps liken's BootNext and the capsule does not run",
			got, a.appendSHA256)}
	}
	variables := 0
	for _, w := range writes {
		if w.Kind == writeKindVariable {
			variables++
		}
	}
	next := "the firmware reads it from \\EFI\\UpdateCapsule"
	if is, ok := after["BootNext-"+efiGlobalGUID]; ok && !bytes.Equal(before["BootNext-"+efiGlobalGUID], is) {
		next = "BootNext is " + bootIDs(bootNumbers(is))
	}
	return ApplyResult{Result: resultWritten, SHA256: a.appendSHA256,
		Message: fmt.Sprintf("staged the capsule with %d variables and %d files, as rehearsed, and %s", variables, len(writes)-variables, next)}
}

// repairUpdater runs repairUpdaterEntry on the variable store at vars,
// with the partition of the ESP of the next boot. It does nothing when
// the rehearser has no partitionOf, as in a test.
func (r rehearser) repairUpdater(vars string, write variableWriter) error {
	if r.partitionOf == nil {
		return nil
	}
	esp, err := r.partitionOf(r.espSource)
	if err != nil {
		return err
	}
	return repairUpdaterEntry(vars, esp, write)
}

// releaseStatusVariables clears efivarfs's immutable flag on the fwupd
// status variables that an earlier update left. The install rewrites
// these variables, and fwupd cannot open one for writing while its flag
// is set.
func (a applier) releaseStatusVariables(vars string, existing map[string][]byte) error {
	for name := range existing {
		if !strings.HasPrefix(name, "fwupd-") || !strings.HasSuffix(name, "-"+fwupdVendorGUID) {
			continue
		}
		path := filepath.Join(vars, name)
		flags, err := a.store.flags(path)
		if err != nil {
			return fmt.Errorf("reading the flags of the variable %s: %w", name, err)
		}
		if flags&immutableFlag == 0 {
			continue
		}
		if err := a.store.setFlags(path, flags&^immutableFlag); err != nil {
			return fmt.Errorf("clearing the immutable flag of the variable %s: %w", name, err)
		}
	}
	return nil
}
