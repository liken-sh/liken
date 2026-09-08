package main

// jellyfinoutbound.go carries a Play of this cluster into Jellyfin. It joins
// the two messages a Play publishes, the sidecar's status on the media tree
// and the operator's audience on the library tree, and writes each person's
// position to Jellyfin.
// The join is in memory and it is lost on a restart. Both messages are
// retained, so a restart reads them back and the first tick writes again.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"
)

// How often a position that moved is written. A variable, so a test drives
// the loop in milliseconds.
var jellyfinWriteInterval = 10 * time.Second

// How near the end of a work counts as watched, in seconds. A person who
// stopped inside the last two minutes finished it.
const jellyfinPlayedWindow = 120

// What the role holds for one Play: what the status says, what the audience
// says, and where the last write left it.
type jellyfinPlay struct {
	item     int
	position int
	duration int
	phase    string
	people   []string
	aliases  map[string]string
	season   int
	episode  int
	written  int
	wrote    bool
	// Whether a status or a final has carried a position for this Play. A
	// Play whose audience arrived and whose position has not writes
	// nothing, because a write of the start would move a person's resume
	// point in Jellyfin back to the beginning.
	reported bool
}

// The outbound half of the role: the server it writes, the index it resolves
// a work and a person through, the positions it wrote, and the Plays it
// holds.
type jellyfinOutbound struct {
	api    *jellyfinAPI
	index  *jellyfinIndex
	echoes *jellyfinEchoes
	now    func() time.Time
	log    io.Writer

	mutex sync.Mutex
	plays map[string]*jellyfinPlay
}

func newJellyfinOutbound(api *jellyfinAPI, index *jellyfinIndex, now func() time.Time, log io.Writer) *jellyfinOutbound {
	return &jellyfinOutbound{
		api:    api,
		index:  index,
		echoes: newJellyfinEchoes(),
		now:    now,
		log:    log,
		plays:  map[string]*jellyfinPlay{},
	}
}

// status folds one report of where a Play reached. An empty payload is a
// cleared topic, and the Play is forgotten with it.
func (o *jellyfinOutbound) status(name string, payload []byte) {
	if len(payload) == 0 {
		o.forget(name)
		return
	}
	status := mediaPlayStatus{}
	if err := json.Unmarshal(payload, &status); err != nil {
		o.logf("the status of %s reads as no report: %v", name, err)
		return
	}
	position, duration := o.seconds(name, status.Position), o.seconds(name, status.Duration)

	o.mutex.Lock()
	defer o.mutex.Unlock()
	held := o.entry(name)
	held.item, held.position, held.duration = status.Item, position, duration
	held.reported = true
}

// audience folds what the operator knows about a Play: who watched and which
// work it is.
func (o *jellyfinOutbound) audience(name string, payload []byte) {
	if len(payload) == 0 {
		o.forget(name)
		return
	}
	audience := playAudience{}
	if err := json.Unmarshal(payload, &audience); err != nil {
		o.logf("the audience of %s reads as no audience: %v", name, err)
		return
	}

	o.mutex.Lock()
	defer o.mutex.Unlock()
	held := o.entry(name)
	held.people, held.aliases = audience.People, audience.Aliases
	held.season, held.episode = audience.Season, audience.Episode
}

// final folds the last status of a Play and writes at once, because the
// position a person resumes from is the one the Play ended on.
func (o *jellyfinOutbound) final(ctx context.Context, name string, payload []byte) {
	if len(payload) == 0 {
		o.forget(name)
		return
	}
	final := playFinal{}
	if err := json.Unmarshal(payload, &final); err != nil {
		o.logf("the final status of %s reads as no status: %v", name, err)
		return
	}
	position, duration := o.seconds(name, final.Position), o.seconds(name, final.Duration)

	o.mutex.Lock()
	held := o.entry(name)
	held.item, held.position, held.duration, held.phase = final.Item, position, duration, final.Phase
	held.reported = true
	o.mutex.Unlock()

	o.write(ctx, name, true)
}

// tick writes every Play whose position moved since its last write. A Play
// that stands still writes nothing, so a paused film is one message on the
// bus and no request to Jellyfin.
func (o *jellyfinOutbound) tick(ctx context.Context) {
	for _, name := range o.moved() {
		o.write(ctx, name, false)
	}
}

// The Plays a tick writes, in name order, so a pass makes its requests in one
// order.
func (o *jellyfinOutbound) moved() []string {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	names := []string{}
	for name, held := range o.plays {
		if held.reported && (!held.wrote || held.written != held.position) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// write puts one Play's position into Jellyfin, once per person. A Play
// nobody claimed writes nothing, because a user-data write names a user.
// A work or a person Jellyfin does not hold leaves a line in the pod log and
// no write. The index has already asked the server again by the time this
// answers.
func (o *jellyfinOutbound) write(ctx context.Context, name string, ended bool) {
	o.mutex.Lock()
	live, standing := o.plays[name]
	if !standing {
		o.mutex.Unlock()
		return
	}
	held := *live
	o.mutex.Unlock()

	if len(held.people) == 0 || !held.reported {
		return
	}
	item, found := o.index.itemFor(ctx, held.aliases, held.season, held.episode)
	if !found {
		o.logf("jellyfin holds no item for %s", name)
		return
	}
	data := jellyfinUserData{
		PlaybackPositionTicks: jellyfinTicks(held.position),
		Played:                jellyfinPlayed(held, ended),
		LastPlayedDate:        o.now().UTC().Format(time.RFC3339),
	}
	for _, person := range held.people {
		o.writeOne(ctx, person, item, held.position, data)
	}

	o.mutex.Lock()
	defer o.mutex.Unlock()
	live.written, live.wrote = held.position, true
}

// One person's write, and the position it leaves behind for the echo drop.
func (o *jellyfinOutbound) writeOne(ctx context.Context, person, item string, position int, data jellyfinUserData) {
	user, known := o.index.userFor(ctx, person)
	if !known {
		o.logf("jellyfin holds no user named %s", person)
		return
	}
	if err := o.api.writeUserData(ctx, item, user, data); err != nil {
		o.logf("could not write the progress of %s in jellyfin: %v", person, err)
		return
	}
	o.echoes.remember(user, item, position)
}

// Whether a Play that ended counts as watched. It is the end of a work that
// is within the last two minutes of it, or a Play that reached the end of a
// work it finished. A work of no duration is never watched, because a
// duration of zero says the report never carried one.
func jellyfinPlayed(held jellyfinPlay, ended bool) bool {
	if !ended || held.duration <= 0 {
		return false
	}
	if held.phase == playPhaseFinished && held.position >= held.duration {
		return true
	}
	return held.position >= held.duration-jellyfinPlayedWindow
}

// forget drops one Play. The operator clears a Play's topics once it releases
// the Play, and the clear is what says the join is over.
func (o *jellyfinOutbound) forget(name string) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	delete(o.plays, name)
}

// The Play one message belongs to, created on the first message of either
// topic. The caller holds the mutex.
func (o *jellyfinOutbound) entry(name string) *jellyfinPlay {
	held, standing := o.plays[name]
	if !standing {
		held = &jellyfinPlay{}
		o.plays[name] = held
	}
	return held
}

// One H:MM:SS value as seconds. A value of another shape records zero and
// leaves a line, the rule the progress role follows.
func (o *jellyfinOutbound) seconds(name, value string) int {
	seconds, ok := parsePosition(value)
	if !ok {
		o.logf("the position %q of %s reads as no time", value, name)
	}
	return seconds
}

func (o *jellyfinOutbound) logf(format string, args ...any) {
	if o.log == nil {
		return
	}
	fmt.Fprintf(o.log, "library.liken.sh: "+format+"\n", args...)
}

// The positions this role last wrote, one per user and item. A webhook that
// carries a position this role wrote a moment ago is that write coming back,
// and the drop is what keeps it out of the store.
type jellyfinEchoes struct {
	mutex   sync.Mutex
	written map[string]int
}

// How near a written position an inbound position must be to read as the echo
// of it, in seconds.
const jellyfinEchoWindow = 1

func newJellyfinEchoes() *jellyfinEchoes {
	return &jellyfinEchoes{written: map[string]int{}}
}

func jellyfinEchoKey(user, item string) string {
	return user + "/" + item
}

func (e *jellyfinEchoes) remember(user, item string, position int) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.written[jellyfinEchoKey(user, item)] = position
}

// Whether this user, item, and position is the last write of this role coming
// back.
func (e *jellyfinEchoes) echoed(user, item string, position int) bool {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	written, held := e.written[jellyfinEchoKey(user, item)]
	if !held {
		return false
	}
	gap := written - position
	if gap < 0 {
		gap = -gap
	}
	return gap <= jellyfinEchoWindow
}
