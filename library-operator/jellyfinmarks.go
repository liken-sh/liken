package main

// jellyfinmarks.go sends the marks a person sets at the media browser to
// Jellyfin. A mark arrives retained on the bus, the same message the
// progress role records, and the jellyfin role writes it to each person's
// user data: watched as played at the end of the work, cleared as unplayed
// at the start. A mark that lists several episodes, which Pick up here
// publishes, is one write for each episode, and one sent record for the
// whole list.
//
// The broker delivers each mark again on every subscription until the
// progress role clears it, a day after the press. A second send of an old
// mark would undo a toggle a person made in Jellyfin since. Two checks
// stop it:
//
//   - After it sends a mark, the role publishes a retained record of the
//     send, and a mark with a record sends nothing. The record is what a
//     restarted role reads back. Jellyfin's last played date cannot do
//     that job alone, because an unplayed toggle in Jellyfin clears it.
//   - Before it writes, the role reads the person's last played date in
//     the item. A date at or after the press is a newer play or toggle in
//     Jellyfin, or this mark's own write, and the role leaves it.
//
// The role sends marks on the write loop's pass and never as they arrive.
// On a new subscription the broker delivers the marks and the records in
// the order of the two subscriptions, and a pass later every record has
// arrived beside its mark.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
)

// The record of one sent mark: the time of the press, so a record whose
// mark is past its retention can be cleared on sight.
type jellyfinMarkSent struct {
	At int64 `json:"at"`
}

// The marks half of the role: the marks that arrived and are not sent
// yet, and the names of the marks whose record stands.
type jellyfinMarks struct {
	out       *jellyfinOutbound
	topicBase string
	namespace string
	publish   func(topic string, payload []byte, retained bool)
	now       func() time.Time
	log       io.Writer

	mutex   sync.Mutex
	pending map[string]titleMark
	sent    map[string]bool
}

func newJellyfinMarks(out *jellyfinOutbound, topicBase, namespace string,
	publish func(string, []byte, bool), now func() time.Time, log io.Writer) *jellyfinMarks {
	return &jellyfinMarks{
		out:       out,
		topicBase: topicBase,
		namespace: namespace,
		publish:   publish,
		now:       now,
		log:       log,
		pending:   map[string]titleMark{},
		sent:      map[string]bool{},
	}
}

// receive holds one mark for the next pass. An empty payload is the
// progress role's clear of the mark, and the role clears its record with
// it. A payload that is no mark is left to the progress role, which
// clears it.
func (m *jellyfinMarks) receive(name string, payload []byte) {
	if len(payload) == 0 {
		m.forget(name)
		return
	}
	mark := titleMark{}
	if err := json.Unmarshal(payload, &mark); err != nil || (mark.Mark != markWatched && mark.Mark != markCleared) {
		m.logf("the mark %s reads as no mark, and the role sends nothing for it", name)
		return
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()
	if !m.sent[name] {
		m.pending[name] = mark
	}
}

// receiveSent reads one record back. A record past its mark's retention is
// cleared, because the mark's own clear may have arrived while the role was
// down, and nothing else clears it.
func (m *jellyfinMarks) receiveSent(name string, payload []byte) {
	if len(payload) == 0 {
		m.mutex.Lock()
		delete(m.sent, name)
		m.mutex.Unlock()
		return
	}
	record := jellyfinMarkSent{}
	if err := json.Unmarshal(payload, &record); err != nil || markClearIn(record.At, m.now()) == 0 {
		m.publish(playMarkSentTopic(m.topicBase, m.namespace, name), nil, true)
		return
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.sent[name] = true
	delete(m.pending, name)
}

// forget drops a mark the progress role cleared, and clears the record of
// it where one stands.
func (m *jellyfinMarks) forget(name string) {
	m.mutex.Lock()
	delete(m.pending, name)
	recorded := m.sent[name]
	delete(m.sent, name)
	m.mutex.Unlock()

	if recorded {
		m.publish(playMarkSentTopic(m.topicBase, m.namespace, name), nil, true)
	}
}

// push sends every mark that waits, in name order. A mark past its
// retention is dropped unsent. A mark whose write failed waits for the next
// pass, and a mark that landed leaves its record.
func (m *jellyfinMarks) push(ctx context.Context) {
	m.mutex.Lock()
	names := slices.Sorted(maps.Keys(m.pending))
	marks := make([]titleMark, len(names))
	for at, name := range names {
		marks[at] = m.pending[name]
	}
	m.mutex.Unlock()

	for at, name := range names {
		mark := marks[at]
		if markClearIn(mark.At, m.now()) == 0 {
			m.logf("the mark %s was pressed more than %s ago, and the role sends nothing for it", name, markRetention)
			m.drop(name)
			continue
		}
		if !m.send(ctx, name, mark) {
			continue
		}
		payload, _ := json.Marshal(jellyfinMarkSent{At: mark.At})
		m.publish(playMarkSentTopic(m.topicBase, m.namespace, name), payload, true)
		m.mutex.Lock()
		m.sent[name] = true
		delete(m.pending, name)
		m.mutex.Unlock()
	}
}

func (m *jellyfinMarks) drop(name string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	delete(m.pending, name)
}

// send writes one mark to each person at the screen, and answers whether
// the mark is done. A mark on one work is one item, and a list is one item
// for each episode. A person or an episode Jellyfin does not hold is done,
// because a later pass would find the same. A read or a write that failed
// is not, and the next pass sends the whole mark again; an item whose
// write landed then reads the mark's own date back and is left alone.
func (m *jellyfinMarks) send(ctx context.Context, name string, mark titleMark) bool {
	if len(mark.People) == 0 {
		m.logf("the mark %s names nobody, and jellyfin keeps progress by person", name)
		return true
	}
	targets, missing := m.items(ctx, mark.Aliases, mark.works(name))
	work := workNamed(mark.Aliases, mark.Season, mark.Episode)
	if len(targets) == 0 {
		m.logf("jellyfin holds no item for the mark %s of %s", name, work)
		return true
	}

	// A list is one press, so it leaves one line for each person with the
	// counts, and not a line for each of its episodes. A failure still
	// leaves its own line, with Jellyfin's answer in it.
	listed := len(mark.Episodes) > 0
	done := true
	for _, person := range mark.People {
		user, known := m.out.index.userFor(ctx, person)
		if !known {
			m.logf("jellyfin holds no user named %s", person)
			continue
		}
		tally := jellyfinMarkTally{missing: missing}
		for _, target := range targets {
			if !m.write(ctx, name, mark, jellyfinMarkWrite{person: person, user: user, target: target, quiet: listed}, &tally) {
				done = false
			}
		}
		if listed {
			m.logf("wrote the mark %s on %d episodes of %s to jellyfin for person %s: "+
				"%d written, %d already played, %d played since the press, %d not held",
				name, len(mark.Episodes), work, person, tally.written, tally.played, tally.since, tally.missing)
		}
	}
	return done
}

// One item a mark writes: Jellyfin's id for it, and the row of the mark
// that names it.
type jellyfinMarkTarget struct {
	item string
	work markedWork
}

// items resolves each work of a mark to its Jellyfin item, and counts the
// works Jellyfin does not hold.
func (m *jellyfinMarks) items(ctx context.Context, aliases map[string]string, works []markedWork) ([]jellyfinMarkTarget, int) {
	targets := []jellyfinMarkTarget{}
	missing := 0
	for _, work := range works {
		item, found := m.out.index.itemFor(ctx, aliases, work.season, work.episode)
		if !found {
			missing++
			continue
		}
		targets = append(targets, jellyfinMarkTarget{item: item, work: work})
	}
	return targets, missing
}

// One write of a mark: the person, their Jellyfin user, the item, and
// whether the write leaves its line to the list's count.
type jellyfinMarkWrite struct {
	person string
	user   string
	target jellyfinMarkTarget
	quiet  bool
}

// What a list did for one person, for the one line it leaves.
type jellyfinMarkTally struct {
	written, played, since, missing int
}

// write sends one item of a mark for one person, and answers false only
// where a read or a write failed.
//
// Before it writes, the role reads what Jellyfin holds. A last played date
// at or after the press is a newer play or toggle in Jellyfin, or this
// mark's own write, and the role leaves it. An item played with no resume
// position is already what a watched mark would write, less the date, so
// the role leaves that too, and a person who watched the earlier seasons
// of a series in Jellyfin costs a list no writes.
func (m *jellyfinMarks) write(ctx context.Context, name string, mark titleMark, to jellyfinMarkWrite, tally *jellyfinMarkTally) bool {
	work := to.target.work
	named := workNamed(mark.Aliases, work.season, work.episode)
	item := to.target.item
	held, err := m.out.api.userData(ctx, item, to.user)
	if err != nil {
		m.logf("could not read the progress of %s in jellyfin before the mark %s: %v", to.person, name, err)
		return false
	}
	if jellyfinPlayedSince(held.LastPlayedDate, mark.At) {
		tally.since++
		m.quietf(to.quiet, "jellyfin holds a play of %s by person %s at %s, at or after the mark %s, and the role leaves it",
			named, to.person, strings.TrimSpace(held.LastPlayedDate), name)
		return true
	}
	if mark.Mark == markWatched && held.Played && held.PlaybackPositionTicks == 0 {
		tally.played++
		m.quietf(to.quiet, "jellyfin holds %s as played by person %s, and the mark %s changes nothing there",
			named, to.person, name)
		return true
	}

	played := mark.Mark == markWatched
	pressed := time.Unix(mark.At, 0).UTC()
	data := jellyfinUserDataUpdate{
		PlaybackPositionTicks: jellyfinTicks(work.position),
		Played:                &played,
		// The date of the press, and not of the write, so the mark's own
		// write reads back as no newer than the mark.
		LastPlayedDate: pressed.Format(time.RFC3339),
	}
	if err := m.out.api.writeUserData(ctx, item, to.user, data); err != nil {
		m.logf("could not write the mark %s of %s for %s in jellyfin: %v", name, named, to.person, err)
		return false
	}
	m.out.echoes.remember(to.user, item, work.position)
	tally.written++
	m.quietf(to.quiet, "wrote the mark %s of %s to jellyfin for person %s: item %s, user %s, %s at %s, pressed at %s",
		name, named, to.person, item, to.user, mark.Mark, positionText(work.position), pressed.Format(time.RFC3339))
	return true
}

// Whether Jellyfin's last played date is at or after the press. An item
// nobody played carries no date, and a date of another shape reads as none,
// so the mark is sent.
func jellyfinPlayedSince(date string, at int64) bool {
	played, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(date))
	if err != nil {
		return false
	}
	return played.Unix() >= at
}

// quietf writes one line unless the write belongs to a list, which leaves
// one line of counts in its place.
func (m *jellyfinMarks) quietf(quiet bool, format string, args ...any) {
	if !quiet {
		m.logf(format, args...)
	}
}

func (m *jellyfinMarks) logf(format string, args ...any) {
	if m.log == nil {
		return
	}
	fmt.Fprintf(m.log, "library.liken.sh: "+format+"\n", args...)
}
