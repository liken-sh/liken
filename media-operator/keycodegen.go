//go:build ignore

package main

// This program writes keycodes.go from the kernel's
// input-event-codes.h. `make codes` runs it, and the result is
// committed, so a build reads no kernel header.

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The three names in the KEY_ space that are not controls: two bounds
// and one alias the kernel keeps for its own filtering.
var notCodes = map[string]bool{
	"KEY_MAX":             true,
	"KEY_CNT":             true,
	"KEY_MIN_INTERESTING": true,
}

var define = regexp.MustCompile(`^#define\s+((?:KEY|BTN)_[A-Z0-9_]+)\s+(\S+)`)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: keycodegen <input-event-codes.h> <output.go>")
		os.Exit(1)
	}
	header, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the header: %v\n", err)
		os.Exit(1)
	}

	type entry struct {
		name    string
		code    uint16
		literal bool
	}
	var read []entry
	codes := map[string]uint16{}
	for _, line := range strings.Split(string(header), "\n") {
		fields := define.FindStringSubmatch(line)
		if fields == nil {
			continue
		}
		name, value := fields[1], fields[2]
		if notCodes[name] {
			continue
		}
		code, known := resolve(value, codes)
		if !known {
			continue
		}
		codes[name] = code
		_, aliased := codes[value]
		read = append(read, entry{name: name, code: code, literal: !aliased})
	}

	// A name the header immediately re-defines with the same literal
	// code is a range marker, such as BTN_GAMEPAD before BTN_SOUTH: it
	// names a class of device and not a control, so the table drops it
	// and keeps the control's own name.
	var entries []entry
	for index, each := range read {
		next := index + 1
		if each.literal && next < len(read) && read[next].literal && read[next].code == each.code {
			continue
		}
		entries = append(entries, each)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "// Code generated from %s. DO NOT EDIT.\n\n", os.Args[1])
	out.WriteString("package main\n\n")
	out.WriteString("// keyCodes is the kernel's whole EV_KEY name space, in header order.\n")
	out.WriteString("// An alias follows the name it aliases, so the first name for a code\n")
	out.WriteString("// is the control's own.\n")
	out.WriteString("var keyCodes = []keyCode{\n")
	for _, each := range entries {
		fmt.Fprintf(&out, "\t{Name: %q, Code: 0x%03x},\n", each.name, each.code)
	}
	out.WriteString("}\n")

	if err := os.WriteFile(os.Args[2], []byte(out.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "writing the table: %v\n", err)
		os.Exit(1)
	}
}

// resolve reads one define's value: a literal, decimal or hexadecimal,
// or the name of a code the header already defined.
func resolve(value string, codes map[string]uint16) (uint16, bool) {
	if code, aliased := codes[value]; aliased {
		return code, true
	}
	number, err := strconv.ParseUint(value, 0, 16)
	if err != nil {
		return 0, false
	}
	return uint16(number), true
}
