package main

// The positions travel the bus as H:MM:SS and the store holds seconds,
// so the two conversions are here.

import (
	"fmt"
	"strconv"
	"strings"
)

// parsePosition reads H:MM:SS as seconds. The hours may run past one
// digit, because a Play of a list runs as long as the list does. An
// empty value is the start of a Play and reads as zero; any other shape
// answers ok false, and the caller records zero and logs it.
func parsePosition(value string) (int, bool) {
	if value == "" {
		return 0, true
	}
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, false
	}
	seconds := 0
	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return 0, false
		}
		seconds = seconds*60 + number
	}
	return seconds, true
}

// formatPosition writes seconds back as H:MM:SS, the shape every
// position on the bus carries.
func formatPosition(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}
