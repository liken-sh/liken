package main

// The operator's loop: level-triggered, woken by the Receiver watch and
// by discovery, with a ticker as the backstop for the failures no event
// follows. A pass reads the whole collection, so a restarted operator
// starts correct.
// One client per Receiver holds the receiver's connection for as long
// as the Receiver stands. A debounced writer folds the burst of lines
// that follows one change into a single status write.

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
	"github.com/liken-sh/equipment-operator/wiim"
)

// How often the loop reconciles with nothing to prompt it. It is a
// backstop, and no state reaches the loop through it alone: a change to
// a Receiver's spec or status wakes the loop through the watch, and a
// discovered address wakes it through discovery. The tick covers the
// failures that no event follows: a list the API server refused, a
// Television's status.session write it refused, a declared setting
// whose send failed, and a setting the receiver took and still reports
// at another value, which the send budget allows a few more sends. A
// tick reads the Receivers from the watch's store, so on a settled
// cluster it sends the API server no read of them, and it sends nothing
// to a receiver that already matches its spec. The pass lists the
// Receivers from the API server only while the store has nothing to
// give (objectcache.go).
const backstopInterval = 30 * time.Second

// How long a burst of lines is collected before one status write.
const statusDebounce = 250 * time.Millisecond

// receiverUnit is one Receiver's running parts: the connection, the
// status writer, the settings and zones it drives, the session a Player
// holds, and the asks it applies.
type receiverUnit struct {
	// ctx is the unit's own context. Each goroutine the unit starts runs
	// under it, and is counted in the group it carries (work.go).
	ctx         context.Context
	name        string
	address     string
	client      *Client
	dial        dialFunc
	now         func() time.Time
	driver      equipment.Driver
	denonClient *denon.Client
	wiimClient  *wiim.Client
	// foreign tells discovery which device reports a project that is
	// not a WiiM. It is nil in a test that builds a unit alone.
	foreign    func(uuid, project, address string)
	readings   *metrics
	log        *receiverLog
	cancel     context.CancelFunc
	dirty      chan struct{}
	generation atomic.Int64
	// The declared inputs live here and not on the session, so an edit to
	// them reaches a standing session with no restart: the sound mode an
	// input names is read when the session selects it.
	inputs atomic.Pointer[[]ReceiverInput]
	// The last power the operator settled, so a reconcile and a toggle
	// share one memory of what was sent and neither re-asserts it.
	power atomic.Pointer[equipment.Power]
	// The last settings the operator applied. A field the receiver does
	// not report is compared with it, so the field is sent once per spec
	// change.
	settings atomic.Pointer[denon.Settings]
	// The same memory for a WiiM, which has its own settings type and no
	// shared field with the Denon's.
	wiimSettings atomic.Pointer[wiim.Settings]
	// The last zone controls the operator applied, keyed by zone, for
	// the controls a zone does not report, the same way as settings.
	zones atomic.Pointer[map[string]ZoneSpec]
	// How many times the operator sent each declared field at its
	// declared value, so a field the receiver never confirms stops.
	budget *sendBudget
	// The digest of each declared block the operator has sent, read from
	// status.settledSettings when the unit starts and written back with
	// the status.
	settled *settledRecord

	// seen holds the at of each ask the unit has read, and volumeAsks
	// holds the volume ask the receiver has not been sent yet
	// (session_asks.go).
	seen       seenAsks
	volumeAsks *volumeAsker

	mutex   sync.Mutex
	session *session
	// sessions writes the status.session of the TV the session shows,
	// and is nil in a test that builds a unit with no TV.
	sessions *televisionSessions
	applied  ReceiverStatus
	written  bool
}

// observe is where every line the receiver sends reaches the operator.
// It wakes the status writer and the volume ask that waits for a
// report, and it reaches the session.
func (u *receiverUnit) observe(event equipment.Event) {
	poke(u.dirty)
	u.log.observe()
	if u.volumeAsks != nil && (event.Field == equipment.EventVolume || event.Field == equipment.EventMute) {
		poke(u.volumeAsks.reported)
	}
	u.mutex.Lock()
	held := u.session
	u.mutex.Unlock()
	if held != nil {
		held.observe(event)
	}
}

// report writes the status a burst of lines settles on, one write per
// burst, and only when the write would change something.
func (u *receiverUnit) report(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-u.dirty:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(statusDebounce):
		}
		drainPokes(u.dirty)
		// A select answers a ready timer as readily as a ready context, so a
		// unit stopped inside the debounce is asked again here before it
		// writes.
		if ctx.Err() != nil {
			return
		}
		u.write()
	}
}

func (u *receiverUnit) write() {
	now := u.now()
	state := u.driver.State()
	// Metrics are read from the same state and the same moment that
	// build the status below, and set whether or not the status itself
	// turns out to have changed: observation_last_success_timestamp_seconds
	// advances on every settled burst, not only on a burst that changed
	// what a person reads in status.
	u.readings.recordObservation(u.name, state, u.driver.VolumeResolution(), now)

	settings := u.denonSettings()
	status := buildReceiverStatus(state, settings, u.wiimStatus(), u.driver.Address(), u.driver.VolumeResolution(), u.generation.Load(), u.applied.Conditions, now)
	status.SettledSettings = u.settled.snapshot()
	status.SettledPower = u.powerApplied()
	if condition, held := inputSelected(u.sessionInput(), state, u.generation.Load(), u.applied.Conditions, now); held {
		status.Conditions = append(status.Conditions, condition)
	}
	if condition, held := settingsConfirmed(u.budget.unconfirmed(), u.generation.Load(), u.applied.Conditions, now); held {
		status.Conditions = append(status.Conditions, condition)
	}
	if u.written && sameStatus(status, u.applied) {
		return
	}
	if _, err := ApplyReceiverStatus(u.client, u.name, status); err != nil {
		fmt.Fprintf(os.Stderr, "writing the status of receiver %s: %v\n", u.name, err)
		return
	}
	u.applied, u.written = status, true
}

// setSession starts, flips, replaces, or lifts the session. A session
// that has not changed is left alone, because power and input are one-
// shots the receiver answers once.
//
// A flip of either flag is not a change of session: it reaches the
// session that stands.
//
// adopted is empty for a session the operator sees appear while it
// runs. Otherwise it names why the session was already standing: the
// operator found it in its first pass, or the unit a new wiring
// replaced handed it over. Such a session adopts its flags and sends
// the receiver nothing for them, and the line ends with adopted.
func (u *receiverUnit) setSession(ctx context.Context, spec *ReceiverSession, adopted string) {
	u.mutex.Lock()
	held := u.session
	u.mutex.Unlock()
	// A session that starts or ends changes the InputSelected condition,
	// which the status writer derives from the session that stands.
	defer poke(u.dirty)

	if held != nil && spec != nil && held.spec == spec.withoutFlags() {
		if flips := flagFlips(held, spec); flips != "" {
			u.log.printf("the session for Player %s: %s", spec.Player, flips)
		}
		held.setFlags(spec.Active, spec.Awake)
		return
	}
	if held != nil {
		u.mutex.Lock()
		u.session = nil
		u.mutex.Unlock()
		held.stop()
		u.log.printf("the session for Player %s ended", held.spec.Player)
		u.sessions.lift(held.spec.Player)
	}
	if spec == nil {
		return
	}
	adopting := adopted != ""
	if adopting {
		adopted = "; " + adopted + ", so it sends nothing for these flags"
	}
	u.log.printf("a session for Player %s started: input %s, active %t, awake %t%s",
		spec.Player, spec.Input, spec.Active, spec.Awake, adopted)
	started := newSession(ctx, u.name, *spec, u.driver, u.readings, u.log, u.inputSoundMode, u.applyPower, u.roomFor(spec))
	u.mutex.Lock()
	u.session = started
	u.mutex.Unlock()
	started.start(spec.Active, spec.Awake, adopting)
}

// flagFlips names each flag of a standing session that the new spec
// changes, and answers an empty string when it changes none.
func flagFlips(held *session, spec *ReceiverSession) string {
	var flips []string
	if was := held.active.Load(); was != spec.Active {
		flips = append(flips, fmt.Sprintf("active went from %t to %t", was, spec.Active))
	}
	if was := held.awake.Load(); was != spec.Awake {
		flips = append(flips, fmt.Sprintf("awake went from %t to %t", was, spec.Awake))
	}
	return strings.Join(flips, ", ")
}

// setInputs records the declared inputs and the sound mode each names,
// which a later input selection reads.
func (u *receiverUnit) setInputs(inputs []ReceiverInput) {
	held := slices.Clone(inputs)
	u.inputs.Store(&held)
}

// roomFor answers the link a session uses to tell its TV what it did,
// and nil for a unit that writes no TV session.
func (u *receiverUnit) roomFor(spec *ReceiverSession) roomEvents {
	if u.sessions == nil {
		return nil
	}
	return u.sessions.room(u.log, spec.Player, spec.Input, u.inputMonitor)
}

// inputMonitor answers the monitor one declared input names, which is
// the Display whose picture the input carries, and an empty string when
// the input is not declared.
func (u *receiverUnit) inputMonitor(input string) string {
	held := u.inputs.Load()
	if held == nil {
		return ""
	}
	for _, one := range *held {
		if one.Name == input {
			return one.Monitor
		}
	}
	return ""
}

// inputSoundMode answers the sound mode one declared input names, and
// an empty string when it names none.
func (u *receiverUnit) inputSoundMode(input string) string {
	held := u.inputs.Load()
	if held == nil {
		return ""
	}
	for _, one := range *held {
		if one.Name == input {
			return one.SoundMode
		}
	}
	return ""
}

// setPower drives the receiver to the power a person declared. The
// operator owns this field and acts only on a change of it: a
// spec.power the operator already settled is not sent again, so a
// person who turns the receiver on or off by hand is not overruled, and
// a generation that changes another field, such as a session flag,
// sends nothing. The value the operator finds in its first pass is
// adopted, unless status.settledPower says it changed while the
// operator was down, in resumePower. A Receiver created while the
// operator runs has no settled value, so its spec.power is a change. A
// spec.power the operator has not settled waits for the survey,
// and then goes out only when the receiver reports another power. A
// command sent before the connection is open is dropped, so the change
// waits for a reachable receiver. A Denon cannot tell Standby from Off,
// so both mean Standby, and a WiiM has no standby, so it always reports
// On. An empty value is no declarative intent, and the receiver is left
// where it is.
func (u *receiverUnit) setPower(power equipment.Power) {
	if power == "" || power == u.powerApplied() {
		return
	}
	if !u.driver.Surveyed() || u.driver.State().Reachable != equipment.ConditionTrue {
		return
	}
	asks := fmt.Sprintf("generation %d asks power %s", u.generation.Load(), power)
	on := power == equipment.PowerOn
	if observed := mainZone(u.driver.State()); on == (observed.Power == equipment.PowerOn) {
		u.log.printf("%s; sent nothing, because the receiver reports %s", asks, powerWords(observed, 0))
		u.applyPower(power)
		return
	}
	sent := powerWords(equipment.ZoneState{Power: commandedPower(on)}, 0)
	line := fmt.Sprintf("%s; sent %s", asks, sent)
	began := time.Now()
	if err := u.driver.SetPower(equipment.MainZone, on); err != nil {
		u.log.refused(line, err)
		return
	}
	u.log.confirm(line, began, mainZoneCheck(u.driver, sent, powerWords))
	u.applyPower(power)
}

// resumePower settles the spec.power the operator finds in its first
// pass. A spec.power that differs from status.settledPower is a change
// no operator settled, such as an edit while the operator was down, so
// the unit keeps the settled value and setPower compares the new one
// with the receiver. Any other value is adopted.
func (u *receiverUnit) resumePower(stored ReceiverStoredStatus, power equipment.Power) {
	if settled := stored.SettledPower; settled != "" && power != "" && settled != power {
		u.power.Store(&settled)
		return
	}
	u.adoptPower(power)
}

// adoptPower takes the spec.power the operator finds in its first pass
// as settled, and sends nothing. A value the operator merely finds may
// be stale: the session's toggle writes spec.power, and a Play or a
// person can turn the receiver on after it. So a restart at the settled
// value adopts, and so does an upgrade, which finds no
// status.settledPower.
func (u *receiverUnit) adoptPower(power equipment.Power) {
	u.power.Store(&power)
	poke(u.dirty)
	if power != "" {
		u.log.printf("generation %d asks power %s; the operator found it when it started, so it sent nothing", u.generation.Load(), power)
	}
}

// carryPower gives a unit that replaces another for a new address the
// spec.power the old one settled, because a new wiring is no change of
// spec.power.
func (u *receiverUnit) carryPower(old *receiverUnit) {
	if held := old.power.Load(); held != nil {
		u.power.Store(held)
	}
}

// denonSettings answers the last settings a Denon reported, and nil for
// a receiver another protocol drives.
func (u *receiverUnit) denonSettings() *denon.Settings {
	if u.denonClient == nil {
		return nil
	}
	settings := u.denonClient.Settings()
	return &settings
}

// wiimStatus answers the last snapshot a WiiM reported, and nil for a
// receiver another protocol drives.
func (u *receiverUnit) wiimStatus() *wiim.Status {
	if u.wiimClient == nil {
		return nil
	}
	status := u.wiimClient.Status()
	return &status
}

// setSettings drives the receiver to the settings a person declared.
// It sends only the declared fields that Pending keeps: a field the
// receiver reports at another value, and a field the receiver does not
// report that the spec changed since the last apply. A restart against
// a receiver that already holds the declared settings sends nothing
// and logs nothing, and a change made at the receiver is sent back on
// the next pass. A field the receiver reports is sent on each pass
// until the receiver reports the declared value. A field it does not
// report is sent once per change of the block. After a restart it is
// sent only when the block differs from the one status.settledSettings
// records, which unrecorded explains. The apply waits for the survey,
// because before it the receiver has reported nothing to compare with.
// An apply error is logged and the spec is not recorded as applied, so
// the next pass tries again. It answers whether the pass settled the
// block, which is what lets the caller record the generation.
func (u *receiverUnit) setSettings(want denon.Settings) bool {
	if u.denonClient == nil {
		return false
	}
	if !u.driver.Surveyed() || u.driver.State().Reachable != equipment.ConditionTrue {
		return false
	}
	previous, known := u.settingsApplied()
	known = known || u.unrecorded(denonSettingsBlock, want)
	pending := budgeted(u, denonSettingsBlock, want.Pending(u.denonClient.Settings(), previous, known))
	if !reflect.DeepEqual(pending, denon.Settings{}) {
		line := fmt.Sprintf("generation %d declares spec.denon.settings %s; sent it", u.generation.Load(), declared(pending))
		began := time.Now()
		if err := u.denonClient.ApplySettings(pending); err != nil {
			u.log.refused(line, err)
			return false
		}
		if u.log.fresh("spec.denon.settings", pending) {
			u.log.confirm(line, began, settingsCheck(func() bool { return pending.ConfirmedBy(u.denonClient.Settings()) }))
		}
	}
	u.settings.Store(&want)
	return true
}

// unrecorded answers whether a declared block differs from the one
// status.settledSettings records. The operator starts with no memory of
// the fields it applied, so after a restart it compares an unreported
// field with nothing. When the record holds the block, the operator
// sent those fields before it restarted, and it sends nothing. When it
// differs, the Receiver is new or the block changed while the operator
// was down, and the operator sends every unreported field of the block
// once.
func (u *receiverUnit) unrecorded(path string, block any) bool {
	return !u.settled.holds(path, block)
}

// recordSettled records that the operator has sent a declared block,
// and asks for a status write when status.settledSettings changes.
func (u *receiverUnit) recordSettled(path string, block any) {
	if u.settled.settle(path, block) {
		poke(u.dirty)
	}
}

// budgeted spends the unit's send budget on one block's pending fields
// and answers the fields it may send. When the fields the budget holds
// back change, it asks for a status write, so the SettingsConfirmed
// condition follows.
func budgeted[T any](u *receiverUnit, family string, pending T) T {
	before := u.budget.unconfirmed()
	kept := spend(u.budget, family, pending)
	if !slices.Equal(before, u.budget.unconfirmed()) {
		poke(u.dirty)
	}
	return kept
}

// settingsApplied answers the last settings the operator settled on,
// and whether it has settled on any since it started.
func (u *receiverUnit) settingsApplied() (denon.Settings, bool) {
	if held := u.settings.Load(); held != nil {
		return *held, true
	}
	return denon.Settings{}, false
}

// setWiimSettings drives the device to the settings a person declared.
// It is the Denon path in WiiM's own terms: it sends only the declared
// fields that Pending keeps, so a restart against a device that already
// holds them sends nothing, and a change made at the device is sent
// back. The two settings types never meet.
func (u *receiverUnit) setWiimSettings(want wiim.Settings) bool {
	if !u.driver.Surveyed() || u.driver.State().Reachable != equipment.ConditionTrue {
		return false
	}
	previous, known := u.wiimSettingsApplied()
	known = known || u.unrecorded(wiimSettingsBlock, want)
	pending := budgeted(u, wiimSettingsBlock, want.Pending(u.wiimClient.Settings(), previous, known))
	if !reflect.DeepEqual(pending, wiim.Settings{}) {
		line := fmt.Sprintf("generation %d declares spec.wiim.settings %s; sent it", u.generation.Load(), declared(pending))
		began := time.Now()
		if err := u.wiimClient.ApplySettings(pending); err != nil {
			u.log.refused(line, err)
			return false
		}
		if u.log.fresh("spec.wiim.settings", pending) {
			u.log.confirm(line, began, settingsCheck(func() bool { return pending.ConfirmedBy(u.wiimClient.Settings()) }))
		}
	}
	u.wiimSettings.Store(&want)
	return true
}

// wiimSettingsApplied answers the last WiiM settings the operator
// settled on, and whether it has settled on any since it started.
func (u *receiverUnit) wiimSettingsApplied() (wiim.Settings, bool) {
	if held := u.wiimSettings.Load(); held != nil {
		return *held, true
	}
	return wiim.Settings{}, false
}

// setZones drives the non-main zones to the controls a person
// declared. For each zone it sends only the controls that
// ZoneSpec.Pending keeps: a control the zone reports at another value,
// and a control the zone does not report that the spec changed since
// the last apply. A restart against a receiver whose zones already
// hold the declared controls sends nothing and logs nothing, and a
// change made at the receiver is sent back on the next pass. A control
// the zone does not report is sent once per change of spec.zones, and
// after a restart only when spec.zones differs from the block
// status.settledSettings records. The main zone is spec.power and the
// session, so a zones map that names main is a misconfiguration and is
// rejected rather than fought. An apply error is logged and the zone is
// not recorded as applied, so the next pass tries again.
func (u *receiverUnit) setZones(want map[string]ZoneSpec) bool {
	if !u.driver.Surveyed() || u.driver.State().Reachable != equipment.ConditionTrue {
		return false
	}
	// zonesApplied returns a copy, so this loop writes a fresh map that
	// never shares its backing with the snapshot the last pass stored; a
	// later setZones cannot reach back into an earlier one.
	applied, known := u.zonesApplied()
	known = known || u.unrecorded(zonesBlock, want)
	settled := true
	state := u.driver.State()
	resolution := u.driver.VolumeResolution()
	for name, spec := range want {
		if name == equipment.MainZone {
			fmt.Fprintf(os.Stderr, "zone %q on receiver %s: the main zone is spec.power and the session, not spec.zones\n", name, u.name)
			continue
		}
		observed, reported := state.Zone(name)
		pending := budgeted(u, "spec.zones."+name, spec.Pending(observed, reported, applied[name], known, resolution))
		if pending != (ZoneSpec{}) {
			line := fmt.Sprintf("generation %d declares zone %s %s; sent it", u.generation.Load(), name, declared(pending))
			began := time.Now()
			if err := u.applyZone(name, pending); err != nil {
				u.log.refused(line, err)
				settled = false
				continue
			}
			if u.log.fresh("spec.zones."+name, pending) {
				u.log.confirm(line, began, settingsCheck(func() bool {
					observed, reported := u.driver.State().Zone(name)
					return !reported || pending.ConfirmedBy(observed, resolution)
				}))
			}
		}
		applied[name] = spec
	}
	u.zones.Store(&applied)
	return settled
}

// zonesApplied answers the last zone controls the operator settled on,
// as a copy, so a caller can never mutate the stored snapshot.
func (u *receiverUnit) zonesApplied() (map[string]ZoneSpec, bool) {
	if held := u.zones.Load(); held != nil {
		copy := make(map[string]ZoneSpec, len(*held))
		for name, spec := range *held {
			copy[name] = spec
		}
		return copy, true
	}
	return map[string]ZoneSpec{}, false
}

// applyZone sends the declared controls for one zone. Volume is in
// display units, so it is turned into the driver's smallest steps
// before it is sent. An unknown zone is an error the caller logs.
func (u *receiverUnit) applyZone(name string, spec ZoneSpec) error {
	if spec.Power != "" {
		if err := u.driver.SetPower(name, spec.Power != equipment.PowerStandby && spec.Power != equipment.PowerOff); err != nil {
			return fmt.Errorf("power: %w", err)
		}
	}
	if spec.Input != "" {
		if err := u.driver.SetInput(name, spec.Input); err != nil {
			return fmt.Errorf("input: %w", err)
		}
	}
	if spec.Volume != nil {
		steps := int(math.Round(*spec.Volume * float64(u.driver.VolumeResolution())))
		if err := u.driver.SetVolume(name, steps); err != nil {
			return fmt.Errorf("volume: %w", err)
		}
	}
	if spec.Mute != nil {
		if err := u.driver.SetMute(name, *spec.Mute); err != nil {
			return fmt.Errorf("mute: %w", err)
		}
	}
	if spec.Sleep != nil {
		if err := u.driver.SetSleep(name, *spec.Sleep); err != nil {
			return fmt.Errorf("sleep: %w", err)
		}
	}
	return nil
}

// settingsCheck answers a check for a declared block, which confirmed
// reads the way ConfirmedBy does: no value the receiver reports differs
// from the block.
func settingsCheck(confirmed func() bool) func() (string, bool) {
	return func() (string, bool) {
		if confirmed() {
			return "no value that differs", true
		}
		return "a value that differs", false
	}
}

// powerApplied answers the last power the operator settled on.
func (u *receiverUnit) powerApplied() equipment.Power {
	if held := u.power.Load(); held != nil {
		return *held
	}
	return ""
}

// applyPower records the power the receiver now stands at and writes it
// to the spec, so the next reconcile sees no change to re-assert, and
// asks for a status write so status.settledPower follows. The session
// calls it after a toggle settles; setPower calls it after a
// declarative change, by a command or because the receiver already
// reported the value.
func (u *receiverUnit) applyPower(power equipment.Power) {
	if _, err := ApplyReceiverPower(u.client, u.name, power); err != nil {
		fmt.Fprintf(os.Stderr, "writing the power of receiver %s: %v\n", u.name, err)
		return
	}
	u.power.Store(&power)
	poke(u.dirty)
}

// standingSession answers the session that stands, with the flags it
// holds now, and nil when none stands.
func (u *receiverUnit) standingSession() *ReceiverSession {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.session == nil {
		return nil
	}
	spec := u.session.spec
	spec.Active, spec.Awake = u.session.active.Load(), u.session.awake.Load()
	return &spec
}

// player names the Player whose session stands, and an empty string
// when none stands.
func (u *receiverUnit) player() string {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.session == nil {
		return ""
	}
	return u.session.spec.Player
}

// stop ends the session and closes the connection, which is what a
// deleted Receiver, a new wiring, and an operator shutdown leave
// behind. The metrics scoped to this receiver go with it, so a Receiver
// that is gone stops being reported.
func (u *receiverUnit) stop() {
	u.mutex.Lock()
	held := u.session
	u.session = nil
	u.mutex.Unlock()
	if held != nil {
		held.stop()
	}
	u.cancel()
	u.readings.forgetReceiver(u.name)
}

// controller holds what every pass needs and the units it runs.
type controller struct {
	client *Client
	// dial reaches each Denon receiver.
	dial      dialFunc
	wake      chan struct{}
	now       func() time.Time
	readings  *metrics
	discovery *discovery
	// networkDiscoveryOff keeps discovery from running at all, so the
	// operator sends no search onto the LAN and creates no Receiver
	// (main.go).
	networkDiscoveryOff bool
	// receivers holds the Receiver watch's store, which the pass and
	// discovery read (objectcache.go).
	receivers *watchStore
	units     map[string]*receiverUnit
	// sessions writes every Television's status.session for the units'
	// sessions.
	sessions *televisionSessions
	// live says the first pass has ended. A session the first pass finds
	// was there when the operator started, and it adopts its flags.
	live bool
	// stopped says that at the end of run, every goroutine that a unit or
	// a session started, and discovery, had stopped (work.go).
	stopped bool
	// log takes the lines a person reads to follow the receivers, the
	// way the node workload's log does for its adapter.
	log io.Writer
}

func newController(client *Client, readings *metrics) *controller {
	c := &controller{
		client:    client,
		dial:      dialTCP,
		wake:      make(chan struct{}, 1),
		now:       time.Now,
		readings:  readings,
		units:     map[string]*receiverUnit{},
		sessions:  newTelevisionSessions(client),
		log:       os.Stderr,
		receivers: &watchStore{},
	}
	// Discovery wakes the same loop a watch event does, so a Receiver it
	// creates or an address it finds reaches a reconcile pass at once.
	c.discovery = newDiscovery(client, func() { poke(c.wake) })
	c.discovery.receivers = c.receivers
	return c
}

// pass derives every unit from the collection as it stands now. It
// starts a client for a Receiver that names a protocol, moves a session
// that changed, and stops the client of a Receiver that is gone. The
// whole pass is one equipment_reconcile_duration_seconds observation,
// because this loop reconciles the collection as a unit and not one
// resource at a time.
func (c *controller) pass(ctx context.Context) error {
	// The wall clock times this pass, and not c.now, because c.now is a
	// fixture a test holds still to make a status's own timestamp
	// deterministic; the duration this measures is real regardless.
	began := time.Now()
	err := c.doPass(ctx)
	c.readings.observeReconcile(time.Since(began), err)
	return err
}

func (c *controller) doPass(ctx context.Context) error {
	// Each unit writes its Receiver's status, and a new unit takes what
	// the old one settled from the stored status. The store can hold the
	// copy from before the last write until the write's own event
	// arrives, and a unit that read it would send a receiver a setting
	// again, so the read replaces such a copy with the API server's
	// (objectcache.go).
	list, err := readReceivers(c.client, c.receivers)
	if err != nil {
		return err
	}

	live := map[string]bool{}
	for index := range list.Items {
		receiver := &list.Items[index]
		if receiver.Spec.Denon == nil && receiver.Spec.Wiim == nil {
			continue
		}
		live[receiver.Metadata.Name] = true
		c.reconcile(ctx, receiver)
	}

	// The first pass adopts what it finds. From its end on, a session
	// that appears is a change a person caused. A TV's session that an
	// earlier pass could not write is written again here.
	defer func() { c.live = true }()
	defer c.sessions.markLive()
	defer c.sessions.retry()
	for name, unit := range c.units {
		if !live[name] {
			// A deleted Receiver ends its session for good, so its TV's
			// session goes too. A stop at shutdown lifts none, because the
			// next operator takes the same session over.
			player := unit.player()
			unit.stop()
			delete(c.units, name)
			if player != "" {
				c.sessions.lift(player)
			}
		}
	}
	return nil
}

// reconcile brings one Receiver's unit up to its spec. An address that
// changed is a different receiver wiring, so the unit is replaced and
// not redialled.
func (c *controller) reconcile(ctx context.Context, receiver *Receiver) {
	name := receiver.Metadata.Name
	unit, held := c.units[name]
	var replaced *receiverUnit
	var carried *ReceiverSession
	if held && unit.address != c.resolvedAddress(&receiver.Spec) {
		// A new wiring is not a new session. The new unit starts the same
		// session, with the flags the old one held, as one that stands. The
		// lift lets that session return to the same TV without a wake.
		player := unit.player()
		carried = unit.standingSession()
		unit.stop()
		delete(c.units, name)
		held, replaced = false, unit
		if player != "" {
			c.sessions.lift(player)
		}
	}
	if !held {
		unit = c.start(ctx, receiver)
		c.units[name] = unit
		switch {
		case replaced != nil:
			unit.carryPower(replaced)
			unit.carryAsks(replaced)
			if carried != nil {
				unit.setSession(ctx, carried, "the unit that held it before the wiring changed handed it over")
			}
		case !c.live:
			unit.resumePower(receiver.Status, receiver.Spec.Power)
		}
	}
	unit.generation.Store(receiver.Metadata.Generation)
	unit.setInputs(receiver.Spec.Inputs)
	unit.setPower(receiver.Spec.Power)
	if receiver.Spec.Denon != nil && unit.setSettings(receiver.Spec.Denon.Settings) {
		unit.recordSettled(denonSettingsBlock, receiver.Spec.Denon.Settings)
	}
	if receiver.Spec.Wiim != nil && unit.setWiimSettings(receiver.Spec.Wiim.Settings) {
		unit.recordSettled(wiimSettingsBlock, receiver.Spec.Wiim.Settings)
	}
	if unit.setZones(receiver.Spec.Zones) {
		unit.recordSettled(zonesBlock, receiver.Spec.Zones)
	}
	adopted := ""
	if !c.live {
		adopted = "the operator found it when it started"
	}
	unit.setSession(ctx, receiver.session(), adopted)
	unit.takeAsks(receiver.session(), !c.live)
}

// protocolAddress is the address the receiver's protocol block declares.
// A change to it is a different receiver wiring, so the unit is
// replaced and not redialled.
func protocolAddress(spec *ReceiverSpec) string {
	if spec.Denon != nil {
		return spec.Denon.Address
	}
	if spec.Wiim != nil {
		return spec.Wiim.Address
	}
	return ""
}

func (c *controller) start(parent context.Context, receiver *Receiver) *receiverUnit {
	ctx, cancel := context.WithCancel(parent)
	unit := &receiverUnit{
		ctx:      ctx,
		name:     receiver.Metadata.Name,
		address:  c.resolvedAddress(&receiver.Spec),
		client:   c.client.withWaits(ctx),
		sessions: c.sessions,
		dial:     c.dial,
		now:      c.now,
		readings: c.readings,
		log:      newReceiverLog(c.log, receiver.Metadata.Name),
		cancel:   cancel,
		dirty:    make(chan struct{}, 1),
		budget:   newSendBudget(),
		settled:  newSettledRecord(receiver.Status, receiver.Spec),
	}
	unit.setInputs(receiver.Spec.Inputs)
	unit.foreign = c.discovery.skip
	unit.startDriver(receiver, unit.address, c.readings.reportCommand)
	// The generation is stored before anything can write, so the first
	// status names the spec it was built from.
	unit.generation.Store(receiver.Metadata.Generation)
	goWork(ctx, func() { unit.driver.Run(ctx) })
	goWork(ctx, func() { unit.report(ctx) })
	unit.volumeAsks = newVolumeAsker()
	goWork(ctx, func() { unit.applyVolumeAsks(ctx) })
	// The first write says the operator holds the receiver and has not
	// reached it yet, before any line arrives.
	poke(unit.dirty)
	return unit
}

// startDriver builds the driver the receiver's protocol block names and
// points the unit at it. A Receiver names exactly one protocol block, so
// the choice is a branch and not a table.
func (u *receiverUnit) startDriver(receiver *Receiver, address string, report func(string)) {
	switch {
	case receiver.Spec.Denon != nil:
		client := denon.NewClient(address, u.observe)
		client.Reporter = report
		client.Dial = u.dial
		u.driver = client
		u.denonClient = client
	case receiver.Spec.Wiim != nil:
		client := wiim.NewClient(address, u.observe)
		client.UUID = receiver.Spec.Wiim.UUID
		client.Reporter = report
		client.Foreign = func(project string) {
			if u.foreign != nil {
				u.foreign(receiver.Spec.Wiim.UUID, project, address)
			}
		}
		u.driver = client
		u.wiimClient = client
	}
}

// run reconciles once before any event arrives, then on every wake and
// every tick of backstopInterval, until ctx ends.
func (c *controller) run(ctx context.Context) {
	// Discovery and every goroutine a unit or a session starts are
	// counted, and run waits for them after it stops every unit, because
	// each one can write, and the Deployment releases its Lease after
	// run returns (work.go).
	var working sync.WaitGroup
	ctx = withWork(ctx, &working)
	c.startDiscovery(ctx)
	ticker := time.NewTicker(backstopInterval)
	defer ticker.Stop()
	for {
		if err := c.pass(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "listing receivers: %v\n", err)
		}
		select {
		case <-ctx.Done():
			c.stopAll()
			c.stopped = awaitWork(&working, workStopWait)
			return
		case <-c.wake:
		case <-ticker.C:
		}
	}
}

// startDiscovery starts the search for amps, or with discovery off,
// writes the one line that says so. With discovery off, a WiiM
// Receiver gets no address from a search, so it reaches its amp only
// through spec.wiim.address. The discovered Receivers already in the
// cluster stay, because only a search is evidence that deletes one.
func (c *controller) startDiscovery(ctx context.Context) {
	if c.networkDiscoveryOff {
		fmt.Fprintf(c.log, "%s is off: the operator sends no mDNS or SSDP search and creates no Receiver, and a WiiM Receiver reaches its amp only at spec.wiim.address\n", networkDiscoveryVariable)
		return
	}
	goWork(ctx, func() { c.discovery.run(ctx) })
}

// stopAll closes every unit at operator shutdown.
func (c *controller) stopAll() {
	c.sessions.stop()
	for name, unit := range c.units {
		unit.stop()
		delete(c.units, name)
	}
}

// poke never blocks, and a wake channel buffers exactly one, because
// the pass that answers a wake reads the whole collection.
func poke(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// drainPokes clears the wakes a burst queued behind the one already
// taken.
func drainPokes(wake <-chan struct{}) {
	for {
		select {
		case <-wake:
		default:
			return
		}
	}
}

// operate reads the configuration, waits for the Lease, and then hands
// it to operateAsLeader. Every failure here ends the process, because
// the kubelet restarts the pod with backoff and the failure shows in
// kubectl instead of hiding in a retry loop.
func operate() {
	config, err := readSettings()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	client, err := InClusterClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "in-cluster config: %v\n", err)
		os.Exit(1)
	}

	// The kubelet stops a pod with SIGTERM. The context ends on it, so
	// the loop stops every receiver's unit, and then the operator
	// releases its Lease, before the process exits. A signal that
	// arrives while the copy waits for the Lease ends the wait.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	_, err = lead(client, config).actWhileLeading(ctx, func() error {
		return operateAsLeader(ctx, client, config)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// operateAsLeader is everything the Deployment's operator does once it
// holds the Lease: it binds the metrics listener and runs serve. It
// returns when ctx ends, after every part that writes has stopped.
func operateAsLeader(ctx context.Context, client *Client, config settings) error {
	readings := newMetrics(version)
	if _, err := readings.Serve(ctx, config.metricsAddress); err != nil {
		return fmt.Errorf("metrics listener: %w", err)
	}
	return serve(ctx, client, config, readings)
}

// serve proves the collection can be read, starts the watch, and runs
// the loop until ctx ends. It answers errStillWriting when a goroutine
// that can write did not stop in time (work.go).
func serve(ctx context.Context, client *Client, config settings, readings *metrics) error {
	err := untilAnswered(ctx, func() error {
		_, err := ListReceivers(client.withContext(ctx))
		return err
	})
	if err != nil {
		return fmt.Errorf("listing receivers: %w", err)
	}

	// serve returns only after the goroutines it started stop, so none
	// of them reads the API after it.
	operator := newController(client, readings)
	if config.dial != nil {
		operator.dial = config.dial
	}
	operator.networkDiscoveryOff = config.networkDiscoveryOff
	var started sync.WaitGroup
	buses := newCECBusController(client)
	buses.sharedReceivers = true
	// The two loops share the Receiver and Television stores, so the
	// process holds each object once.
	buses.receivers = operator.receivers
	operator.sessions.televisions = buses.televisions
	started.Go(func() { watchReceivers(ctx, client, operator.wake, buses.wake, readings, operator.receivers) })
	started.Go(func() { buses.run(ctx, readings) })
	operator.run(ctx)
	started.Wait()
	if !operator.stopped {
		return errStillWriting
	}
	return nil
}
