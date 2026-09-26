package machine

// The serio subtrees of the facts tree: serio/, the standing of each
// attachment, and boot/serio/, the entries the boot declared.
//
// Both are lists keyed by position, counted from zero, the way
// boot/network/interfaces is. A status list mixes matched lines with
// entries that match none, so no single field is a natural key, and
// the position keeps the order init reported them in.
//
// Each element is one record file, by rule 5, because both lists
// change while the machine runs: the serio watch rewrites serio/ when
// an adapter is plugged in or unplugged, and the module loader
// rewrites boot/serio/ when a live load adds an entry. A rename
// replaces one element's record in one step, so a reader never reads
// a record that names one tty and another tty's state.

import (
	"path/filepath"
	"strconv"
	"strings"
)

// WriteSerio publishes the standing of every attachment. The serio
// watch owns this subtree (init/serio.go).
func (t FactsTree) WriteSerio(statuses []SerioStatus) error {
	records := make([][][2]string, 0, len(statuses))
	for _, s := range statuses {
		records = append(records, [][2]string{
			{"protocol", s.Protocol},
			{"vendor", s.USB.Vendor},
			{"product", s.USB.Product},
			{"serial", s.USB.Serial},
			{"tty", s.TTY},
			{"port", s.Port},
			{"state", string(s.State)},
			{"message", s.Message},
			// A node path comes from a DEVNAME, which holds no space,
			// so one space separates the list on its one line.
			{"nodes", strings.Join(s.Nodes, " ")},
		})
	}
	return t.report(t.writePositionalRecords("serio", records))
}

// WriteBootSerio publishes the spec.serio list the boot declared. The
// boot writes it, and the module loader rewrites it when a live load
// adds an entry.
func (t FactsTree) WriteBootSerio(entries []SerioAttachment) error {
	records := make([][][2]string, 0, len(entries))
	for _, a := range entries {
		records = append(records, [][2]string{
			{"protocol", a.Protocol},
			{"vendor", a.USB.Vendor},
			{"product", a.USB.Product},
			{"serial", a.USB.Serial},
		})
	}
	return t.report(t.writePositionalRecords(filepath.Join("boot", "serio"), records))
}

// writePositionalRecords writes one record file for each element,
// named by its position, and removes the files of the positions the
// list no longer reaches.
func (t FactsTree) writePositionalRecords(dir string, records [][][2]string) error {
	want := map[string]bool{}
	for i, record := range records {
		key := strconv.Itoa(i)
		want[key] = true
		if err := t.writeRecordFact(filepath.Join(dir, key), record); err != nil {
			return err
		}
	}
	return syncEntryFiles(filepath.Join(t.Dir, dir), want)
}

// readSerio reads serio/ back into the status list, in position
// order.
func (t FactsTree) readSerio() ([]SerioStatus, error) {
	records, err := t.readPositionalRecords("serio")
	if err != nil {
		return nil, err
	}
	var statuses []SerioStatus
	for _, r := range records {
		s := SerioStatus{
			Protocol: r["protocol"],
			USB:      SerioUSB{Vendor: r["vendor"], Product: r["product"], Serial: r["serial"]},
			TTY:      r["tty"],
			Port:     r["port"],
			State:    SerioState(r["state"]),
			Message:  r["message"],
		}
		if r["nodes"] != "" {
			s.Nodes = strings.Split(r["nodes"], " ")
		}
		statuses = append(statuses, s)
	}
	return statuses, nil
}

// readBootSerio reads boot/serio/ back into the declared list.
func (t FactsTree) readBootSerio() ([]SerioAttachment, error) {
	records, err := t.readPositionalRecords(filepath.Join("boot", "serio"))
	if err != nil {
		return nil, err
	}
	var entries []SerioAttachment
	for _, r := range records {
		entries = append(entries, SerioAttachment{
			Protocol: r["protocol"],
			USB:      SerioUSB{Vendor: r["vendor"], Product: r["product"], Serial: r["serial"]},
		})
	}
	return entries, nil
}

// readPositionalRecords reads the records at positions 0, 1, 2, and on,
// until a position has no file. Reading by position keeps the list's
// order, which sorted file names would lose at the tenth element.
func (t FactsTree) readPositionalRecords(dir string) ([]map[string]string, error) {
	var records []map[string]string
	for i := 0; ; i++ {
		record, err := t.readRecordFact(filepath.Join(dir, strconv.Itoa(i)))
		if err != nil {
			return nil, err
		}
		if record == nil {
			return records, nil
		}
		records = append(records, record)
	}
}
