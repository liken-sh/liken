package main

// The controller runs a pass over every Receiver when the Receiver
// watch or discovery wakes it, and on a slow tick for failures that no
// event follows. A pass reads the whole collection, so a restarted
// operator needs no saved state to start correctly.
//
// Each Receiver has a receiverUnit, which keeps the connection to the
// receiver open for as long as the Receiver exists.

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/signal"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/equipment-operator/wiim"
	"github.com/liken-sh/liken/kubernetes/events"
)

// backstopInterval is how often the loop runs a pass when nothing wakes
// it. The watch and discovery wake the loop for every change. The tick
// retries what no event follows: a list or a Television status.session
// write that the API server refused, a setting whose send failed, and a
// setting that the receiver still reports at another value, while the
// send budget allows more sends. A tick reads the Receivers from the
// watch's store (objectcache.go), so on a settled cluster it sends
// nothing to the API server or to a receiver.
const backstopInterval = 30 * time.Second

// statusDebounce is how long the status writer waits after the first
// line of a burst, so the whole burst produces one status write.
const statusDebounce = 250 * time.Millisecond

// receiverUnit drives one Receiver: the connection to the device, the
// status writes, the declared settings and zones, and the session that a
// Player writes into the Receiver.
type receiverUnit struct {
	// Each goroutine of the unit runs under ctx, and the wait group in
	// ctx counts it (work.go).
	ctx         context.Context
	name        string
	address     string
	client      *Client
	recorder    *events.Recorder
	reference   events.ObjectReference
	dial        dialFunc
	listen      listenFunc
	now         func() time.Time
	driver      equipment.Driver
	denonClient *denon.Client
	wiimClient  *wiim.Client
	// foreign tells discovery that the device at an address reports a
	// project other than WiiM. It is nil in a test that builds a unit
	// alone.
	foreign    func(uuid, project, address string)
	readings   *metrics
	log        *receiverLog
	cancel     context.CancelFunc
	dirty      chan struct{}
	generation atomic.Int64
	// The declared inputs are kept on the unit, not on the session, so
	// an edit to them reaches a running session without a restart.
	inputs atomic.Pointer[[]ReceiverInput]
	// The power the operator last settled. Reconcile and the session's
	// power toggle both read it, so neither sends a power that the other
	// has already sent.
	power atomic.Pointer[equipment.Power]
	// The settings and zone controls the operator last applied. A field
	// that the receiver does not report is compared with these, so it is
	// sent once for each change of the spec.
	settings     atomic.Pointer[denon.Settings]
	wiimSettings atomic.Pointer[wiim.Settings]
	zones        atomic.Pointer[map[string]ZoneSpec]
	// budget counts the sends of each declared field, so a field that
	// the receiver never confirms stops being sent.
	budget *sendBudget
	// settled holds a digest of each declared block that the operator
	// has sent. It is read from status.settledSettings when the unit
	// starts and written back with the status.
	settled *settledRecord

	// seen and volumeAsks track the asks of status.session
	// (session_asks.go).
	seen       seenAsks
	volumeAsks *volumeAsker

	mutex   sync.Mutex
	session *session
	// sessions writes status.session of the Television that shows the
	// session's Display. It is nil in a test that builds a unit with no
	// Television.
	sessions *televisionSessions
	applied  ReceiverStatus
	written  bool
}

// observe receives every event that the driver reads from the receiver.
// A volume or mute report also wakes the volume ask sender, which waits
// for the receiver's report of one ask before it sends the next.
func (u *receiverUnit) observe(event equipment.Event) {
	sendWake(u.dirty)
	u.log.observe()
	if u.volumeAsks != nil && (event.Field == equipment.EventVolume || event.Field == equipment.EventMute) {
		sendWake(u.volumeAsks.reported)
	}
	u.mutex.Lock()
	held := u.session
	u.mutex.Unlock()
	if held != nil {
		held.observe(event)
	}
}

// report writes the status once for each burst of lines from the
// receiver, statusDebounce after the first line.
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
		drainWakes(u.dirty)
		// When the timer and the context are both ready, select picks
		// one at random. Check the context again so that a unit stopped
		// during the debounce does not write.
		if ctx.Err() != nil {
			return
		}
		u.write()
	}
}

// write builds the status from the driver's state and applies it when it
// differs from the last one applied.
func (u *receiverUnit) write() {
	now := u.now()
	state := u.driver.State()
	// The metrics are recorded even when the status is unchanged, so
	// observation_last_success_timestamp_seconds advances on every burst.
	u.readings.recordObservation(u.name, state, u.driver.VolumeResolution(), now)
	if u.wiimClient != nil {
		u.readings.recordWiim(u.name, u.wiimClient.Readings())
	}

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
	postTransitions(u.recorder, u.reference, u.applied.Conditions, status.Conditions)
	u.applied, u.written = status, true
}

// setSession makes the running session match spec, which is nil when
// the Receiver has no session. When spec differs only in the active and
// awake flags, the running session takes the new flags and keeps
// running, because a session sends its power and input commands once,
// when it starts. Any other difference stops the session and starts a
// new one.
//
// adopted is empty for a session that appears while the operator runs.
// Otherwise it is the reason, for the log line, that the session was
// already running: the operator found it in its first pass, or the unit
// for the Receiver's old address handed it over. An adopted session
// sends the receiver nothing for its flags.
func (u *receiverUnit) setSession(ctx context.Context, spec *ReceiverSession, adopted string) {
	u.mutex.Lock()
	held := u.session
	u.mutex.Unlock()
	// The InputSelected condition depends on the running session.
	defer sendWake(u.dirty)

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

// flagFlips describes each flag of the running session that spec
// changes, and returns an empty string when it changes none.
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

func (u *receiverUnit) setInputs(inputs []ReceiverInput) {
	held := slices.Clone(inputs)
	u.inputs.Store(&held)
}

// roomFor returns the link a session uses to tell its Television what
// it did, or nil for a unit that writes no Television session.
func (u *receiverUnit) roomFor(spec *ReceiverSession) roomEvents {
	if u.sessions == nil {
		return nil
	}
	return u.sessions.room(u.log, spec.Player, spec.Input, u.inputMonitor)
}

// inputMonitor returns the Display whose picture a declared input
// carries, or an empty string when the input is not declared.
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

// inputSoundMode returns the sound mode declared for an input, or an
// empty string when the input is not declared or has none.
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

// setPower sends spec.power to the receiver only when it differs from
// the power that the operator last settled. The operator does not send a
// settled value again, so it does not overrule a person who turns the
// receiver on or off by hand, and an edit to another field of the spec
// sends no power.
//
// The value found in the first pass counts as settled (see
// resumePower). A Receiver created while the operator runs has no
// settled value, so its spec.power is sent. The send waits until the
// receiver is reachable and has finished its first full read, so the operator can read the
// receiver's power, and it is skipped when the receiver already
// reports that power. Standby and Off both send
// standby, because a Denon reports both as Standby. A WiiM has no
// standby and always reports On. An empty spec.power leaves the
// receiver as it is.
func (u *receiverUnit) setPower(power equipment.Power) {
	if power == "" || power == u.powerApplied() {
		return
	}
	if !u.driver.FirstReadDone() || u.driver.State().Reachable != equipment.ConditionTrue {
		return
	}
	request := fmt.Sprintf("generation %d requests power %s", u.generation.Load(), power)
	on := power == equipment.PowerOn
	if observed := mainZone(u.driver.State()); on == (observed.Power == equipment.PowerOn) {
		u.log.printf("%s; sent nothing, because the receiver reports %s", request, powerWords(observed, 0))
		u.applyPower(power)
		return
	}
	sent := powerWords(equipment.ZoneState{Power: commandedPower(on)}, 0)
	line := fmt.Sprintf("%s; sent %s", request, sent)
	began := time.Now()
	if err := u.driver.SetPower(equipment.MainZone, on); err != nil {
		u.log.refused(line, err)
		return
	}
	u.log.confirm(line, began, mainZoneCheck(u.driver, sent, powerWords))
	u.applyPower(power)
}

// resumePower determines whether the spec.power found in the first pass
// counts as settled. When status.settledPower records a different
// value, spec.power changed while no operator was running, so the unit
// settles the recorded value and setPower sends the new one. Otherwise
// the unit adopts spec.power.
func (u *receiverUnit) resumePower(stored ReceiverStoredStatus, power equipment.Power) {
	if settled := stored.SettledPower; settled != "" && power != "" && settled != power {
		u.power.Store(&settled)
		return
	}
	u.adoptPower(power)
}

// adoptPower records spec.power as settled without sending it. The
// spec.power found at startup can be older than the receiver's power,
// because a Play or a person can turn the receiver on or off after the
// session's toggle last wrote spec.power. Sending it would undo that
// change.
func (u *receiverUnit) adoptPower(power equipment.Power) {
	u.power.Store(&power)
	sendWake(u.dirty)
	if power != "" {
		u.log.printf("generation %d requests power %s; the operator found it when it started, so it sent nothing", u.generation.Load(), power)
	}
}

// carryPower copies the settled power from the unit that this one
// replaces, because a changed address is not a change of spec.power.
func (u *receiverUnit) carryPower(old *receiverUnit) {
	if held := old.power.Load(); held != nil {
		u.power.Store(held)
	}
}

// denonSettings returns the settings that the Denon last reported, or
// nil when another protocol drives the receiver.
func (u *receiverUnit) denonSettings() *denon.Settings {
	if u.denonClient == nil {
		return nil
	}
	settings := u.denonClient.Settings()
	return &settings
}

// wiimStatus returns the status that the WiiM last reported, or nil
// when another protocol drives the receiver.
func (u *receiverUnit) wiimStatus() *wiim.Status {
	if u.wiimClient == nil {
		return nil
	}
	status := u.wiimClient.Status()
	return &status
}

// setSettings sends a Denon the fields of spec.denon.settings that
// Pending keeps. A field that the receiver reports is sent on each pass
// until the receiver reports the declared value, within the send
// budget, so a change made at the receiver is sent back. A field that
// the receiver does not report is sent once for each change of the
// block. After a restart it is sent only when the block differs from
// status.settledSettings (see differsFromSettled). The apply waits for
// the first full read, because before it the receiver has reported nothing to
// compare with.
//
// setSettings returns true when the block is applied, so the caller can
// record it as settled. After a failed send it returns false, and the
// next pass tries again.
func (u *receiverUnit) setSettings(want denon.Settings) bool {
	if u.denonClient == nil {
		return false
	}
	if !u.driver.FirstReadDone() || u.driver.State().Reachable != equipment.ConditionTrue {
		return false
	}
	previous, known := u.settingsApplied()
	known = known || u.differsFromSettled(denonSettingsBlock, want)
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

// differsFromSettled reports whether the declared block differs from
// its digest in status.settledSettings. After a restart the unit has no
// earlier value to compare an unreported field with, so it compares
// the digest. When it matches, the fields were sent before the restart.
// When it differs, the Receiver is new or the block changed while no
// operator was running, and each unreported field is sent once.
func (u *receiverUnit) differsFromSettled(path string, block any) bool {
	return !u.settled.holds(path, block)
}

// recordSettled records that the operator has sent a declared block,
// and wakes the status writer when status.settledSettings changes.
func (u *receiverUnit) recordSettled(path string, block any) {
	if u.settled.settle(path, block) {
		sendWake(u.dirty)
	}
}

// budgeted returns the pending fields of one block that the send budget
// still allows. When the set of fields that the budget holds back
// changes, it wakes the status writer, because the SettingsConfirmed
// condition is built from that set.
func budgeted[T any](u *receiverUnit, family string, pending T) T {
	before := u.budget.unconfirmed()
	kept := spend(u.budget, family, pending)
	if !slices.Equal(before, u.budget.unconfirmed()) {
		sendWake(u.dirty)
	}
	return kept
}

// settingsApplied returns the settings that the operator last applied,
// and whether it has applied any since it started.
func (u *receiverUnit) settingsApplied() (denon.Settings, bool) {
	if held := u.settings.Load(); held != nil {
		return *held, true
	}
	return denon.Settings{}, false
}

// setWiimSettings is setSettings for spec.wiim.settings.
func (u *receiverUnit) setWiimSettings(want wiim.Settings) bool {
	if !u.driver.FirstReadDone() || u.driver.State().Reachable != equipment.ConditionTrue {
		return false
	}
	previous, known := u.wiimSettingsApplied()
	known = known || u.differsFromSettled(wiimSettingsBlock, want)
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

// wiimSettingsApplied returns the WiiM settings that the operator last
// applied, and whether it has applied any since it started.
func (u *receiverUnit) wiimSettingsApplied() (wiim.Settings, bool) {
	if held := u.wiimSettings.Load(); held != nil {
		return *held, true
	}
	return wiim.Settings{}, false
}

// setZones sends each zone of spec.zones the controls that
// ZoneSpec.Pending keeps, by the same rules as setSettings. spec.power
// and the session control the main zone, so setZones logs and skips a
// main entry.
func (u *receiverUnit) setZones(want map[string]ZoneSpec) bool {
	if !u.driver.FirstReadDone() || u.driver.State().Reachable != equipment.ConditionTrue {
		return false
	}
	applied, known := u.zonesApplied()
	known = known || u.differsFromSettled(zonesBlock, want)
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

// zonesApplied returns a copy of the zone controls that the operator
// last applied, so a caller cannot change the stored map, and whether
// it has applied any since it started.
func (u *receiverUnit) zonesApplied() (map[string]ZoneSpec, bool) {
	if held := u.zones.Load(); held != nil {
		snapshot := make(map[string]ZoneSpec, len(*held))
		for name, spec := range *held {
			snapshot[name] = spec
		}
		return snapshot, true
	}
	return map[string]ZoneSpec{}, false
}

// applyZone sends the controls declared for one zone. The volume is in
// display units, so applyZone converts it to the driver's smallest
// steps first. It returns the first error, named for the control that
// failed, and an unknown zone is an error. The caller logs it.
func (u *receiverUnit) applyZone(name string, spec ZoneSpec) error {
	if spec.Power != "" {
		if err := u.driver.SetPower(name, spec.Power != equipment.PowerStandby && spec.Power != equipment.PowerOff); err != nil {
			return fmt.Errorf("setting the power: %w", err)
		}
	}
	if spec.Input != "" {
		if err := u.driver.SetInput(name, spec.Input); err != nil {
			return fmt.Errorf("setting the input: %w", err)
		}
	}
	if spec.Volume != nil {
		steps := int(math.Round(*spec.Volume * float64(u.driver.VolumeResolution())))
		if err := u.driver.SetVolume(name, steps); err != nil {
			return fmt.Errorf("setting the volume: %w", err)
		}
	}
	if spec.Mute != nil {
		if err := u.driver.SetMute(name, *spec.Mute); err != nil {
			return fmt.Errorf("setting the mute: %w", err)
		}
	}
	if spec.Sleep != nil {
		if err := u.driver.SetSleep(name, *spec.Sleep); err != nil {
			return fmt.Errorf("setting the sleep timer: %w", err)
		}
	}
	return nil
}

// settingsCheck turns a ConfirmedBy test into the check that
// receiverLog.confirm runs, with the words that the log line ends with.
func settingsCheck(confirmed func() bool) func() (string, bool) {
	return func() (string, bool) {
		if confirmed() {
			return "no value that differs", true
		}
		return "a value that differs", false
	}
}

// powerApplied returns the power that the operator last settled, or an
// empty string when it has settled none.
func (u *receiverUnit) powerApplied() equipment.Power {
	if held := u.power.Load(); held != nil {
		return *held
	}
	return ""
}

// applyPower writes power to spec.power and records it as settled, so
// the next pass has no change to send, and wakes the status writer for
// status.settledPower. The session calls it after a power toggle, and
// setPower after a declared change. When the write fails, nothing is
// recorded.
func (u *receiverUnit) applyPower(power equipment.Power) {
	if _, err := ApplyReceiverPower(u.client, u.name, power); err != nil {
		fmt.Fprintf(os.Stderr, "writing the power of receiver %s: %v\n", u.name, err)
		return
	}
	u.power.Store(&power)
	sendWake(u.dirty)
}

// runningSessionSpec returns the spec of the running session with its
// current flags, or nil when no session is running.
func (u *receiverUnit) runningSessionSpec() *ReceiverSession {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.session == nil {
		return nil
	}
	spec := u.session.spec
	spec.Active, spec.Awake = u.session.active.Load(), u.session.awake.Load()
	return &spec
}

// player returns the name of the Player whose session is running, or an
// empty string when no session is running.
func (u *receiverUnit) player() string {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.session == nil {
		return ""
	}
	return u.session.spec.Player
}

// stop ends the session, closes the connection, and removes the
// receiver's metrics.
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

// controller runs the passes over the Receivers and owns their units.
type controller struct {
	client *Client
	// When recorder is nil, no Event is posted.
	recorder *events.Recorder
	// dial opens the connection to each receiver and to each UPnP
	// description, and listen opens the listener that each WiiM sends
	// its events to.
	dial      dialFunc
	listen    listenFunc
	wake      chan struct{}
	now       func() time.Time
	readings  *metrics
	discovery *discovery
	// networkDiscoveryOff stops discovery from running. The operator
	// then sends no search onto the LAN and creates no Receiver
	// (main.go).
	networkDiscoveryOff bool
	// receivers is the Receiver watch's store, which the pass and
	// discovery read (objectcache.go).
	receivers *watchStore
	units     map[string]*receiverUnit
	// sessions writes status.session of each Television for the units'
	// sessions.
	sessions *televisionSessions
	// live is true after the first pass ends. A session that the first
	// pass finds was already running before the operator started, so
	// its unit adopts the session and sends nothing for its flags.
	live bool
	// stopped is true when run ended after every goroutine of the
	// units, the sessions, and discovery had stopped (work.go).
	stopped bool
	// log receives the log lines about each receiver.
	log io.Writer
}

func newController(client *Client, readings *metrics) *controller {
	c := &controller{
		client:    client,
		dial:      dialTCP,
		listen:    net.Listen,
		wake:      make(chan struct{}, 1),
		now:       time.Now,
		readings:  readings,
		units:     map[string]*receiverUnit{},
		sessions:  newTelevisionSessions(client),
		log:       os.Stderr,
		receivers: &watchStore{},
	}
	// Discovery wakes the loop, so a Receiver that it creates or an
	// address that it finds is reconciled at once.
	c.discovery = newDiscovery(client, func() { sendWake(c.wake) })
	c.discovery.receivers = c.receivers
	return c
}

// pass reconciles every Receiver that has a protocol block, and stops
// the unit of each Receiver that is gone or has none. The whole pass is
// one observation of equipment_reconcile_duration_seconds.
func (c *controller) pass(ctx context.Context) error {
	// The pass is timed with the wall clock and not c.now, because
	// tests fix c.now to fix the timestamps of a status.
	began := time.Now()
	err := c.doPass(ctx)
	c.readings.observeReconcile(time.Since(began), err)
	return err
}

func (c *controller) doPass(ctx context.Context) error {
	// A new unit reads what was settled from the stored status, so the
	// read must not return a copy older than the operator's last status
	// write (objectcache.go).
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

	// The first pass adopts what it finds. After it, a session that
	// appears is a new session. retry writes each Television session
	// that an earlier pass could not write.
	defer func() { c.live = true }()
	defer c.sessions.markLive()
	defer c.sessions.retry()
	for name, unit := range c.units {
		if !live[name] {
			// A deleted Receiver ends its session, so the session is
			// removed from its Television too. stopAll at shutdown
			// removes none, because the next operator adopts the
			// session.
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

// reconcile brings the unit of one Receiver up to its spec. A changed
// address is a different receiver, so the unit is replaced and not
// reconnected.
func (c *controller) reconcile(ctx context.Context, receiver *Receiver) {
	name := receiver.Metadata.Name
	unit, held := c.units[name]
	var replaced *receiverUnit
	var carried *ReceiverSession
	if held && unit.address != c.resolvedAddress(&receiver.Spec) {
		// The new unit adopts the old unit's session with its current
		// flags. lift removes the session from its Television only
		// after a delay, so when the adopted session opens on the same
		// Display first, the TV is not woken again.
		player := unit.player()
		carried = unit.runningSessionSpec()
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

// protocolAddress returns the address in the Receiver's protocol block,
// or an empty string when the block has none.
func protocolAddress(spec *ReceiverSpec) string {
	if spec.Denon != nil {
		return spec.Denon.Address
	}
	if spec.Wiim != nil {
		return spec.Wiim.Address
	}
	return ""
}

// start creates the unit for a Receiver and starts its driver, status
// writer, and volume ask sender.
func (c *controller) start(parent context.Context, receiver *Receiver) *receiverUnit {
	ctx, cancel := context.WithCancel(parent)
	unit := &receiverUnit{
		ctx:       ctx,
		name:      receiver.Metadata.Name,
		address:   c.resolvedAddress(&receiver.Spec),
		client:    c.client.withWaits(ctx),
		recorder:  c.recorder,
		reference: reference("Receiver", receiver.Metadata),
		sessions:  c.sessions,
		dial:      c.dial,
		listen:    c.listen,
		now:       c.now,
		readings:  c.readings,
		log:       newReceiverLog(c.log, receiver.Metadata.Name),
		cancel:    cancel,
		dirty:     make(chan struct{}, 1),
		budget:    newSendBudget(),
		settled:   newSettledRecord(receiver.Status, receiver.Spec),
		// The driver's goroutine reads volumeAsks for each line that
		// the receiver sends, so volumeAsks must exist before the
		// driver starts.
		volumeAsks: newVolumeAsker(),
	}
	// The stored conditions are the unit's first previous conditions,
	// so an operator that restarts keeps each lastTransitionTime and
	// posts an Event only for a condition that changed while it was
	// away.
	unit.applied.Conditions = receiver.Status.Conditions
	unit.setInputs(receiver.Spec.Inputs)
	unit.foreign = c.discovery.skip
	unit.startDriver(receiver, unit.address, c.readings.reportCommand)
	// The generation is stored before anything can write, so the first
	// status carries the generation of the spec that it was built from.
	unit.generation.Store(receiver.Metadata.Generation)
	goWork(ctx, func() { unit.driver.Run(ctx) })
	goWork(ctx, func() { unit.report(ctx) })
	goWork(ctx, func() { unit.applyVolumeAsks(ctx) })
	// The first status write reports that the unit exists and has not
	// reached the receiver yet. It happens before any line arrives.
	sendWake(unit.dirty)
	return unit
}

// startDriver creates the driver for the protocol block that the
// Receiver declares and stores it on the unit.
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
		client.Dial = u.dial
		client.Listen = u.listen
		client.Foreign = func(project string) {
			if u.foreign != nil {
				u.foreign(receiver.Spec.Wiim.UUID, project, address)
			}
		}
		u.driver = client
		u.wiimClient = client
	}
}

// run runs one pass before any event arrives, then another on each wake
// and each tick of backstopInterval, until ctx ends.
func (c *controller) run(ctx context.Context) {
	// Discovery and every goroutine that a unit or a session starts are
	// counted. After run stops every unit it waits for them, because
	// each can write and the Deployment releases its Lease after run
	// returns (work.go).
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

// startDiscovery starts the search for receivers. When discovery is off
// it writes one log line that says so. A WiiM Receiver then gets no
// address from a search and reaches its device only through
// spec.wiim.address. Receivers that discovery created earlier stay,
// because discovery deletes a Receiver only on the evidence of a
// search.
func (c *controller) startDiscovery(ctx context.Context) {
	if c.networkDiscoveryOff {
		fmt.Fprintf(c.log, "%s is off: the operator sends no mDNS or SSDP search and creates no Receiver, and a WiiM Receiver reaches its amp only at spec.wiim.address\n", networkDiscoveryVariable)
		return
	}
	c.discovery.dial = c.dial
	goWork(ctx, func() { c.discovery.run(ctx) })
}

// stopAll stops every unit at operator shutdown.
func (c *controller) stopAll() {
	c.sessions.stop()
	for name, unit := range c.units {
		unit.stop()
		delete(c.units, name)
	}
}

// sendWake sends to wake without blocking. A wake channel buffers one
// value, because a pass reads the whole collection and one pending wake
// covers any number of events.
func sendWake(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// drainWakes discards the wakes that a burst queued behind the one
// already taken.
func drainWakes(wake <-chan struct{}) {
	for {
		select {
		case <-wake:
		default:
			return
		}
	}
}

// operate reads the settings, waits for the Lease, and then runs
// operateAsLeader with the leader's client. A failure ends the process,
// because the kubelet restarts the pod with backoff and the failure
// shows in kubectl instead of hiding in a retry loop.
func operate() {
	config, err := readSettings()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// The kubelet stops a pod with SIGTERM. The context ends on it, so
	// the loop stops every unit, and then the operator releases its
	// Lease before the process exits. A signal that arrives while the
	// operator waits for the Lease ends the wait.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	_, err = lead(config).actWhileLeading(ctx, func(client *Client) error {
		return operateAsLeader(ctx, client, config)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// operateAsLeader does what the operator does while it holds the Lease:
// it binds the metrics listener and runs serve. It returns when ctx
// ends, after every part that writes has stopped.
func operateAsLeader(ctx context.Context, client *Client, config settings) error {
	readings := newMetrics(version)
	if _, err := readings.Serve(ctx, config.metricsAddress); err != nil {
		return fmt.Errorf("opening the metrics listener: %w", err)
	}
	return serve(ctx, client, config, readings)
}

// serve checks that the Receivers can be listed, starts the watch, and
// runs the loop until ctx ends. It returns errStillWriting when a
// goroutine that can write did not stop in time (work.go), or when the
// Event recorder's last write did not return in time.
func serve(ctx context.Context, client *Client, config settings, readings *metrics) error {
	err := retryWhileThrottled(ctx, func() error {
		_, err := ListReceivers(client.withContext(ctx))
		return err
	})
	if err != nil {
		return fmt.Errorf("listing the Receivers: %w", err)
	}

	// serve returns only after the goroutines it started have stopped,
	// so none of them reads the API after it returns. The recorder
	// outlives ctx so that the Events of the last pass are written.
	// serve shuts it down after every loop that posts has returned.
	recorder := events.New(context.Background(), client.Client, "equipment-operator", events.Options{})
	operator := newController(client, readings)
	operator.recorder = recorder
	if config.dial != nil {
		operator.dial = config.dial
	}
	if config.listen != nil {
		operator.listen = config.listen
	}
	operator.networkDiscoveryOff = config.networkDiscoveryOff
	var started sync.WaitGroup
	buses := newCECBusController(client, config.namespace)
	buses.recorder = recorder
	buses.sharedReceivers = true
	// The two loops share the Receiver and Television stores, so the
	// process holds each object once.
	buses.receivers = operator.receivers
	operator.sessions.televisions = buses.televisions
	started.Go(func() { watchReceivers(ctx, client, operator.wake, buses.wake, readings, operator.receivers) })
	started.Go(func() { buses.run(ctx, readings) })
	operator.run(ctx)
	started.Wait()
	shutdown, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
	defer cancel()
	recorded := recorder.Shutdown(shutdown)
	if !operator.stopped {
		return fmt.Errorf("a receiver unit or a session did not stop in time: %w", errStillWriting)
	}
	if recorded != nil {
		return fmt.Errorf("%w: %w", errStillWriting, recorded)
	}
	return nil
}
