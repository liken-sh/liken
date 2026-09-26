package main

// logline.go is the operator's own log: one line for each operation a person
// caused or waits on, such as a Play a screen asked for, a Job a refresh
// started, or a Library that departed. A loop that runs at machine scale, such
// as a pass, a watch event, or a status write that changes nothing, writes no
// line here.
//
// The repository is public and a log line reaches places a person copies it
// to, so a line names a title by an opaque id and never by its words: no
// title, no person from the credits, no path, and no library size.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// logf writes one line under the shared prefix, or nothing when the operator
// was built with no log. The pass, the bus reader, and the webhook server
// each write, so the mutex keeps one line from cutting into another.
func (o *operator) logf(format string, args ...any) {
	if o.log == nil {
		return
	}
	o.logMutex.Lock()
	defer o.logMutex.Unlock()
	fmt.Fprintf(o.log, "library.liken.sh: "+format+"\n", args...)
}

// The schemes of an id whose tail is a folder key or a name, which carries
// the title. A provider's scheme carries a number, which names nothing a
// person reads.
var titledSchemes = map[string]bool{"path": true, "name": true}

// opaqueID is a catalog id as a log line may carry it. The tail of an id
// whose scheme is path or name is replaced by its hash, and every other id
// passes through. The media browser hashes the same way, so one id reads the
// same in both logs.
func opaqueID(id string) string {
	kind, rest, found := strings.Cut(id, ":")
	if !found {
		return id
	}
	scheme, tail, found := strings.Cut(rest, ":")
	if !found || !titledSchemes[scheme] {
		return id
	}
	return kind + ":" + scheme + ":" + hashed(tail)
}

// hashed is the first 12 hexadecimal characters of the SHA-256 of a value:
// enough to tell two titles apart in one log, and nothing a person can read
// the title from.
func hashed(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

// workNamed is the identity a play request or a Play carries, as a log line
// may carry it: the provider ids in provider order, with a folder key
// hashed, and the numbers of an episode after them.
func workNamed(aliases map[string]string, season, episode int) string {
	parts := []string{}
	for _, provider := range slices.Sorted(maps.Keys(aliases)) {
		value := aliases[provider]
		if titledSchemes[provider] {
			value = hashed(value)
		}
		parts = append(parts, provider+":"+value)
	}
	if season != 0 || episode != 0 {
		parts = append(parts, fmt.Sprintf("s%02de%02d", season, episode))
	}
	if len(parts) == 0 {
		return "a work with no ids"
	}
	return strings.Join(parts, " ")
}

// counted is a count and its noun, with the noun plural for every count but
// one, so a line reads "1 folder" and "2 folders".
func counted(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}
