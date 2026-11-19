package machine

// The facts tree has two serio lists: serio/ has the state of each
// attachment, and boot/serio/ has the spec.serio entries of the
// boot. Each element is a file named by its position from zero, as in
// boot/network/interfaces, because serio/ mixes entries that match a
// declared attachment with entries that match none, and so has no key
// to name them by.
//
// Each element is one record file of key=value lines, because both
// lists change while the machine runs: the serio watch rewrites
// serio/ when an adapter is plugged in or unplugged, and a live module
// load rewrites boot/serio/. A record is replaced by one rename, so a
// reader never reads one tty's name with another tty's state.

import (
	"path/filepath"
	"strconv"
	"strings"
)

// WriteSerio writes serio/. Only the serio watch in init/serio.go
// calls it.
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
			// A node path comes from a DEVNAME, which has no space.
			{"nodes", strings.Join(s.Nodes, " ")},
		})
	}
	return t.report(t.writePositionalRecords("serio", records))
}

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
// named by its position, and removes the files at positions past the
// end of the list.
//
// writeRecordFact deletes the file of a record whose fields are all
// empty, and readPositionalRecords stops at the first position with no
// file. So such a record would hide itself and every element after it.
// Neither serio list writes one, because every element has the
// protocol of its spec.serio entry, and the schema requires protocol.
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

// readPositionalRecords reads the records at positions 0, 1, 2, and so
// on, until a position has no file. Sorting the file names instead
// would put "10" before "2".
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
