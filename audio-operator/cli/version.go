package main

// Each sibling CLI has a copy of this version comparison, so a change
// to the order belongs in every copy.

import (
	"fmt"
	"strconv"
	"strings"
)

type versionAction int

const (
	actionRun versionAction = iota
	actionWarn
	actionRefuse
)

const syncCommand = "kubectl liken plugins sync"

// decideVersionAction refuses a CLI older than minimumCLI, and warns
// when the CLI differs from the operator. An empty minimumCLI or operator
// version skips that check. A version that does not parse, such as
// "dev", is never older than the minimum. runCapture passes an empty
// minimumCLI, so it never refuses.
func decideVersionAction(cli, operator, minimumCLI string) (versionAction, string) {
	if minimumCLI != "" {
		if order, ok := compareVersions(cli, minimumCLI); ok && order < 0 {
			return actionRefuse, fmt.Sprintf(
				"the operator needs CLI %s or newer, and this CLI is %s: run %s",
				minimumCLI, cli, syncCommand)
		}
	}
	if operator == "" || cli == operator {
		return actionRun, ""
	}
	return actionWarn, fmt.Sprintf(
		"this CLI is %s and the operator is %s: run %s to install the CLI that matches the operator",
		cli, operator, syncCommand)
}

// A development build sorts after the release it follows and before
// the next release.
type versionKey struct {
	year, month, day, serial int
	dev                      bool
	devCount                 int
}

// compareVersions returns -1, 0, or 1 for a before, equal to, or after
// b. It returns false when either version does not parse, such as a
// local "dev" build.
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

// parseVersion reads YYYY.MM.DD-NNN, and the -dev-NNN-sha suffix that a
// development build adds.
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
