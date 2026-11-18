package main

// This file reads and writes UEFI variables, which hold the boot
// entries and the boot order. loadoption.go describes the entry
// format.
//
// The kernel exposes the firmware's variable store as efivarfs, where
// each variable is a file. The first four bytes of the file are the
// variable's attribute flags (non-volatile, boot services access,
// runtime access), and the payload follows them.
//
// /sys/firmware/efi exists only when UEFI booted the kernel. liken also
// runs on BIOS machines and in direct-kernel QEMU boots, which have no
// variable store, so a missing store is not an error.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// efiGlobalVariable is EFI_GLOBAL_VARIABLE, the vendor GUID of the boot
// manager's variables, such as Boot####, BootOrder, and BootNext.
// efivarfs names each file <name>-<vendor GUID>, because a variable
// name is unique only within its vendor GUID.
const efiGlobalVariable = "8be4df61-93ca-11d2-aa0d-00e098032b8c"

// efiSysDir and efiVarsDir are variables so that a test can point them
// at a fake store, or at nothing to act as a BIOS machine.
var efiSysDir = "/sys/firmware/efi"

var efiVarsDir = efiSysDir + "/efivars"

func firmwareIsUEFI() bool {
	_, err := os.Stat(efiSysDir)
	return err == nil
}

// mountEFIVars mounts efivarfs on a UEFI machine. EBUSY means that it
// is already mounted.
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

// readEFIVar returns the payload of one global variable, without the
// attribute word.
func readEFIVar(dir, name string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, name+"-"+efiGlobalVariable))
	if err != nil {
		return nil, err
	}
	if len(raw) < 4 {
		return nil, fmt.Errorf("variable %s has %d bytes, and every variable starts with a 4-byte attribute word", name, len(raw))
	}
	return raw[4:], nil
}

// fsImmutableFlag is the kernel's FS_IMMUTABLE_FL file flag, the one
// that chattr +i sets. golang.org/x/sys/unix does not export it.
const fsImmutableFlag = 0x00000010

// efiVarAttrs is the attribute word of every variable that liken
// writes. The variable must survive power loss and be readable by the
// boot manager, or the firmware cannot boot from it.
const efiVarAttrs = 0x00000007 // NON_VOLATILE | BOOTSERVICE_ACCESS | RUNTIME_ACCESS

// writeEFIVar writes one global variable. efivarfs requires the
// attribute word and the payload in a single write.
//
// efivarfs marks a variable's file immutable unless the variable is on
// the kernel's list of variables that are safe to delete, so that
// `rm -rf /` cannot delete a variable that the firmware needs to boot.
// The boot variables are on that list, but this clears the flag of an
// existing file before it writes, so that a write never fails on the
// flag.
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

// removeEFIVar deletes one variable, and clears the immutable flag
// first as writeEFIVar does. A variable that does not exist is not an
// error.
func removeEFIVar(dir, name string) error {
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
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", name, err)
	}
	return nil
}

// listBootEntries returns every Boot#### variable that decodes, keyed
// by entry number. An entry that does not decode is not one that liken
// wrote, so it is skipped.
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

// setBootEntry writes a boot entry and returns its number. It
// overwrites the entry with the same description, or else uses the
// lowest number that has no variable. liken finds its entries by
// description because the firmware can number entries any way it likes.
func setBootEntry(dir string, option loadOption) (uint16, error) {
	entries := listBootEntries(dir)
	number := uint16(0)
	for {
		existing, taken := entries[number]
		if taken && existing.description == option.description {
			break
		}
		if !taken {
			if _, err := readEFIVar(dir, bootEntryID(number)); err != nil {
				break // an entry that does not decode still uses its number
			}
		}
		number++
	}
	return number, writeEFIVar(dir, bootEntryID(number), encodeLoadOption(option))
}

// readBootOrder returns the entry numbers in BootOrder, first choice
// first, or nil when the variable is missing or unreadable.
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

func writeBootOrder(dir string, order []uint16) error {
	payload := make([]byte, len(order)*2)
	for i, n := range order {
		binary.LittleEndian.PutUint16(payload[i*2:], n)
	}
	return writeEFIVar(dir, "BootOrder", payload)
}

// firmwareFacts returns the firmware mode, BootCurrent, BootNext, and
// BootOrder, with each entry shown with its description. When dir does
// not exist, the machine is BIOS, and biosFirmwareFacts reads the boot
// facts from GRUB's environment block.
func firmwareFacts(dir string) machine.FirmwareStatus {
	if _, err := os.Stat(dir); err != nil {
		return biosFirmwareFacts()
	}
	fw := machine.FirmwareStatus{Mode: machine.FirmwareUEFI}

	// A direct-kernel boot can set none of these variables.
	if b, err := readEFIVar(dir, "BootCurrent"); err == nil && len(b) >= 2 {
		fw.BootCurrent = describeBootEntry(dir, binary.LittleEndian.Uint16(b), "")
	}
	order := readBootOrder(dir)
	// The firmware deletes BootNext when it boots from it.
	// assertBootNext writes the proven slot to BootNext on every boot,
	// so a BootNext equal to the first BootOrder entry is that copy.
	// Any other value is a trial boot that has not happened yet.
	if b, err := readEFIVar(dir, "BootNext"); err == nil && len(b) >= 2 {
		next, note := binary.LittleEndian.Uint16(b), ""
		if len(order) > 0 && order[0] == next {
			note = "pinned at the proven slot"
		}
		fw.BootNext = describeBootEntry(dir, next, note)
	}
	for _, n := range order {
		fw.BootOrder = append(fw.BootOrder, describeBootEntry(dir, n, ""))
	}
	fw.BootOrderKept = bootOrderKept
	return fw
}

// describeBootEntry returns entry n as "Boot0001 (description, note)".
// It leaves out the description when the entry is missing or does not
// decode, and the parentheses when there is nothing to put in them.
func describeBootEntry(dir string, n uint16, note string) string {
	id, inside := bootEntryID(n), []string{}
	if payload, err := readEFIVar(dir, id); err == nil {
		if option, err := parseLoadOption(payload); err == nil && option.description != "" {
			inside = append(inside, option.description)
		}
	}
	if note != "" {
		inside = append(inside, note)
	}
	if len(inside) == 0 {
		return id
	}
	return fmt.Sprintf("%s (%s)", id, strings.Join(inside, ", "))
}

// reportFirmware prints the facts of firmwareFacts on the console.
func reportFirmware() {
	fw := firmwareFacts(efiVarsDir)
	if fw.Mode == machine.FirmwareUEFI {
		fmt.Println("liken: firmware: UEFI")
		if fw.BootCurrent == "" {
			fmt.Println("liken: firmware: BootCurrent is not set, because a direct-kernel boot does not select a boot entry")
		}
	} else {
		fmt.Println("liken: firmware: BIOS, because /sys/firmware/efi does not exist. Boot preferences are in GRUB when it is installed")
	}
	if fw.BootCurrent != "" {
		fmt.Printf("liken: firmware: booted via %s\n", fw.BootCurrent)
	}
	if fw.BootNext != "" {
		fmt.Printf("liken: firmware: BootNext is %s\n", fw.BootNext)
	}
	for _, entry := range fw.BootOrder {
		fmt.Printf("liken: firmware: boot order: %s\n", entry)
	}
	if fw.BootOrderKept != nil {
		fmt.Printf("liken: firmware: the proven slot was first in the boot order at power-on: %t\n", *fw.BootOrderKept)
	}
}
