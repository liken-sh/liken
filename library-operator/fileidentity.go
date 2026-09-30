package main

// fileidentity.go tells the media file at a path from the file that was
// there before it. The facts record their work on a file under the file's
// path, so a download manager that imports a new encode under the old name
// leaves every fact of the old encode in place. The probe record holds the
// size of the file the probe read, and a file of another size is another
// file. The modified time does not decide, because a download manager
// copies it from the source, and a tag edit or a restore moves it on a file
// whose content is the same.
//
// The walk and the phases read this one rule. The walk decides which
// attempts still describe the volume. A phase reads it again just before it
// replaces a thumbnail, a tile directory, or a set of marks, because the walk
// that opened the gap can be minutes old.

import (
	"os"
	"time"
)

// What the walk knows about one media file against its probe record.
type fileIdentity struct {
	// The size the probe record holds, and the size on the volume, which the
	// walk logs for a replaced file.
	recorded int64
	found    int64
	// The file on the volume is not the file the probe read.
	sizeChanged bool
	// The second before which an attempt of a file fact, a thumbnail, or a
	// tile directory belongs to an earlier file at this path, and zero where
	// no earlier file is known.
	earlier int64
	// The same second for the probe's own attempt, which a new modified time
	// also moves: the probe reads a file again when its modified time changed,
	// and no other fact does.
	probeEarlier int64
}

// The identity of one file against its record. changed reads the file's
// change time, which is when the file took its current state. The walk calls
// it only for a file that differs from its record, so an unchanged library
// costs no stat beyond the one the walk makes of every file.
//
// A record of size zero holds no size to compare, so it opens nothing. The
// probe never writes one for a file with bytes in it.
func identityOf(record probedFile, size, modified int64, changed func() int64) fileIdentity {
	identity := fileIdentity{recorded: record.Size, found: size}
	if !record.Replaced.IsZero() {
		identity.earlier = record.Replaced.Unix()
	}
	if record.Size > 0 && record.Size != size {
		identity.sizeChanged = true
		identity.earlier = changed()
	}
	identity.probeEarlier = identity.earlier
	if !identity.sizeChanged && record.Modified != modified {
		identity.probeEarlier = max(identity.probeEarlier, changed())
	}
	return identity
}

// The second before which one fact's attempt at this file describes an
// earlier state of it.
func (i fileIdentity) cutoff(fact string) int64 {
	if fact == factProbe {
		return i.probeEarlier
	}
	return i.earlier
}

// The change time of a file, read when the walk or a phase needs it. A file
// the stat cannot read takes the present second, so every attempt made
// before now reads as one on an earlier file, and the fact asks again.
func lazyChangeTime(absolute string) func() int64 {
	return func() int64 {
		at, err := changeTime(absolute)
		if err != nil {
			return time.Now().Unix()
		}
		return at
	}
}

// The identities one folder read found, keyed by the file's path under the
// root.
type fileIdentities map[string]fileIdentity

// The identities of this result, made on the first file that has a record.
func (r *walkResult) fileIdentities() fileIdentities {
	if r.identities == nil {
		r.identities = fileIdentities{}
	}
	return r.identities
}

// The second before which an attempt or an output at one media file's path
// belongs to an earlier file, read the way the walk reads it, and zero where
// the path has held only this file or no probe has read it.
func earlierFileAt(kind, absolute string) (int64, error) {
	folder, entry := likenFolderFor(kind, absolute)
	ledger, err := readLikenLedger(folder, factProbe)
	if err != nil {
		return 0, err
	}
	record, held := ledger.probeAt(entry)
	if !held {
		return 0, nil
	}
	size, modified, err := statFile(absolute)
	if err != nil {
		return 0, err
	}
	return identityOf(record, size, modified, lazyChangeTime(absolute)).earlier, nil
}

// Whether a thumbnail or a tile directory on the volume was made before an
// earlier file left the path, which makes it that file's output. An output
// the stat cannot read is not one, and the caller's own read of the volume
// answers for it.
func madeBefore(path string, earlier int64) bool {
	if earlier == 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.ModTime().Unix() < earlier
}

// The probe record one path holds, or false where the ledger holds none.
func (l *likenLedger) probeAt(path string) (probedFile, bool) {
	for _, record := range l.Probes {
		if record.Path == path {
			return record, true
		}
	}
	return probedFile{}, false
}

// Whether the ledger's last attempt at one path came at or after a second.
func (l *likenLedger) attemptedSince(path string, second int64) bool {
	for _, attempt := range l.Attempts {
		if attempt.Path == path {
			return attempt.At.Unix() >= second
		}
	}
	return false
}

// A new record keeps the replacement time of the record it replaces, and
// takes the file's change time where the size differs, because that is when
// the new file took the path. A path with no earlier record has no earlier
// file to record.
func (p probedFile) replacing(held probedFile, found bool, changed func() int64) probedFile {
	if !found {
		return p
	}
	p.Replaced = held.Replaced
	if held.Size > 0 && held.Size != p.Size {
		p.Replaced = time.Unix(changed(), 0).UTC()
	}
	return p
}
