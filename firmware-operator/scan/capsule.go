package main

// A capsule updates a device's own firmware, such as a board's UEFI,
// through the firmware itself: fwupd stages the capsule, and the
// firmware applies it at the next boot, before any kernel loads. fwupd
// stages it in one of two ways. In NVRAM mode it writes its updater and
// the capsule to the ESP, a boot entry for the updater, BootNext naming
// that entry, and a status variable of its own, and the updater hands
// the capsule to the firmware at the next boot. In capsule-on-disk mode
// it writes the capsule to \EFI\UpdateCapsule and sets a bit in
// OsIndications, and the firmware reads the file itself. fwupd 2.1.1
// wrote exactly these under OVMF (liken's milestone 33, the firmware
// under QEMU), and with some firmware it adds the updater's entry to
// the end of BootOrder.
//
// A rehearsal runs the install against a copy of the variables and a
// copy of the ESP's \EFI directory, and passes when the install wrote
// these and nothing else. It cannot show whether the firmware accepts
// the capsule: that happens before any kernel loads, and a copy has no
// firmware. The ESP is the one that the machine's next boot reads,
// which liken links at /run/liken/proven-esp, and the operator mounts
// it read-only at espSource.

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
	// firmware for capsule-on-disk.
	fileCapsuleDelivery = 0x4
)

var (
	bootEntryVariable = regexp.MustCompile(`^Boot([0-9A-F]{4})-` + efiGlobalGUID + `$`)
	// capsuleFiles are the files a capsule writes on the ESP: the
	// updater and the capsule under the directory fwupd names for the
	// operating system, and a capsule-on-disk.
	capsuleFiles = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^EFI/[^/]+/fwupdx64\.efi$`),
		regexp.MustCompile(`(?i)^EFI/[^/]+/fw/[^/]+\.cap$`),
		regexp.MustCompile(`(?i)^EFI/UpdateCapsule/[^/]+$`),
	}
)

// Write is one variable or file that a capsule's install wrote, as the
// rehearsal records it.
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

// rehearseCapsule rehearses the install of a capsule on device.
func (r rehearser) rehearseCapsule(ctx context.Context, device fwupdDevice) RehearsalResult {
	if info, err := os.Stat(r.espSource); r.espSource == "" || err != nil || !info.IsDir() {
		return rehearsalRefused("the machine has no ESP for its next boot, because its store names no proven slot, so %s has nowhere to stage a capsule", device.Name)
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
	espBefore, err := readFiles(r.espDir)
	if err != nil {
		return rehearsalFailed("reading the ESP's copy before the install: %v", err)
	}
	_, stderr, installErr := r.fwupdtool(ctx, "install", "--assume-yes", "--no-reboot-check", cabPath, r.deviceID)
	if installErr != nil {
		return rehearsalFailed("fwupdtool install: %s", lastLine(stderr))
	}
	after, err := readVariables(varsDir)
	if err != nil {
		return rehearsalFailed("reading the copy after the install: %v", err)
	}
	espAfter, err := readFiles(r.espDir)
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

// judgeCapsule checks the difference that an install made in the copies
// against what a capsule may write. It answers each write, sorted by
// kind and name, or a refusal that names the first write it does not
// allow.
func judgeCapsule(before, after map[string][]byte, espBefore, espAfter map[string][]byte) ([]Write, string) {
	var writes []Write
	var entries []string
	for _, name := range sortedNames(before, after) {
		was, held := before[name]
		is, kept := after[name]
		switch {
		case !kept:
			return nil, fmt.Sprintf("the install removed %s", name)
		case held && bytes.Equal(was, is):
			continue
		case bootEntryVariable.MatchString(name) && !held:
			path, ok := loadOptionPath(is[4:])
			if !ok || !strings.HasSuffix(strings.ToLower(path), `\fwupdx64.efi`) {
				return nil, fmt.Sprintf("the install created %s, which boots %s, not fwupd's updater", name, path)
			}
			entries = append(entries, name)
		case name == "BootNext-"+efiGlobalGUID, name == "BootOrder-"+efiGlobalGUID:
			// Checked below, against the entry the install created.
		case strings.HasPrefix(name, "fwupd-") && strings.HasSuffix(name, "-"+fwupdVendorGUID):
		case name == "OsIndications-"+efiGlobalGUID:
			old, now := osIndications(was), osIndications(is)
			if now != old|fileCapsuleDelivery {
				return nil, fmt.Sprintf("the install set OsIndications to %#x, which asks for more than capsule-on-disk", now)
			}
		default:
			if held {
				return nil, fmt.Sprintf("the install changed %s, which a capsule does not write", name)
			}
			return nil, fmt.Sprintf("the install created %s, which a capsule does not write", name)
		}
		writes = append(writes, Write{Kind: writeKindVariable, Name: name, Bytes: int64(len(is)), SHA256: sum(is)})
	}
	if len(entries) > 1 {
		return nil, fmt.Sprintf("the install created %d boot entries, and a capsule needs one", len(entries))
	}
	if refusal := judgeBootVariables(before, after, entries); refusal != "" {
		return nil, refusal
	}

	for _, name := range sortedNames(espBefore, espAfter) {
		was, held := espBefore[name]
		is, kept := espAfter[name]
		switch {
		case !kept:
			return nil, fmt.Sprintf("the install removed %s from the ESP", name)
		case held && bytes.Equal(was, is):
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

// judgeBootVariables checks BootNext and BootOrder against the boot
// entry the install created: BootNext names it, and BootOrder keeps its
// old order and gains, at most, that entry at its end.
func judgeBootVariables(before, after map[string][]byte, entries []string) string {
	created := -1
	if len(entries) == 1 {
		n := bootEntryVariable.FindStringSubmatch(entries[0])[1]
		fmt.Sscanf(n, "%X", &created)
	}
	next := "BootNext-" + efiGlobalGUID
	if is, ok := after[next]; ok && !bytes.Equal(before[next], is) {
		got := bootNumbers(is)
		if len(got) != 1 || int(got[0]) != created {
			return fmt.Sprintf("the install set BootNext to %s, not to the updater's entry", bootIDs(got))
		}
	}
	order := "BootOrder-" + efiGlobalGUID
	if is, ok := after[order]; ok && !bytes.Equal(before[order], is) {
		old, now := bootNumbers(before[order]), bootNumbers(is)
		if created < 0 || len(now) != len(old)+1 || !slices.Equal(now[:len(old)], old) || int(now[len(old)]) != created {
			return "the install changed BootOrder by more than adding the updater's entry at its end"
		}
	}
	return ""
}

// bootNumbers reads the entry numbers of BootNext or BootOrder, after
// the four bytes of attributes.
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

// osIndications reads the variable's bits, after the four bytes of
// attributes. An absent variable has none.
func osIndications(variable []byte) uint64 {
	if len(variable) < 12 {
		return 0
	}
	return binary.LittleEndian.Uint64(variable[4:])
}

// loadOptionPath reads the file that an EFI_LOAD_OPTION boots: the path
// of the first file path node of its device path.
func loadOptionPath(option []byte) (string, bool) {
	if len(option) < 6 {
		return "", false
	}
	listLength := int(binary.LittleEndian.Uint16(option[4:]))
	i := 6
	for ; i+1 < len(option); i += 2 {
		if option[i] == 0 && option[i+1] == 0 {
			i += 2
			break
		}
	}
	if i+listLength > len(option) {
		return "", false
	}
	list := option[i : i+listLength]
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

// readFiles reads every file under dir, keyed by its path relative to
// dir with forward slashes. A directory that does not exist holds none.
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

// writesDigest is one SHA-256 over a capsule's writes, which the
// operator records, and an apply compares with what fwupd writes.
func writesDigest(writes []Write) string {
	var b strings.Builder
	for _, w := range writes {
		fmt.Fprintf(&b, "%s %s %d %s\n", w.Kind, w.Name, w.Bytes, w.SHA256)
	}
	return sum([]byte(b.String()))
}
