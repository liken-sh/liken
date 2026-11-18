package main

// This file reads and writes the GRUB environment block, which stores
// a BIOS machine's boot variables.
//
// UEFI firmware keeps BootNext (boot this entry once) and BootOrder
// (boot this entry by default) in variables that survive a reboot.
// BIOS firmware has no such store, so a BIOS machine keeps the same
// choices in GRUB's environment block, a 1024-byte file that GRUB reads
// and writes at boot. default_slot is the slot that a normal boot runs.
// try_slot is a slot to boot once, and grub.cfg clears it before it
// loads the kernel.
//
// The block is a signature line, then name=value lines, padded with '#'
// to exactly 1024 bytes. Lines that start with # are comments. The size
// must not change, because GRUB's save_env overwrites the file's blocks
// in place through its own FAT driver, and a write there that had to
// allocate a block would not be safe.
//
// liken writes the block itself instead of shipping grub-editenv,
// because the format is small and documented. Linux writes it with
// writeFileDurably, a temporary file and a rename. That is safe for
// GRUB, because GRUB looks up the file's blocks again at each boot.

import (
	"fmt"
	"maps"
	"os"
	"sort"
	"strings"
)

const (
	grubEnvSize      = 1024
	grubEnvSignature = "# GRUB Environment Block\n"
)

// parseGRUBEnv returns the variables of an environment block. It
// accepts what GRUB accepts: exactly 1024 bytes, the signature first,
// and comment lines ignored, which includes the '#' padding after the
// last variable.
func parseGRUBEnv(block []byte) (map[string]string, error) {
	if len(block) != grubEnvSize {
		return nil, fmt.Errorf("the GRUB environment block has %d bytes and must have exactly %d", len(block), grubEnvSize)
	}
	text := string(block)
	if !strings.HasPrefix(text, grubEnvSignature) {
		return nil, fmt.Errorf("the GRUB environment block does not start with the signature")
	}
	vars := map[string]string{}
	for line := range strings.SplitSeq(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("the GRUB environment block has a line that is neither a comment nor name=value: %q", line)
		}
		vars[name] = value
	}
	return vars, nil
}

// renderGRUBEnv returns a block that holds exactly these variables,
// sorted by name so the same variables always produce the same bytes.
func renderGRUBEnv(vars map[string]string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(grubEnvSignature)
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := vars[name]
		if strings.ContainsAny(name, "=\n#") || name == "" {
			return nil, fmt.Errorf("%q is not a valid GRUB environment variable name, which is not empty and has no '=', '#', or newline", name)
		}
		if strings.Contains(value, "\n") {
			return nil, fmt.Errorf("the GRUB environment value has a newline: %q", value)
		}
		b.WriteString(name)
		b.WriteString("=")
		b.WriteString(value)
		b.WriteString("\n")
	}
	if b.Len() > grubEnvSize {
		return nil, fmt.Errorf("the variables need %d bytes and the block has %d", b.Len(), grubEnvSize)
	}
	block := make([]byte, grubEnvSize)
	copy(block, b.String())
	for i := b.Len(); i < grubEnvSize; i++ {
		block[i] = '#'
	}
	return block, nil
}

// updateGRUBEnv reads the block at path, sets the given variables, and
// writes the block back durably. A variable set to the empty string
// stays in the block, because grub.cfg's -n tests treat an empty
// variable and a missing one the same, and keeping the name makes the
// block easier to read.
func updateGRUBEnv(path string, set map[string]string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	vars, err := parseGRUBEnv(raw)
	if err != nil {
		return err
	}
	maps.Copy(vars, set)
	block, err := renderGRUBEnv(vars)
	if err != nil {
		return err
	}
	return writeFileDurably(path, block)
}

func readGRUBEnv(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseGRUBEnv(raw)
}
