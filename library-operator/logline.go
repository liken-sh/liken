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
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"path/filepath"
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

// opaquePath is a path on a library's volume as a log line may carry it: the
// hash of its place under the root. A folder or a file name carries the
// title, and the hash tells two of them apart. A path the caller already
// holds relative to the root hashes the same as its absolute form.
func opaquePath(root, path string) string {
	if filepath.IsAbs(path) {
		path = relativePath(root, path)
	}
	return "path:" + hashed(path)
}

// playNamed is a Play as a line names it where only its name is at hand. The
// API server mints a Play's name from the title's slug, so the line carries
// the hash of the name.
func playNamed(namespace, name string) string {
	return "the Play " + namespace + "/" + hashed(name)
}

// entryNamed is a person's entry as a log line names it: the hash of the
// entry's path, as the media browser names it. The path is the person's
// name.
func entryNamed(path string) string {
	return "entry " + hashed(path)
}

// opaqueError is the text of an error as a log line may carry it, with every
// path and address in it made opaque by opaqueText. An os error carries the
// whole path it failed on, and a failed request carries the whole address it
// asked, so the text keeps the cause and loses the title.
func opaqueError(err error, roots ...string) string {
	if err == nil {
		return "<nil>"
	}
	return opaqueText(err.Error(), roots...)
}

// opaqueErrors is the arguments of a log line with every error among them
// replaced by its opaqueError text, so a worker's line keeps an error's cause
// and loses its paths whatever verb prints it.
func opaqueErrors(args []any, roots ...string) []any {
	filtered := slices.Clone(args)
	for index, arg := range filtered {
		if err, isError := arg.(error); isError {
			filtered[index] = opaqueError(err, roots...)
		}
	}
	return filtered
}

// opaqueText makes every path under one of the roots, or under the mounts
// where a Job reads a library and its art, into its opaquePath form, and
// every web address into its host and the hash of the rest. The rest of the
// text stays word for word. A path ends at the quote it opened with, or at
// ": ", "; ", or the end of a line, which are the marks an error puts after
// a path; a quote inside a title does not end it.
func opaqueText(text string, roots ...string) string {
	roots = append(slices.Clone(roots), libraryMountPath, artMountPath)
	for index, root := range roots {
		roots[index] = filepath.Clean(root)
	}
	// The longest root matches first, so a path reads relative to the
	// library's own root and not to the mount above it.
	slices.SortFunc(roots, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	var out strings.Builder
	for index := 0; index < len(text); {
		rest := text[index:]
		if length, opaque := opaqueAddressAt(rest); length > 0 {
			out.WriteString(opaque)
			index += length
			continue
		}
		var quote byte
		if index > 0 && (text[index-1] == '"' || text[index-1] == '\'') {
			quote = text[index-1]
		}
		if length, opaque := opaquePathAt(rest, quote, roots); length > 0 {
			out.WriteString(opaque)
			index += length
			continue
		}
		out.WriteByte(text[index])
		index++
	}
	return out.String()
}

// The path under a root that starts the text, as its length in the text and
// its opaque form, or a length of zero. The root alone names no title, so a
// path must reach below it.
func opaquePathAt(text string, quote byte, roots []string) (int, string) {
	for _, root := range roots {
		if root == "/" || !strings.HasPrefix(text, root+"/") {
			continue
		}
		end := len(text)
		if quote != 0 {
			if at := strings.IndexByte(text, quote); at >= 0 {
				end = at
			}
		} else {
			for _, mark := range []string{": ", "; ", "\n"} {
				if at := strings.Index(text, mark); at >= 0 && at < end {
					end = at
				}
			}
		}
		return end, opaquePath(root, text[:end])
	}
	return 0, ""
}

// The web address that starts the text, as its length in the text and its
// opaque form, or a length of zero. The host names a provider and stays;
// the path and the query can name the title, so they turn into a hash. An
// address ends at a space or a quote, and the punctuation of the sentence
// after it is not part of it.
func opaqueAddressAt(text string) (int, string) {
	if !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
		return 0, ""
	}
	end := strings.IndexAny(text, " \t\n\"'<>")
	if end < 0 {
		end = len(text)
	}
	address := strings.TrimRight(text[:end], ":;,.)")
	scheme, rest, _ := strings.Cut(address, "://")
	host, tail, _ := strings.Cut(rest, "/")
	if tail == "" {
		return len(address), address
	}
	return len(address), scheme + "://" + host + "/" + hashed(tail)
}
