package main

// Reading and writing the firmware's variables.
//
// A UEFI machine's firmware keeps a small store of variables in
// non-volatile memory (the modern descendant of "BIOS settings"),
// and the boot menu lives there (loadoption.go describes the
// records). The kernel exposes the store as efivarfs, a tiny
// filesystem where every variable is a file, so reading a variable
// is just reading its file. Each file's first four bytes are the
// variable's attribute flags (non-volatile, visible at boot-time,
// visible at runtime); the payload follows.
//
// None of this is guaranteed to exist: /sys/firmware/efi appears only
// when the kernel was actually booted by UEFI. Its absence is a fact
// worth reporting, not an error; a direct-kernel QEMU boot and an old
// BIOS server are both real machines liken runs on. Everything here
// handles a machine with no variable store by reporting nothing.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
)

const (
	efiSysDir = "/sys/firmware/efi"

	// Every variable's filename carries its owner's GUID, because
	// variable names are only unique per vendor. The boot manager's
	// variables all belong to the specification's own GUID, fixed
	// forever as EFI_GLOBAL_VARIABLE.
	efiGlobalVariable = "8be4df61-93ca-11d2-aa0d-00e098032b8c"
)

// efiVarsDir is a variable rather than a constant so tests can stand
// up a directory of fake variables and exercise everything above the
// mount itself.
var efiVarsDir = efiSysDir + "/efivars"

// firmwareIsUEFI reports whether this kernel was booted by UEFI
// firmware: the kernel creates /sys/firmware/efi only when the EFI
// runtime services came up with it.
func firmwareIsUEFI() bool {
	_, err := os.Stat(efiSysDir)
	return err == nil
}

// mountEFIVars mounts the firmware's variable store, when there is
// one. Quietly a no-op on non-UEFI machines; EBUSY means something
// already mounted it, which is just as good.
func mountEFIVars() {
	if !firmwareIsUEFI() {
		return
	}
	err := unix.Mount("efivarfs", efiVarsDir, "efivarfs",
		unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "")
	switch {
	case err == nil:
		fmt.Printf("liken: mounted efivarfs on %s\n", efiVarsDir)
	case errors.Is(err, unix.EBUSY):
	default:
		fmt.Fprintf(os.Stderr, "liken: mounting efivarfs: %v\n", err)
	}
}

// readEFIVar reads one global variable's payload, with the efivarfs
// attribute word stripped: callers get the variable's value, the way
// the specification describes it.
func readEFIVar(dir, name string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, name+"-"+efiGlobalVariable))
	if err != nil {
		return nil, err
	}
	if len(raw) < 4 {
		return nil, fmt.Errorf("variable %s is %d bytes; even an empty one carries 4 of attributes", name, len(raw))
	}
	return raw[4:], nil
}

// fsImmutableFlag is the kernel's per-file immutable bit
// (FS_IMMUTABLE_FL), part of the fixed ioctl ABI that chattr uses;
// x/sys doesn't export it, so it's spelled out here the way the
// ext4 superblock offsets are.
const fsImmutableFlag = 0x00000010

// efiVarAttrs is the attribute word every liken-written variable
// carries: stored in NVRAM (survives power loss), visible to the
// firmware's boot services, visible to the running OS. Boot entries
// need all three; an entry the boot manager can't see can never be
// chosen.
const efiVarAttrs = 0x00000007 // NON_VOLATILE | BOOTSERVICE_ACCESS | RUNTIME_ACCESS

// writeEFIVar writes one global variable: the attribute word and the
// payload in a single write, which is the only way efivarfs lets a
// variable change (a partial variable is worse than none, so the
// filesystem refuses piecemeal writes).
//
// The complication is the immutable flag. The kernel marks every
// variable file immutable so that a stray `rm -rf /` can't brick the
// motherboard, a real failure mode on early UEFI machines, and that
// flag is why this helper exists instead of a plain WriteFile.
// Clearing it takes two ioctls on the existing file; a variable that
// doesn't exist yet has no flag to clear.
func writeEFIVar(dir, name string, payload []byte) error {
	path := filepath.Join(dir, name+"-"+efiGlobalVariable)
	if f, err := os.Open(path); err == nil {
		flags, err := unix.IoctlGetInt(int(f.Fd()), unix.FS_IOC_GETFLAGS)
		if err == nil && flags&fsImmutableFlag != 0 {
			err = unix.IoctlSetPointerInt(int(f.Fd()), unix.FS_IOC_SETFLAGS, flags&^fsImmutableFlag)
			if err != nil {
				f.Close()
				return fmt.Errorf("clearing the immutable flag on %s: %w", name, err)
			}
		}
		f.Close()
	}
	b := make([]byte, 4, 4+len(payload))
	binary.LittleEndian.PutUint32(b, efiVarAttrs)
	b = append(b, payload...)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	return nil
}

// listBootEntries reads every Boot#### variable, decoded, keyed by
// entry number. Entries that won't decode are skipped: they belong
// to the firmware, and liken finds its own entries by description,
// never by assuming a number.
func listBootEntries(dir string) map[uint16]loadOption {
	entries := map[uint16]loadOption{}
	files, err := os.ReadDir(dir)
	if err != nil {
		return entries
	}
	for _, f := range files {
		var n uint16
		if _, err := fmt.Sscanf(f.Name(), "Boot%04X-"+efiGlobalVariable, &n); err != nil {
			continue
		}
		payload, err := readEFIVar(dir, bootEntryID(n))
		if err != nil {
			continue
		}
		option, err := parseLoadOption(payload)
		if err != nil {
			continue
		}
		entries[n] = option
	}
	return entries
}

// setBootEntry writes a boot entry under the number that already
// carries its description, or under the lowest free number. This is
// recognition by name, exactly like partitions: the number is a
// handle the firmware owns, and the description is the identity
// liken owns.
func setBootEntry(dir string, option loadOption) (uint16, error) {
	entries := listBootEntries(dir)
	number := uint16(0)
	for {
		existing, taken := entries[number]
		if taken && existing.description == option.description {
			break // ours already; overwrite in place
		}
		if !taken {
			if _, err := readEFIVar(dir, bootEntryID(number)); err != nil {
				break // genuinely free, not just undecodable
			}
		}
		number++
	}
	return number, writeEFIVar(dir, bootEntryID(number), encodeLoadOption(option))
}

// readBootOrder decodes the firmware's standing preference list.
// BootOrder is a packed array of 16-bit little-endian entry numbers,
// first preference first; nil when the variable is missing or
// unreadable, which reads the same as an empty order everywhere it
// matters.
func readBootOrder(dir string) []uint16 {
	b, err := readEFIVar(dir, "BootOrder")
	if err != nil {
		return nil
	}
	var order []uint16
	for i := 0; i+2 <= len(b); i += 2 {
		order = append(order, binary.LittleEndian.Uint16(b[i:i+2]))
	}
	return order
}

// writeBootOrder packs a preference list back into the firmware's
// format and writes it.
func writeBootOrder(dir string, order []uint16) error {
	payload := make([]byte, len(order)*2)
	for i, n := range order {
		binary.LittleEndian.PutUint16(payload[i*2:], n)
	}
	return writeEFIVar(dir, "BootOrder", payload)
}

// firmwareFacts reads the machine's boot facts from its firmware:
// which mode it booted in, which entry the firmware used, and the
// standing preference order. Each entry is decoded to its name, so a
// fleet listing reads "liken slot A", not a hex dump. Console parity
// holds here as everywhere: reportFirmware prints these same facts
// at boot.
//
// The mode is decided by the variable store's presence: efivarfs
// exists exactly when UEFI booted this kernel. "BIOS" is shorthand
// for everything else, whether a legacy server or QEMU's
// direct-kernel boot: any machine with no firmware variables to
// consult.
func firmwareFacts(dir string) machine.FirmwareStatus {
	if _, err := os.Stat(dir); err != nil {
		return machine.FirmwareStatus{Mode: machine.FirmwareBIOS}
	}
	fw := machine.FirmwareStatus{Mode: machine.FirmwareUEFI}

	// BootCurrent and BootNext are single entry numbers; BootOrder is
	// a list of them. All are 16-bit little-endian, all optional — a
	// firmware that direct-booted a kernel may have set none of them.
	if b, err := readEFIVar(dir, "BootCurrent"); err == nil && len(b) >= 2 {
		fw.BootCurrent = describeBootEntry(dir, binary.LittleEndian.Uint16(b))
	}
	if b, err := readEFIVar(dir, "BootNext"); err == nil && len(b) >= 2 {
		fw.BootNext = describeBootEntry(dir, binary.LittleEndian.Uint16(b))
	}
	for _, n := range readBootOrder(dir) {
		fw.BootOrder = append(fw.BootOrder, describeBootEntry(dir, n))
	}
	return fw
}

// describeBootEntry renders one entry the way a person wants to read
// it: the firmware's own name for the variable, plus the entry's
// description when it can be decoded. An entry that's missing or
// mangled is still listed by its ID, so nothing in the order is
// hidden.
func describeBootEntry(dir string, n uint16) string {
	id := bootEntryID(n)
	payload, err := readEFIVar(dir, id)
	if err != nil {
		return id
	}
	option, err := parseLoadOption(payload)
	if err != nil || option.description == "" {
		return id
	}
	return fmt.Sprintf("%s (%s)", id, option.description)
}

// reportFirmware prints the firmware's facts on the console: the
// same facts firmwareFacts publishes, formatted for the world
// report.
func reportFirmware() {
	fw := firmwareFacts(efiVarsDir)
	if fw.Mode != machine.FirmwareUEFI {
		fmt.Println("liken: firmware: BIOS (no /sys/firmware/efi; no boot variables to consult)")
		return
	}
	fmt.Println("liken: firmware: UEFI")
	if fw.BootCurrent != "" {
		fmt.Printf("liken: firmware: booted via %s\n", fw.BootCurrent)
	} else {
		fmt.Println("liken: firmware: BootCurrent not set (a direct-kernel boot never picks an entry)")
	}
	if fw.BootNext != "" {
		fmt.Printf("liken: firmware: BootNext is armed: %s\n", fw.BootNext)
	}
	for _, entry := range fw.BootOrder {
		fmt.Printf("liken: firmware: boot order: %s\n", entry)
	}
}
