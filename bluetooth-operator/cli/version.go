package main

// The check of the CLI's version against the operator's version.

import (
	"fmt"
	"strconv"
	"strings"
)

// versionAction is what the CLI does after the version check.
type versionAction int

const (
	actionRun versionAction = iota
	actionWarn
	actionRefuse
)

// syncCommand is the command that the version messages tell a person to
// run to update the CLI.
const syncCommand = "kubectl liken plugins sync"

// decideVersionAction returns the action for a CLI at version cli, and
// the message to print. It refuses when cli is older than minimumCLI,
// warns when cli differs from operator, and runs otherwise. It never
// refuses when minimumCLI is empty or either version does not parse,
// and never warns when operator is empty.
func decideVersionAction(cli, operator, minimumCLI string) (versionAction, string) {
	if minimumCLI != "" {
		if order, ok := compareVersions(cli, minimumCLI); ok && order < 0 {
			return actionRefuse, fmt.Sprintf(
				"the operator needs CLI %s or newer and this CLI is %s, run %s",
				minimumCLI, cli, syncCommand)
		}
	}
	if operator == "" || cli == operator {
		return actionRun, ""
	}
	return actionWarn, fmt.Sprintf(
		"this CLI is %s and the operator is %s, run %s",
		cli, operator, syncCommand)
}

// versionKey is a parsed `liken` version. A development build sorts
// after the release with the same date and serial, and before the next
// release.
type versionKey struct {
	year, month, day, serial int
	dev                      bool
	devCount                 int
}

// compareVersions returns -1, 0, or 1 when a is older than, equal to,
// or newer than b. ok is false when either version does not parse, as
// with a local build named "dev".
func compareVersions(a, b string) (int, bool) {
	ak, aok := parseVersion(a)
	bk, bok := parseVersion(b)
	if !aok || !bok {
		return 0, false
	}
	for _, pair := range [][2]int{
		{ak.year, bk.year},
		{ak.month, bk.month},
		{ak.day, bk.day},
		{ak.serial, bk.serial},
		{boolToInt(ak.dev), boolToInt(bk.dev)},
		{ak.devCount, bk.devCount},
	} {
		if pair[0] < pair[1] {
			return -1, true
		}
		if pair[0] > pair[1] {
			return 1, true
		}
	}
	return 0, true
}

// parseVersion parses YYYY.MM.DD-NNN, with the optional -dev-NNN-sha
// suffix of a development build.
func parseVersion(text string) (versionKey, bool) {
	parts := strings.Split(text, "-")
	if len(parts) != 2 && len(parts) != 5 {
		return versionKey{}, false
	}
	date := strings.Split(parts[0], ".")
	if len(date) != 3 {
		return versionKey{}, false
	}
	year, ok1 := atoi(date[0])
	month, ok2 := atoi(date[1])
	day, ok3 := atoi(date[2])
	serial, ok4 := atoi(parts[1])
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return versionKey{}, false
	}
	key := versionKey{year: year, month: month, day: day, serial: serial}
	if len(parts) == 5 {
		if parts[2] != "dev" {
			return versionKey{}, false
		}
		count, ok := atoi(parts[3])
		if !ok {
			return versionKey{}, false
		}
		key.dev = true
		key.devCount = count
	}
	return key, true
}

func atoi(text string) (int, bool) {
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0, false
	}
	return value, true
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
