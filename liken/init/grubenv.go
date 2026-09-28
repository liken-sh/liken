package main

// GRUB's environment block: the BIOS machine's boot variables.
//
// UEFI firmware gives liken a small durable store with exactly the
// right shape for blue-green boots: BootNext (try this once) and
// BootOrder (prefer this from now on). BIOS firmware stores nothing,
// so GRUB provides the equivalent: a preallocated 1024-byte file at a
// fixed location that GRUB itself can read and write at boot time,
// designed for exactly this bookkeeping. liken keeps two variables in
// it: default_slot, the proven slot that every unremarkable boot
// should run (it corresponds to BootOrder), and try_slot, the
// one-shot trial (it corresponds to BootNext), which grub.cfg
// consumes before it loads a single kernel byte.
//
// The format is fixed so that GRUB can rewrite the file in place
// through its own filesystem driver: a signature line, then
// name=value lines (lines starting with # are comments), padded with
// '#' characters to exactly 1024 bytes. The size never changes. This
// is what makes the boot-time write safe on FAT: the file's blocks
// are simply overwritten, and no allocation moves.
//
// liken writes the block from Go rather than shipping grub-editenv,
// because it is a 1 KiB documented format, and it fits the same
// write-it-by-hand approach that the GPT and FAT writers already
// follow. Writes from Linux go through the durable temp-and-rename
// path. Unlike GRUB, init has a real filesystem driver underneath,
// and GRUB re-resolves the file's blocks fresh each boot, so the
// in-place constraint applies only to GRUB's own save_env.

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

// parseGRUBEnv reads an environment block's variables. Its strictness
// matches what GRUB itself accepts: exactly 1024 bytes, the signature
// first, comments ignored, and the padding after the last variable
// never parsed as content.
func parseGRUBEnv(block []byte) (map[string]string, error) {
	if len(block) != grubEnvSize {
		return nil, fmt.Errorf("a GRUB environment block is exactly %d bytes, not %d", grubEnvSize, len(block))
	}
	text := string(block)
	if !strings.HasPrefix(text, grubEnvSignature) {
		return nil, fmt.Errorf("the GRUB environment block signature is missing")
	}
	vars := map[string]string{}
	for line := range strings.SplitSeq(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("the GRUB environment block holds a line that is neither comment nor name=value: %q", line)
		}
		vars[name] = value
	}
	return vars, nil
}

// renderGRUBEnv lays out a block holding exactly these variables, in
// sorted order so the same variables always produce the same bytes.
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
			return nil, fmt.Errorf("%q cannot name a GRUB environment variable", name)
		}
		if strings.Contains(value, "\n") {
			return nil, fmt.Errorf("a GRUB environment value cannot span lines: %q", value)
		}
		b.WriteString(name)
		b.WriteString("=")
		b.WriteString(value)
		b.WriteString("\n")
	}
	if b.Len() > grubEnvSize {
		return nil, fmt.Errorf("the variables overflow the block: %d bytes into %d", b.Len(), grubEnvSize)
	}
	block := make([]byte, grubEnvSize)
	copy(block, b.String())
	for i := b.Len(); i < grubEnvSize; i++ {
		block[i] = '#'
	}
	return block, nil
}

// updateGRUBEnv is the read-modify-write function that the actuator
// uses. It loads the block at path, applies the given values (an
// empty value still writes the variable, present but empty, which is
// how a one-shot reads after GRUB consumes it), and writes the result
// durably. A variable mapped to the empty string stays in the block on
// purpose: absent and empty read the same to grub.cfg's -n tests, and
// keeping the name visible makes the block easier to inspect.
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

// readGRUBEnv loads and parses the block at path.
func readGRUBEnv(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseGRUBEnv(raw)
}
