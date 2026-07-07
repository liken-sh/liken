package main

// Speaking to the firmware.
//
// A UEFI machine's firmware keeps a small store of variables in
// non-volatile memory — the modern descendant of "BIOS settings" —
// and the boot menu lives there (loadoption.go describes the
// records). The kernel exposes the store as efivarfs, a tiny
// filesystem where every variable is a file: read the file, read the
// variable. Each file's first four bytes are the variable's attribute
// flags (non-volatile, visible at boot-time, visible at runtime);
// the payload follows.
//
// Whether any of this exists is the firmware's call, not ours:
// /sys/firmware/efi appears only when the kernel was actually booted
// by UEFI. Its absence is a fact worth reporting, not an error — a
// direct-kernel QEMU boot and an old BIOS server are both real
// machines liken runs on. Everything here degrades to "the firmware
// has nothing to say."

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
	efiSysDir  = "/sys/firmware/efi"
	efiVarsDir = efiSysDir + "/efivars"

	// Every variable's filename carries its owner's GUID, because
	// variable names are only unique per vendor. The boot manager's
	// variables all belong to the specification's own GUID, fixed
	// forever as EFI_GLOBAL_VARIABLE.
	efiGlobalVariable = "8be4df61-93ca-11d2-aa0d-00e098032b8c"
)

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
// (FS_IMMUTABLE_FL), part of the fixed ioctl ABI chattr speaks;
// x/sys doesn't export it, so it's spelled out here the way the
// ext4 superblock offsets are.
const fsImmutableFlag = 0x00000010

// efiVarAttrs is the attribute word every liken-written variable
// carries: stored in NVRAM (survives power loss), visible to the
// firmware's boot services, visible to the running OS. Boot entries
// need all three — an entry the boot manager can't see boots nothing.
const efiVarAttrs = 0x00000007 // NON_VOLATILE | BOOTSERVICE_ACCESS | RUNTIME_ACCESS

// writeEFIVar writes one global variable: the attribute word and the
// payload in a single write, which is how efivarfs insists variables
// change (a partial variable is worse than none, so the filesystem
// refuses piecemeal writes).
//
// The wrinkle is the immutable flag: the kernel marks every variable
// file immutable so a stray `rm -rf /` can't brick the motherboard —
// a real failure mode on early UEFI machines, and the reason this
// helper exists instead of a plain WriteFile. Clearing it is two
// ioctls on the existing file; a variable that doesn't exist yet has
// no flag to clear.
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

// deleteEFIVar removes a variable — which, for firmware, means
// writing it with no payload; efivarfs translates an unlink into
// exactly that, after the same immutable dance.
func deleteEFIVar(dir, name string) error {
	path := filepath.Join(dir, name+"-"+efiGlobalVariable)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil {
		if flags, ferr := unix.IoctlGetInt(int(f.Fd()), unix.FS_IOC_GETFLAGS); ferr == nil && flags&fsImmutableFlag != 0 {
			_ = unix.IoctlSetPointerInt(int(f.Fd()), unix.FS_IOC_SETFLAGS, flags&^fsImmutableFlag)
		}
		f.Close()
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("deleting %s: %w", name, err)
	}
	return nil
}

// listBootEntries reads every Boot#### variable, decoded, keyed by
// entry number. Entries that won't decode are skipped — they're the
// firmware's business, not ours, and liken finds its own entries by
// description, never by assuming a number.
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
// carries its description, or the lowest free number — recognition
// by name, exactly like partitions: the number is a handle the
// firmware owns, the description is the identity liken owns.
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

// firmwareFacts reads the machine's boot story from its firmware:
// which mode it booted in, which entry the firmware used, and the
// standing preference order — each entry decoded to its name, so a
// fleet listing reads "liken slot A", not a hex dump. Console parity
// as usual: reportFirmware prints these same facts at boot.
//
// The mode is decided by the variable store's presence: efivarfs
// exists exactly when UEFI booted this kernel. "BIOS" is shorthand
// for everything else — a legacy server, QEMU's direct-kernel boot —
// any world with no firmware variables to consult.
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
	if b, err := readEFIVar(dir, "BootOrder"); err == nil {
		for i := 0; i+2 <= len(b); i += 2 {
			fw.BootOrder = append(fw.BootOrder, describeBootEntry(dir, binary.LittleEndian.Uint16(b[i:i+2])))
		}
	}
	return fw
}

// describeBootEntry renders one entry the way a person wants to read
// it: the firmware's own name for the variable, plus the entry's
// description when it can be decoded. An entry that's missing or
// mangled still gets its ID — an honest listing beats a hidden one.
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

// reportFirmware narrates the firmware's story on the console: the
// same facts firmwareFacts publishes, in the world report's voice.
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
