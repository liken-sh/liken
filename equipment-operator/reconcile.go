package main

// The operator's loop: level-triggered, woken by a watch, with a ticker
// as the backstop. A pass reads the whole collection, so a lost event
// costs at most one tick and a restarted operator starts correct.
// One client per Receiver holds the receiver's connection for as long
// as the Receiver stands. A debounced writer folds the burst of lines
// that follows one change into a single status write.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
	"github.com/liken-sh/equipment-operator/wiim"
)

// How often the loop reconciles with nothing to prompt it.
const backstopInterval = 30 * time.Second

// How long a burst of lines is collected before one status write.
var statusDebounce = 250 * time.Millisecond

// receiverUnit is one Receiver's running parts: the connection, the
// status writer, the settings and zones it drives, and the session that
// holds the level.
type receiverUnit struct {
	name          string
	address       string
	settingsTopic string
	commandsTopic string
	client        *Client
	busAddress    string
	now           func() time.Time
	driver        equipment.Driver
	denonClient   *denon.Client
	wiimClient    *wiim.Client
	readings      *metrics
	cancel        context.CancelFunc
	dirty         chan struct{}
	generation    atomic.Int64
	// The ceiling and the step live here and not on the session, so an
	// edit to them reaches a standing session with no restart.
	volume atomic.Pointer[ReceiverVolume]
	// The declared inputs live here for the same reason: the sound mode
	// an input names is read when the session selects it.
	inputs atomic.Pointer[[]ReceiverInput]
	// The last power the operator applied, so a reconcile and a toggle
	// share one memory of what was sent and neither re-asserts it.
	power atomic.Pointer[equipment.Power]
	// The last settings the operator applied, so a reconcile applies a
	// declared change once and a bus write that returns a value to the
	// spec is not re-sent on the next pass.
	settings atomic.Pointer[denon.Settings]
	// The last zone controls the operator applied, keyed by zone, so a
	// reconcile sends a declared change once and a pass with no change
	// sends nothing.
	zones atomic.Pointer[map[string]ZoneSpec]

	mutex   sync.Mutex
	session *session
	applied ReceiverStatus
	written bool
}

// observe is where every line the receiver sends reaches the operator.
// It wakes the status writer, and it reaches the session that owns the
// level.
func (u *receiverUnit) observe(event equipment.Event) {
	poke(u.dirty)
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
	status := buildReceiverStatus(state, settings, u.wiimStatus(), u.driver.VolumeResolution(), u.generation.Load(), u.applied.Conditions, now)
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
// session that stands, which keeps its broker connection and its
// adopted level.
func (u *receiverUnit) setSession(ctx context.Context, spec *ReceiverSession) {
	u.mutex.Lock()
	held := u.session
	u.mutex.Unlock()

	if held != nil && spec != nil && held.spec == spec.withoutFlags() {
		held.setFlags(spec.Active, spec.Awake)
		return
	}
	if held != nil {
		u.mutex.Lock()
		u.session = nil
		u.mutex.Unlock()
		held.stop()
	}
	if spec == nil {
		u.readings.setClaimed(u.name, false)
		return
	}
	started := startSession(ctx, u.name, *spec, u.driver, u.readings, u.busAddress, u.volumeRule, u.inputSoundMode, u.applyPower)
	u.mutex.Lock()
	u.session = started
	u.mutex.Unlock()
	u.readings.setClaimed(u.name, true)
}

// setVolume records the ceiling and the step a person declared, which
// every press reads.
func (u *receiverUnit) setVolume(spec *ReceiverVolume) {
	rule := ReceiverVolume{}
	if spec != nil {
		rule = *spec
	}
	u.volume.Store(&rule)
}

// volumeRule answers what the spec states now, so a press made after an
// edit is measured against the edited scale.
func (u *receiverUnit) volumeRule() ReceiverVolume {
	if held := u.volume.Load(); held != nil {
		return *held
	}
	return ReceiverVolume{}
}

// setInputs records the declared inputs and the sound mode each names,
// which a later input selection reads.
func (u *receiverUnit) setInputs(inputs []ReceiverInput) {
	held := slices.Clone(inputs)
	u.inputs.Store(&held)
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
// operator owns this field, so it sends one command and applies the
// spec once per change, and never re-asserts while the value stands. A
// command sent before the connection is open is dropped, so the change
// waits for a reachable receiver rather than applying a value the
// equipment never saw. A Denon cannot tell standby from off, so both
// mean standby and the interface's two-way SetPower is enough; a
// protocol that could would need a richer method. An empty value is no
// declarative intent, and the receiver is left where it is.
func (u *receiverUnit) setPower(power equipment.Power) {
	if power == "" || u.powerApplied() == power {
		return
	}
	if u.driver.State().Reachable != equipment.ConditionTrue {
		return
	}
	if err := u.driver.SetPower(equipment.MainZone, power != equipment.PowerStandby && power != equipment.PowerOff); err != nil {
		fmt.Fprintf(os.Stderr, "setting the power of receiver %s: %v\n", u.name, err)
		return
	}
	u.applyPower(power)
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
// A declared value is enforced: when the settings block changes, every
// declared field is re-sent on purpose, so a value declared in the spec
// is authoritative over a change made at the receiver. A block already
// sent whose reported fields confirm is left alone; a block the
// receiver has not confirmed is sent again on the next pass and stops
// the moment the receiver reports it; a block with no reported fields
// confirms trivially, so it sends once per spec change. A command sent
// before the connection is open is dropped, so the change waits for a
// reachable receiver rather than applying a value the equipment never
// saw. An apply error is logged and not recorded, so the next pass
// tries again.
func (u *receiverUnit) setSettings(want denon.Settings) {
	if u.denonClient == nil {
		return
	}
	if u.driver.State().Reachable != equipment.ConditionTrue {
		return
	}
	observed := u.denonClient.Settings()
	if reflect.DeepEqual(u.settingsApplied(), want) && want.ConfirmedBy(observed) {
		return
	}
	if err := u.denonClient.ApplySettings(want); err != nil {
		fmt.Fprintf(os.Stderr, "applying the settings of receiver %s: %v\n", u.name, err)
		return
	}
	u.settings.Store(&want)
}

// settingsApplied answers the last settings the operator settled on.
func (u *receiverUnit) settingsApplied() denon.Settings {
	if held := u.settings.Load(); held != nil {
		return *held
	}
	return denon.Settings{}
}

// setZones drives the non-main zones to the controls a person declared.
// A declared value is enforced: when a zone's block changes, every
// declared field of that zone is re-sent on purpose, so a value declared
// in the spec is authoritative over a change made at the receiver. A
// block already sent whose reported controls confirm is left alone; a
// block the receiver has not confirmed is sent again on the next pass
// and stops the moment the receiver reports it; a block for a zone the
// receiver has not reported at all confirms trivially, so it sends once
// per spec change. A command sent before the connection is open is
// dropped, so the change waits for a reachable receiver. The main zone
// is spec.power and spec.session, so a zones map that names main is a
// misconfiguration and is rejected rather than fought. An apply error
// is logged and not recorded, so the next pass tries again.
func (u *receiverUnit) setZones(want map[string]ZoneSpec) {
	if u.driver.State().Reachable != equipment.ConditionTrue {
		return
	}
	// zonesApplied returns a copy, so this loop writes a fresh map that
	// never shares its backing with the snapshot the last pass stored; a
	// later setZones cannot reach back into an earlier one.
	applied := u.zonesApplied()
	state := u.driver.State()
	for name, spec := range want {
		if name == equipment.MainZone {
			fmt.Fprintf(os.Stderr, "zone %q on receiver %s: the main zone is spec.power and spec.session, not spec.zones\n", name, u.name)
			continue
		}
		observed, reported := state.Zone(name)
		confirmed := !reported || spec.ConfirmedBy(observed, u.driver.VolumeResolution())
		if reflect.DeepEqual(applied[name], spec) && confirmed {
			continue
		}
		if err := u.applyZone(name, spec); err != nil {
			fmt.Fprintf(os.Stderr, "setting zone %s on receiver %s: %v\n", name, u.name, err)
			continue
		}
		applied[name] = spec
	}
	u.zones.Store(&applied)
}

// zonesApplied answers the last zone controls the operator settled on,
// as a copy, so a caller can never mutate the stored snapshot.
func (u *receiverUnit) zonesApplied() map[string]ZoneSpec {
	if held := u.zones.Load(); held != nil {
		copy := make(map[string]ZoneSpec, len(*held))
		for name, spec := range *held {
			copy[name] = spec
		}
		return copy
	}
	return map[string]ZoneSpec{}
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

// startBus opens the unit's own broker connection and subscribes to
// the settings and commands topics when the spec names them. The unit
// bus is separate from a session's bus, so settings and commands reach
// a receiver with no Play and no screen. It holds no retained state, so
// it names no will.
func (u *receiverUnit) startBus(ctx context.Context) {
	if u.settingsTopic == "" && u.commandsTopic == "" {
		return
	}
	// The bus lives and dies with this unit's context, so nothing here
	// stores it: the goroutine owns the only reference.
	bus := newBus(u.busAddress, "equipment-operator-"+u.name+"-bus", nil, nil, u.busMessage)
	if u.settingsTopic != "" {
		bus.Subscribe(u.settingsTopic)
	}
	if u.commandsTopic != "" {
		bus.Subscribe(u.commandsTopic)
	}
	go bus.Run(ctx)
}

// busMessage routes one message off the unit's own bus. Each handler
// runs in its own goroutine, so a slow settings patch cannot stall the
// reader and the messages behind it on the topic. The driver client and
// the API client are thread-safe, so the handlers may overlap. The
// settings topic and the commands topic each carry their own message
// shape.
func (u *receiverUnit) busMessage(topic string, payload []byte) {
	switch topic {
	case u.settingsTopic:
		go u.handleSettings(payload)
	case u.commandsTopic:
		go u.handleCommand(payload)
	}
}

// handleSettings reads one settings message and sends the value it
// names to the receiver. A value that lands is written back to the leaf
// of spec.denon.settings it came from, so the declared state of the
// resource stays true. The write is scoped to that one leaf under this
// operator's own field manager, so a bus-written key is operator-owned
// and never a key the manifest declared. A manifest-declared key and a
// bus-written key on the same leaf is a misconfiguration: the operator's
// forced write wins each round and Flux reverts the leaf on its next
// sync. A bus write is recorded into the spec on queue acceptance, so
// the spec is desired state, not a mirror of the hardware. An error is
// logged and not recorded: the receiver's own echo is the only thing
// that moves the observed settings.
func (u *receiverUnit) handleSettings(payload []byte) {
	var message struct {
		Setting string                 `json:"setting"`
		Value   equipment.SettingValue `json:"value"`
	}
	if err := json.Unmarshal(payload, &message); err != nil || message.Setting == "" {
		return
	}
	if u.denonClient == nil {
		return
	}
	if err := u.denonClient.Set(message.Setting, message.Value); err != nil {
		fmt.Fprintf(os.Stderr, "setting %s on receiver %s: %v\n", message.Setting, u.name, err)
		return
	}
	leaf := settingsPath(message.Setting)
	if _, err := ApplyReceiverSettings(u.client, u.name, leaf, message.Value); err != nil {
		fmt.Fprintf(os.Stderr, "writing the setting %s of receiver %s: %v\n", message.Setting, u.name, err)
	}
}

// handleCommand reads one commands message and runs the one-shot it
// names. The ensure asks the session for its input; every other id is a
// driver action, and the error is logged and never fatal, so the bus
// keeps serving.
func (u *receiverUnit) handleCommand(payload []byte) {
	var message struct {
		Command string                            `json:"command"`
		Args    map[string]equipment.SettingValue `json:"args"`
	}
	if err := json.Unmarshal(payload, &message); err != nil || message.Command == "" {
		return
	}
	if message.Command == commandEnsureInput {
		u.ensureInput()
		return
	}
	if u.denonClient == nil {
		return
	}
	if err := u.denonClient.Do(message.Command, message.Args); err != nil {
		fmt.Fprintf(os.Stderr, "command %s on receiver %s: %v\n", message.Command, u.name, err)
	}
}

// commandEnsureInput is the receiver's one generic player action: make
// sure the session's player is on the input the session names. A
// program asks for it in player terms, and the receiver resolves the
// input, so no input name crosses the bus.
const commandEnsureInput = "input.ensure"

// ensureInput hands one ensure ask to the session that holds the input.
// A receiver with no session has no player listening, so the ask is
// dropped.
func (u *receiverUnit) ensureInput() {
	u.mutex.Lock()
	held := u.session
	u.mutex.Unlock()
	if held != nil {
		held.ensureInput()
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
// to the spec, so the next reconcile sees no change to re-assert. The
// session calls it after a toggle settles; setPower calls it after a
// declarative change.
func (u *receiverUnit) applyPower(power equipment.Power) {
	if _, err := ApplyReceiverPower(u.client, u.name, power); err != nil {
		fmt.Fprintf(os.Stderr, "writing the power of receiver %s: %v\n", u.name, err)
		return
	}
	u.power.Store(&power)
}

// stop lifts the session and closes the connection, which is what a
// deleted Receiver leaves behind. The metrics scoped to this receiver
// go with it, so a Receiver that is gone stops being reported.
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
	client     *Client
	busAddress string
	wake       chan struct{}
	now        func() time.Time
	readings   *metrics
	units      map[string]*receiverUnit
}

func newController(client *Client, busAddress string, readings *metrics) *controller {
	return &controller{
		client:     client,
		busAddress: busAddress,
		wake:       make(chan struct{}, 1),
		now:        time.Now,
		readings:   readings,
		units:      map[string]*receiverUnit{},
	}
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
	list, err := ListReceivers(c.client)
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

	for name, unit := range c.units {
		if !live[name] {
			unit.stop()
			delete(c.units, name)
		}
	}
	return nil
}

// reconcile brings one Receiver's unit up to its spec. An address or a
// bus topic that changed is a different receiver wiring, so the unit is
// replaced and not redialled.
func (c *controller) reconcile(ctx context.Context, receiver *Receiver) {
	name := receiver.Metadata.Name
	unit, held := c.units[name]
	if held && (unit.address != protocolAddress(&receiver.Spec) ||
		unit.settingsTopic != receiver.Spec.SettingsTopic ||
		unit.commandsTopic != receiver.Spec.CommandsTopic) {
		unit.stop()
		delete(c.units, name)
		held = false
	}
	if !held {
		unit = c.start(ctx, receiver)
		c.units[name] = unit
	}
	unit.generation.Store(receiver.Metadata.Generation)
	unit.setVolume(receiver.Spec.Volume)
	unit.setInputs(receiver.Spec.Inputs)
	unit.setPower(receiver.Spec.Power)
	if receiver.Spec.Denon != nil {
		unit.setSettings(receiver.Spec.Denon.Settings)
	}
	unit.setZones(receiver.Spec.Zones)
	unit.setSession(ctx, receiver.Spec.Session)
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
		name:          receiver.Metadata.Name,
		address:       protocolAddress(&receiver.Spec),
		settingsTopic: receiver.Spec.SettingsTopic,
		commandsTopic: receiver.Spec.CommandsTopic,
		client:        c.client,
		busAddress:    c.busAddress,
		now:           c.now,
		readings:      c.readings,
		cancel:        cancel,
		dirty:         make(chan struct{}, 1),
	}
	unit.setVolume(receiver.Spec.Volume)
	unit.setInputs(receiver.Spec.Inputs)
	unit.startDriver(receiver, c.readings.reportCommand)
	// The generation is stored before anything can write, so the first
	// status names the spec it was built from.
	unit.generation.Store(receiver.Metadata.Generation)
	go unit.driver.Run(ctx)
	go unit.report(ctx)
	unit.startBus(ctx)
	// The first write says the operator holds the receiver and has not
	// reached it yet, before any line arrives.
	poke(unit.dirty)
	return unit
}

// startDriver builds the driver the receiver's protocol block names and
// points the unit at it. A Receiver names exactly one protocol block, so
// the choice is a branch and not a table.
func (u *receiverUnit) startDriver(receiver *Receiver, report func(string)) {
	switch {
	case receiver.Spec.Denon != nil:
		client := denon.NewClient(receiver.Spec.Denon.Address, u.observe)
		client.Reporter = report
		u.driver = client
		u.denonClient = client
	case receiver.Spec.Wiim != nil:
		client := wiim.NewClient(receiver.Spec.Wiim.Address, u.observe)
		client.UUID = receiver.Spec.Wiim.UUID
		client.Reporter = report
		u.driver = client
		u.wiimClient = client
	}
}

// run reconciles once before any event arrives, then on every wake and
// every backstop tick, until ctx ends.
func (c *controller) run(ctx context.Context) {
	ticker := time.NewTicker(backstopInterval)
	defer ticker.Stop()
	for {
		if err := c.pass(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "listing receivers: %v\n", err)
		}
		select {
		case <-ctx.Done():
			c.stopAll()
			return
		case <-c.wake:
		case <-ticker.C:
		}
	}
}

func (c *controller) stopAll() {
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

// operate reads the configuration and hands it to serve. Every failure
// here ends the process, because the kubelet restarts the pod with
// backoff and the failure shows in kubectl instead of hiding in a retry
// loop.
func operate() {
	config := readSettings()
	if config.busAddress == "" {
		fmt.Fprintf(os.Stderr, "%s is unset; the Deployment must name the broker\n", busAddressVariable)
		os.Exit(1)
	}

	client, err := InClusterClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "in-cluster config: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	readings := newMetrics(version)
	if _, err := readings.Serve(ctx, config.metricsAddress); err != nil {
		fmt.Fprintf(os.Stderr, "metrics listener: %v\n", err)
		os.Exit(1)
	}

	if err := serve(ctx, client, config.busAddress, readings); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serve proves the collection can be read, starts the watch from the
// version that first list carried, and runs the loop until ctx ends.
func serve(ctx context.Context, client *Client, busAddress string, readings *metrics) error {
	list, err := ListReceivers(client)
	if err != nil {
		return fmt.Errorf("listing receivers: %w", err)
	}

	operator := newController(client, busAddress, readings)
	go watchReceivers(ctx, client, list.Metadata.ResourceVersion, operator.wake, readings)
	operator.run(ctx)
	return nil
}
