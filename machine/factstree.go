package machine

// The facts tree carries what init observed to the operator, as a
// directory of small files under /run/liken/facts.
//
// Init learns facts that no program inside the cluster can observe
// directly: the DHCP exchange, the moment of boot, the hardware as the
// kernel first showed it. The operator reads these facts, adds what it
// observes itself, and publishes the result to the Machine's status.
//
// The tree is one rendering of the same contract that MachineStatus and
// the CRD describe. Each path segment is a JSON field name, so the
// three renderings read alike. A person can explore the tree with ls,
// cat, and grep, and read one fact without parsing the whole status.
//
// One value lives in one file. This is the reason the tree exists as a
// tree at all. A single status file forces one writer, because a write
// serializes the whole struct, and two writers would race to rewrite
// the same bytes. A tree gives each fact its own file, so each init
// component writes its own subtree with no shared lock. The ownership
// map has no file with two owners:
//
//	time/                          the clock loop
//	hardware/blockDevices/         the hardware watch
//	hardware/unclaimed/            the hardware watch
//	boot/manifest, boot/modules    the module loader
//	modules/                       the module loader
//	boot/clusterManifest           the restart path
//	boot/credentials               the restart path
//	boot/restarts                  the restart path
//	features/, registries/         the restart path
//	runtime/                       the restart path
//	everything else                the boot step that discovers it
//
// A boot step writes its subtree once, at the point where it discovers
// the fact, and prints its console line from the same place. The tree
// fills in as the boot runs. A partial tree is safe, because the
// operator runs under k3s, and k3s starts only after the boot steps
// finish.
//
// The grammar has five rules:
//
//  1. A scalar fact is one file: the value plus one trailing newline.
//     Strings are raw, integers decimal, booleans the word true,
//     timestamps RFC3339Nano in UTC, and enums their API word.
//  2. An absent fact has no file, and a zero value has no file. A
//     reader treats a missing file as the zero value, which is
//     omitempty semantics. A writer removes the file for a fact that
//     disappears.
//  3. A scalar list is one file, one item per line, in list order. An
//     empty list has no file.
//  4. An identity-keyed collection is a directory for each element,
//     named by the element's natural key.
//  5. A group of facts that change together mid-run is one record file
//     of key=value lines.
//
// Rule 5 applies to four boot records only: boot/manifest,
// boot/clusterManifest, boot/credentials, and boot/imports. Each of
// these is rewritten while the machine runs. A rename replaces one
// inode in one step, so a rename of one file is the only write that a
// concurrent reader can never see half done. The paired fields of each
// record must land together, so each pair is one file that one rename
// replaces. The rest of the tree writes once at boot, before the
// operator exists, so those facts cannot tear and stay one file each.
//
// The tree lives on tmpfs, so a write needs no fsync. Every write goes
// through writeAtomic: a temp file in the same directory, then a
// rename. A reader that polls or wakes on its own schedule sees either
// the old file or the new file, never a torn write.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FactsDir is the root of the facts tree. It sits on /run, which is
// tmpfs, because the facts describe one boot and a reboot must start
// them empty.
const FactsDir = "/run/liken/facts"

// FactsTree is one facts tree, rooted at Dir. Production code roots it
// at FactsDir. Tests root it at a temporary directory, so a test never
// touches the machine's real /run.
//
// A fact write must never stop the machine: a lost fact is a reporting
// gap the operator fills with a zero value, not a reason to halt PID 1.
// The program that owns the tree states that policy once, in Report,
// rather than at every call site. Each writer routes its result through
// report, which hands a failure to Report and returns the error
// unchanged. The reporter is additive: the error still reaches the
// caller, so a test with a nil Report asserts it directly.
type FactsTree struct {
	Dir string
	// Report receives each failed write. A nil Report leaves the error
	// with the caller.
	Report func(err error)
}

// report hands a failed write to the tree's reporter and returns the
// error unchanged, so the caller still sees it.
func (t FactsTree) report(err error) error {
	if err != nil && t.Report != nil {
		t.Report(err)
	}
	return err
}

// keyPattern is the set of characters that a collection key may use
// directly, for interfaces, disks, modules, and features. These keys
// come from the kernel and from the spec, and they already read as
// plain names, so the writer asserts the pattern instead of rewriting
// the key. A key outside the pattern is a programming error, not a
// value to sanitize.
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// assertKey checks that a collection key is safe to use as a directory
// name. It rejects a key that could escape its parent or collide with
// the tree's own structure.
func assertKey(kind, key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("facts %s key %q is not a plain name", kind, key)
	}
	return nil
}

// safeKey turns a modalias into a directory name. A modalias is the
// kernel's own fingerprint string, and it carries bytes that a path
// segment cannot hold, such as a slash or a space. safeKey replaces
// every byte outside [A-Za-z0-9._:+-] with an underscore. If it
// changed any byte, or the result runs past 200 bytes, it truncates to
// 64 bytes and appends a dash and the first twelve hex digits of the
// modalias's sha256. The hash makes the key unique, so two aliases
// that differ only in an unsafe byte never collide. The exact modalias
// lives in the modalias file inside the directory, so the key itself
// needs no way back to the original.
func safeKey(modalias string) string {
	var b strings.Builder
	changed := false
	for i := range len(modalias) {
		c := modalias[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteByte(c)
		case c == '.' || c == '_' || c == ':' || c == '+' || c == '-':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
			changed = true
		}
	}
	key := b.String()
	if !changed && len(key) <= 200 {
		return key
	}
	sum := sha256.Sum256([]byte(modalias))
	suffix := hex.EncodeToString(sum[:])[:12]
	if len(key) > 64 {
		key = key[:64]
	}
	return key + "-" + suffix
}

// writeFact writes one scalar file. An empty value means the fact is
// absent, so writeFact removes the file instead. A reader treats the
// missing file as the zero value.
func (t FactsTree) writeFact(rel, value string) error {
	path := filepath.Join(t.Dir, rel)
	if value == "" {
		return removeFile(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, []byte(value+"\n"))
}

// writeListFact writes one scalar list, one item per line, in order.
// An empty list means the fact is absent, so writeListFact removes the
// file.
func (t FactsTree) writeListFact(rel string, items []string) error {
	path := filepath.Join(t.Dir, rel)
	if len(items) == 0 {
		return removeFile(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, []byte(strings.Join(items, "\n")+"\n"))
}

// writeRecordFact writes one record file of key=value lines, in the
// given order. It skips a pair whose value is empty, so an absent
// field of the record leaves no line. If no pair has a value, the whole
// record is absent, so writeRecordFact removes the file. A value cannot
// hold a newline or a control byte, because those would break the line
// grammar, so writeRecordFact replaces each with a space.
func (t FactsTree) writeRecordFact(rel string, pairs [][2]string) error {
	var b strings.Builder
	for _, p := range pairs {
		if p[1] == "" {
			continue
		}
		b.WriteString(p[0])
		b.WriteByte('=')
		b.WriteString(sanitizeValue(p[1]))
		b.WriteByte('\n')
	}
	path := filepath.Join(t.Dir, rel)
	if b.Len() == 0 {
		return removeFile(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, []byte(b.String()))
}

// sanitizeValue replaces every newline and control byte with a space,
// so a record value stays on one line. A record file's grammar is one
// key=value pair per line, and a raw newline in a value would forge a
// second line.
func sanitizeValue(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

// readFact reads one scalar file. A missing file is the zero value, so
// readFact returns an empty string with no error. It strips the single
// trailing newline that writeFact added.
func (t FactsTree) readFact(rel string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(t.Dir, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(raw), "\n"), nil
}

// readListFact reads one scalar list into its lines, in order. A
// missing file is an empty list.
func (t FactsTree) readListFact(rel string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(t.Dir, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(string(raw), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// readRecordFact reads one record file into a map of its key=value
// lines. A missing file is an empty record. It reads each key on its
// own, so it accepts a record that carries a key it does not know, and
// a caller reads only the keys it wants.
func (t FactsTree) readRecordFact(rel string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(t.Dir, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		record[key] = value
	}
	return record, nil
}

// readInt reads a scalar file as a decimal integer. A missing file is
// zero. A file that does not hold an integer is an error that names its
// path, so an operator can find the bad file.
func (t FactsTree) readInt(rel string) (int, error) {
	value, err := t.readFact(rel)
	if err != nil || value == "" {
		return 0, err
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", filepath.Join(t.Dir, rel), err)
	}
	return n, nil
}

// readUint reads a scalar file as a decimal unsigned integer, the shape
// of every byte count in the tree. A missing file is zero, and a file
// that does not hold an unsigned integer is an error that names its
// path.
func (t FactsTree) readUint(rel string) (uint64, error) {
	value, err := t.readFact(rel)
	if err != nil || value == "" {
		return 0, err
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", filepath.Join(t.Dir, rel), err)
	}
	return n, nil
}

// readTime reads a scalar file as an RFC3339Nano timestamp in UTC. A
// missing file is a nil time, the absent value for every timestamp in
// the tree. A file that does not hold a timestamp is an error that
// names its path.
func (t FactsTree) readTime(rel string) (*time.Time, error) {
	value, err := t.readFact(rel)
	if err != nil || value == "" {
		return nil, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(t.Dir, rel), err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

// formatTime renders a timestamp for the tree: RFC3339Nano in UTC. A
// nil or zero time renders empty, so its file is absent.
func formatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// formatInt renders an integer for the tree. Zero renders empty, so a
// zero value leaves no file.
func formatInt(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// formatUint renders an unsigned integer for the tree. Zero renders
// empty, so a zero value leaves no file.
func formatUint(n uint64) string {
	if n == 0 {
		return ""
	}
	return strconv.FormatUint(n, 10)
}

// formatBool renders a boolean for the tree. Only a true value has a
// file, and its content is the word true. A false value leaves no file,
// which the reader treats as false.
func formatBool(b bool) string {
	if b {
		return "true"
	}
	return ""
}

// removeFile deletes a file that a fact no longer needs. A file that is
// already gone is not an error, because the goal is only its absence.
func removeFile(path string) error {
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// syncEntryDirs removes the element directories under parent whose keys
// are not in want. It is how every collection writer honors rule 2: an
// element that leaves the set loses its directory. It removes
// directories only, so a temp file that another write left behind stays
// untouched. A parent that does not exist yet has nothing to remove.
func syncEntryDirs(parent string, want map[string]bool) error {
	entries, err := os.ReadDir(parent)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || want[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(parent, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
