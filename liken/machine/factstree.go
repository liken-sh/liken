package machine

// The facts tree is how init passes what it observed to the operator.
// It is a directory of small files under /run/liken/facts.
//
// Init records facts that no program inside the cluster can observe
// directly: the DHCP exchange, the time of boot, and the hardware as
// the kernel first showed it. The operator reads these facts, adds
// what it observes itself, and publishes the result to the Machine's
// status.
//
// The tree mirrors MachineStatus and the CRD, because each path
// segment is a JSON field name. You can read one fact with cat, or
// explore the tree with ls and grep, without parsing the whole status.
//
// Each fact is in its own file so that each part of init can write its
// own subtree with no shared lock. A single status file would allow
// only one writer, because each write replaces the whole file. Each
// path below has one owner:
//
//	time/                          the clock loop
//	hardware/blockDevices/         the hardware watch
//	hardware/unclaimed/            the hardware watch
//	boot/manifest, boot/modules    the module loader
//	boot/serio/                    the module loader
//	modules/                       the module loader
//	serio/                         the serio watch
//	boot/clusterManifest           the restart path
//	boot/credentials               the restart path
//	boot/restarts                  the restart path
//	features/, registries/         the restart path
//	runtime/                       the restart path
//	network/                       the boot step, then the radio watch
//	firmware/                      the boot step, then the proving watch
//	everything else                the boot step that discovers it
//
// A boot step writes its subtree when it discovers the fact. The
// operator runs under k3s, and k3s starts only after the boot steps
// finish, so the operator never reads a subtree that a boot step has
// only partly written.
//
// The tree follows five rules:
//
//  1. A scalar fact is one file: the value plus one trailing newline.
//     Strings are raw, integers are decimal, booleans are the word
//     true, timestamps are RFC3339Nano in UTC, and enums are their API
//     word.
//  2. An absent fact has no file, and a zero value has no file. A
//     reader treats a missing file as the zero value, as omitempty
//     does. A writer removes the file for a fact that disappears.
//  3. A scalar list is one file, one item per line, in list order. An
//     empty list has no file.
//  4. A collection with a natural key is a directory with one
//     subdirectory for each element, named by the element's key.
//  5. A group of facts that must change together while the machine
//     runs is one record file of key=value lines.
//
// Rule 2 has one exception, boot/network. A machine that declared no
// interface and a machine whose boot recorded no network are different
// cases, and the operator must act differently on each. So the
// boot/network directory exists whenever the boot recorded anything,
// even when it has no file under it. No other empty directory means
// anything.
//
// The tree is on tmpfs, so a write needs no fsync. Every write goes
// through writeAtomic, which writes a temp file in the same directory
// and renames it over the target, so a reader sees either the old file
// or the new one, never a partial write.
//
// The owners in the table other than the boot steps rewrite their
// subtrees while the operator runs. A subtree of several files changes
// one file at a time, so a pass of the operator can read part of the
// new subtree with part of the old. The operator watches the tree with
// WatchFactsTree, so the last rename wakes another pass, which reads
// the subtree as the writer left it. A record file of rule 5 holds
// fields that a reader must never see from two different writes,
// because one rename replaces all of them. These are the records:
//
//   - boot/manifest, boot/clusterManifest, boot/credentials, and
//     boot/imports
//   - time/standing, the state, source, stratum, and offset of the clock
//   - network/interfaces/<name>/wireless/association for each radio
//   - each element of the serio lists, serio/ and boot/serio/

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

// FactsDir is the root of the facts tree. It is on /run, which is
// tmpfs, so a reboot starts with an empty tree.
const FactsDir = "/run/liken/facts"

// FactsTree is one facts tree, rooted at Dir. Production code uses
// FactsDir, and tests use a temporary directory.
//
// A failed fact write must not stop PID 1. The operator reads a lost
// fact as a zero value, which leaves a gap in the status. So init sets
// Report to log each write error, and does not stop on one. Each
// writer still returns its error, so a test with a nil Report can
// check it.
type FactsTree struct {
	Dir string
	// Report, when it is not nil, receives each error that a write
	// returns.
	Report func(err error)
}

func (t FactsTree) report(err error) error {
	if err != nil && t.Report != nil {
		t.Report(err)
	}
	return err
}

// keyPattern matches the keys of collections such as interfaces,
// disks, modules, and features. These keys come from the kernel and
// the spec and are already plain names. A key outside the pattern, such
// as one with a slash, is a programming error, so a writer refuses it
// and does not escape it.
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

func assertKey(kind, key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("the facts tree has a %s key %q outside %s", kind, key, keyPattern)
	}
	return nil
}

// safeKey turns a kernel modalias, which can contain a slash or a
// space, into a directory name. It replaces each byte outside
// [A-Za-z0-9._:+-] with an underscore. When it replaced a byte, or the
// key is longer than 200 bytes, it cuts the key to 64 bytes and appends
// a dash and 12 hex digits of the modalias's sha256, so two aliases
// that differ only in an unsafe byte get different keys. The key need
// not be reversible, because the directory's modalias file holds the
// exact modalias.
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

// writeFact writes one scalar file. An empty value is an absent fact,
// so writeFact removes the file instead.
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
// An empty list is an absent fact, so writeListFact removes the file.
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
// order of pairs. A pair with an empty value is an absent field and
// gets no line. When every value is empty, the record is absent and
// writeRecordFact removes the file.
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

// sanitizeValue replaces each newline and control character with a
// space. A newline in a value would start a new line in the record
// file, and a reader would take that line as another key.
func sanitizeValue(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

// readFact reads one scalar file without its trailing newline. A
// missing file returns an empty string and no error.
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

// readListFact reads one scalar list, one item for each line. A
// missing file returns an empty list.
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
// lines. A missing file returns a nil map. It skips a line with no "=".
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

// readInt reads a scalar file as a decimal integer. A missing file
// returns zero. A file that is not an integer returns an error with
// the file's path.
func (t FactsTree) readInt(rel string) (int, error) {
	value, err := t.readFact(rel)
	if err != nil || value == "" {
		return 0, err
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("reading %s as a number: %w", filepath.Join(t.Dir, rel), err)
	}
	return n, nil
}

// readUint reads a scalar file as a decimal unsigned integer, which is
// how the tree stores each byte count. A missing file returns zero. A
// file that is not an unsigned integer returns an error with the
// file's path.
func (t FactsTree) readUint(rel string) (uint64, error) {
	value, err := t.readFact(rel)
	if err != nil || value == "" {
		return 0, err
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("reading %s as a number: %w", filepath.Join(t.Dir, rel), err)
	}
	return n, nil
}

// readTime reads a scalar file as an RFC3339Nano timestamp and returns
// it in UTC. A missing file returns a nil time. A file that is not a
// timestamp returns an error with the file's path.
func (t FactsTree) readTime(rel string) (*time.Time, error) {
	value, err := t.readFact(rel)
	if err != nil || value == "" {
		return nil, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("reading %s as a timestamp: %w", filepath.Join(t.Dir, rel), err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

// formatTime formats a timestamp as RFC3339Nano in UTC. A nil or zero
// time formats as empty, so it gets no file.
func formatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// formatInt formats an integer in decimal. Zero formats as empty, so
// it gets no file.
func formatInt(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// formatUint formats an unsigned integer in decimal. Zero formats as
// empty, so it gets no file.
func formatUint(n uint64) string {
	if n == 0 {
		return ""
	}
	return strconv.FormatUint(n, 10)
}

// formatBool formats true as the word true and false as empty, so a
// false fact has no file. A reader treats a missing file as false.
func formatBool(b bool) string {
	if b {
		return "true"
	}
	return ""
}

// formatOptionalBool formats a boolean that can be unknown. True and
// false each get a file, and nil gets none, so a reader can tell false
// from unknown.
func formatOptionalBool(b *bool) string {
	if b == nil {
		return ""
	}
	return strconv.FormatBool(*b)
}

// parseOptionalBool reverses formatOptionalBool. Content that is not a
// boolean returns nil.
func parseOptionalBool(s string) *bool {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return nil
	}
	return &b
}

// removeFile deletes a file. A missing file is not an error.
func removeFile(path string) error {
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// syncEntryDirs deletes each directory under parent whose name is not
// in want, so an element that leaves a collection loses its directory.
// It leaves files alone, including the temp file of a write in
// progress. A missing parent is not an error.
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

// syncEntryFiles is syncEntryDirs for a collection whose elements are
// files. It deletes each file under parent whose name is not in want,
// and leaves directories alone.
func syncEntryFiles(parent string, want map[string]bool) error {
	entries, err := os.ReadDir(parent)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || want[entry.Name()] {
			continue
		}
		if err := removeFile(filepath.Join(parent, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
