package wiim

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/equipment-operator/upnp"
)

var (
	requestTimeout = 8 * time.Second

	// pollInterval is the wait between polls of a device that answers.
	// The client polls although it subscribes to events, for two reasons.
	// The amp sends no event while nothing changes, so a failed
	// getStatusEx is how the client finds out that the amp is gone before
	// the next subscription renewal fails. And the event parser reads only
	// the volume, the mute, and the transport state, so getPlayerStatus is
	// the client's only read of the input.
	pollInterval = 10 * time.Second

	maxBackoff = 30 * time.Second

	// settingsResync is how long after the last read of every family the
	// next poll reads them all again. The events report only the volume,
	// the mute, and the transport state, so a change to the equalizer, the
	// output, a timer, or another setting made in the WiiM Home app or on
	// the amp reaches the client only through a read. Without this read,
	// status shows such a change only after the next subscription opens,
	// and the operator does not set back a declared setting that the app
	// changed.
	settingsResync = 30 * time.Minute

	// pollFailures is how many polls in a row must fail before the
	// client calls the device unreachable. A burst of commands can make
	// the device miss one request and answer the next, so one failed
	// poll does not mean the device is gone.
	pollFailures = 3
)

const maxSleepMinutes = 1440

// trackRetries is the most getMetaInfo requests, one on each poll and
// counting the first, that the client sends for one track while they
// fail, so a getMetaInfo that always fails is not sent on every poll. A
// track change starts the count again.
const trackRetries = 3

// readRetries is the number of full reads in a row that a command which
// answered before can fail in before the client stops counting its
// failures. Until then, each failure makes the next poll a full read
// again. After that, the command is still sent in each full read that
// something else triggers, but its failure triggers no full read, so a
// command that stopped answering does not make every poll a full read
// that waits out requestTimeout.
const readRetries = 3

// volumeStepMax is the top of the device's volume scale of whole steps.
const volumeStepMax = 100

// Client drives one WiiM and keeps the state the device last reported.
// The device's HTTPS API has no authentication and presents a
// self-signed certificate, so the transport skips certificate
// verification.
type Client struct {
	address  string
	listener func(equipment.Event)

	// upnpBase is the URL of the device's UPnP MediaRenderer, which
	// serves the event subscriptions on a fixed port, not the control
	// port.
	upnpBase string

	// upnp sends the subscription requests. The device closes the
	// connection after it answers a SUBSCRIBE, so each request opens its
	// own connection.
	upnp *http.Client

	// Reporter receives CommandOK or CommandFailed for each getStatusEx
	// read, setter, and subscription request. It can be nil.
	Reporter func(status string)

	// Foreign is called when getStatusEx reports a project that is not a
	// WiiM and differs from the last one reported. The client's polls then
	// send that device getStatusEx and nothing else.
	Foreign func(project string)

	// UUID is the identity the device must report before the client
	// drives it. An empty value skips the check.
	UUID string

	// Dial opens each connection to the device: the HTTPS commands, the
	// event subscriptions, and the read of the description. NewClient
	// sets a TCP dialer, and a test sets a dialer that reaches its fake
	// device in memory.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)

	// Listen opens the callback listener that the device sends its events
	// to. NewClient sets net.Listen, and a test sets a listener in memory
	// that its fake device can reach.
	Listen func(network, address string) (net.Listener, error)

	http *http.Client

	// reads is held for one cycle of reads: a poll, or the read back
	// after a setter. A cycle copies the state, reads into the copy, and
	// publishes the copy. When two cycles overlap, each copies the state
	// before the other publishes, so the later publish undoes the earlier
	// cycle's reads.
	reads sync.Mutex

	mutex     sync.Mutex
	state     Status
	readings  Readings
	reachable equipment.ConditionStatus
	// resync is true while the next poll must read every family: before
	// the first poll, and after the client called the amp unreachable.
	// settingsRead is when a poll last read every family.
	resync       bool
	settingsRead time.Time
	// pendingReads is the families that a setter's read back left for
	// the poll to read, because the poll held reads (readback.go).
	pendingReads family
	// writes numbers each write of an event or a setter to a field that
	// a poll also reads. written is the number of the last write to each
	// field (sequence.go).
	writes  uint64
	written fieldWrites

	// These fields belong to the holder of reads. readFailures counts
	// the failed requests of commands that answered before. A poll
	// compares it before and after a full read to tell whether every
	// command answered. answeredOnce is the commands that have answered
	// at least once. gaveUp is the commands whose failures the client has
	// logged and stopped counting (readFailed). failedReads counts, for
	// each command that answered before, the full reads in a row in which
	// it failed. trackFailures is the number of getMetaInfo requests in a
	// row that failed.
	readFailures  int
	answeredOnce  map[string]bool
	gaveUp        map[string]bool
	failedReads   map[string]int
	trackFailures int
	// unread is the read commands whose last answer had no value: the
	// device's reply for a command it does not support or a read it cannot
	// serve. Settings reports no value for a field whose command is in
	// unread, so the zero value in the snapshot is not taken for the
	// device's setting. A failed request does not change unread, because
	// a lost request does not show whether the model supports the command.
	// The mutex guards it.
	unread map[string]bool
	// misses is how many polls in a row the device did not answer. The
	// next poll the device answers resets it.
	misses int
	// foreign is the project that a device reported when it is not a
	// WiiM. It is empty while the device is a WiiM or has not answered.
	foreign string
	// firstReadDone is true once a poll has read every family. The controller
	// waits for it before it applies a declared setting, so it compares
	// against the device's own values.
	firstReadDone bool
	// description is the amp's UPnP description, and describeRetry is
	// the earliest time to retry a description read that failed
	// (description.go). State returns its model and manufacturer, which
	// the controller writes to status.model and status.manufacturer, so
	// status.wiim does not repeat them.
	description   upnp.Description
	describeRetry time.Time

	// The event side: the callback listener that the device sends its
	// events to, the subscriptions, and whether the client has reported
	// losing events.
	events             *eventServer
	eventsMutex        sync.Mutex
	eventsLost         bool
	eventsRetry        time.Time
	subscriptions      map[string]*subscription
	subscriptionsMutex sync.Mutex
}

// NewClient returns a client for the device at address. The listener
// receives an event after every poll and after every change that the
// client records between polls (announce). Another device can answer at
// an address that was right earlier, so set UUID to make the client
// refuse a device with a different identity.
func NewClient(address string, listener func(equipment.Event)) *Client {
	c := &Client{
		address:       address,
		listener:      listener,
		upnpBase:      upnpBaseFor(address),
		subscriptions: map[string]*subscription{},
		Dial:          (&net.Dialer{}).DialContext,
		Listen:        net.Listen,
		state:         newStatus(),
		reachable:     equipment.ConditionUnknown,
		resync:        true,
	}
	c.upnp = &http.Client{
		Timeout:   requestTimeout,
		Transport: &http.Transport{DisableKeepAlives: true, DialContext: c.dial},
	}
	c.http = &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext:     c.dial,
		},
	}
	return c
}

// dial calls Dial when it is called, not when the client is built, so a
// Dial that a caller sets after NewClient takes effect.
func (c *Client) dial(ctx context.Context, network, address string) (net.Conn, error) {
	return c.Dial(ctx, network, address)
}

// State returns the latest device state in equipment units. It is safe
// to call while Run is running.
func (c *Client) State() equipment.State {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.withDescription(c.state.equipmentState(c.reachable))
}

// Status returns the snapshot of the device's own fields, which the
// controller writes under status.wiim.
func (c *Client) Status() Status {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.state
}

// Address returns the address that the client sends its requests to.
func (c *Client) Address() string { return c.address }

// VolumeResolution is 1, because the device counts whole steps and the
// display shows the same number.
func (c *Client) VolumeResolution() int { return 1 }

// HasStandby is false, because the HTTP API has no power command.
func (c *Client) HasStandby() bool { return false }

// FirstReadDone reports whether a poll has read every family.
func (c *Client) FirstReadDone() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.firstReadDone
}

// Run polls the device until ctx ends. Reachability becomes false after
// pollFailures failed polls in a row and true after the next answered
// poll.
func (c *Client) Run(ctx context.Context) {
	c.startEvents(ctx)
	defer c.stopEvents()
	var waits pollWaits
	for ctx.Err() == nil {
		wait := waits.after(c.poll(ctx))
		if !c.isForeign() {
			c.describe(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// pollWaits chooses the wait before the next poll. The first failed
// poll waits pollInterval, and each further failure in a row doubles
// the wait, up to maxBackoff. An answered poll resets it.
type pollWaits struct {
	next time.Duration
}

func (w *pollWaits) after(answered bool) time.Duration {
	if answered {
		w.next = 0
		return pollInterval
	}
	wait := max(w.next, pollInterval)
	w.next = min(wait*2, maxBackoff)
	return wait
}

// poll reads the device once and publishes the result. It returns false
// when getStatusEx fails or reports a UUID other than the declared one.
//
// Every poll reads getStatusEx and getPlayerStatus. The other reads are
// grouped in families, such as the equalizer or the timers, and a poll
// reads every family only when readsEveryFamily is true. Otherwise it
// reads the track when the player reports a different one, the sleep
// timer while it counts down, and the families that a setter's read
// back left to it (readback.go).
//
// getStatusEx comes first because it reports the device's identity, and
// a poll of a device with the wrong UUID merges nothing and counts a
// miss. The subscriptions open before the other reads, so an event that
// the amp sends during the reads is not lost. A failed read leaves its
// family as it was.
func (c *Client) poll(ctx context.Context) bool {
	c.reads.Lock()
	defer c.reads.Unlock()
	c.mutex.Lock()
	next := c.state
	c.mutex.Unlock()

	device, err := c.readStatusEx(ctx, &next)
	if err != nil {
		c.report(CommandFailed)
		c.miss()
		return false
	}
	if !c.identityMatches(next.Device.UUID) {
		c.report(CommandFailed)
		c.miss()
		return false
	}
	if next.Device.Model != "" && !IsWiiM(next.Device.Model) {
		// Other brands built on the LinkPlay platform answer the same
		// API. The client sends such a device nothing after this read.
		// It counts as a miss, so the device becomes unreachable, and
		// the poll still returns true so that Run waits pollInterval and
		// does not back off. A getStatusEx with no project does not make
		// the device foreign, because it does not show whose device it is.
		c.rejectForeign(next.Device.Model)
		c.miss()
		return true
	}
	if c.answered() {
		// An amp that missed polls may have rebooted, which drops its
		// subscriptions. The amp refuses to renew a SID that it lost, so
		// the client drops that subscription, manageSubscriptions opens
		// a new one, and this poll reads every family. A refused renewal
		// shows a reboot, so the commands that failed while the amp was
		// booting are retried again.
		if c.renewAll(ctx) {
			c.resetReadRetries()
		}
	}
	c.report(CommandOK)

	opened := c.manageSubscriptions(ctx)
	every := c.readsEveryFamily(opened)
	failures := c.readFailures
	playbackBefore := next.Playback
	// Take the write mark and copy the volume, the mute, and the play
	// state right before the player read, not at the start of the poll,
	// because the reads before it can take seconds. publishSince keeps a
	// write after the mark, because it is newer than the player read. The
	// player read replaces a write before the mark (sequence.go).
	c.mutex.Lock()
	mark := c.writes
	next.Playback.Volume = c.state.Playback.Volume
	next.Playback.Mute = c.state.Playback.Mute
	next.Playback.Status = c.state.Playback.Status
	c.mutex.Unlock()
	c.readPlayback(ctx, &next)
	if every {
		c.readSettings(ctx, &next)
	} else {
		switch {
		case trackMoved(playbackBefore, next.Playback):
			c.trackFailures = 0
			c.readNowPlaying(ctx, &next)
		case c.trackFailures > 0 && c.trackFailures < trackRetries:
			c.readNowPlaying(ctx, &next)
		}
		if next.Timers.SleepSeconds > 0 {
			c.readSleep(ctx, &next)
		}
	}
	c.readPending(ctx, &next)

	// firstReadDone is set before the publish, so the event that the publish
	// sends already reports it.
	c.mutex.Lock()
	c.firstReadDone = true
	// When a command failed in a full read, its family keeps its old
	// value, so the next poll reads every family again.
	switch {
	case !every:
	case c.readFailures == failures:
		c.resync = false
		c.settingsRead = time.Now()
	default:
		c.resync = true
	}
	c.readings = readingsFrom(device, next.Network.Interface == "wifi")
	c.mutex.Unlock()
	c.publishSince(next, mark)
	return true
}

// readsEveryFamily reports whether this poll reads every family: on the
// first poll, after a full read in which a command failed, on the first
// poll after the client called the amp unreachable, on a poll that
// opened a subscription, and once settingsResync has passed since the
// last full read. A new subscription can follow missed events, and an
// unreachable amp may have rebooted or been changed. A renewal of a
// live subscription does not count as opening one.
func (c *Client) readsEveryFamily(opened bool) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.resync || opened || !time.Now().Before(c.settingsRead.Add(settingsResync))
}

// trackMoved reports whether the player's report shows a different
// track than before, which is when getMetaInfo can return different
// details.
func trackMoved(before, after Playback) bool {
	return before.Title != after.Title || before.Artist != after.Artist || before.Album != after.Album ||
		before.Mode != after.Mode || before.Vendor != after.Vendor ||
		before.LengthMS != after.LengthMS || before.PlaylistIndex != after.PlaylistIndex
}

func (c *Client) readSettings(ctx context.Context, next *Status) {
	c.readStaticIP(ctx, next)
	c.readNowPlaying(ctx, next)
	c.readAudio(ctx, next)
	c.readEqualizer(ctx, next)
	c.readTimers(ctx, next)
	c.readBluetooth(ctx, next)
	c.readPresets(ctx, next)
	c.readControls(ctx, next)
}

func (c *Client) isForeign() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.foreign != ""
}

// rejectForeign records the project of a device that is not a WiiM and
// calls Foreign when the project changed.
func (c *Client) rejectForeign(project string) {
	c.mutex.Lock()
	changed := c.foreign != project
	c.foreign = project
	c.mutex.Unlock()
	if changed && c.Foreign != nil {
		c.Foreign(project)
	}
}

// miss counts a poll that the device did not answer. After pollFailures
// misses in a row the device is unreachable. Fewer misses keep the last
// answer, so a request that the device drops under load does not look
// like the device leaving.
func (c *Client) miss() {
	c.mutex.Lock()
	c.misses++
	misses := c.misses
	c.mutex.Unlock()
	if misses >= pollFailures {
		c.lose()
	}
}

// resetReadRetries starts the readRetries count again for each command
// that answered before. The client calls it when the amp probably
// rebooted: when it called the amp unreachable, or when the amp refused
// a renewal. A command that failed while the amp was booting may answer
// once the amp is up. A single missed poll does not show a reboot, and a
// reset on it would defeat readRetries for a command that always fails.
// The caller holds reads.
func (c *Client) resetReadRetries() {
	clear(c.failedReads)
	for command := range c.gaveUp {
		if c.answeredOnce[command] {
			delete(c.gaveUp, command)
		}
	}
}

// lose marks the device unreachable. The next poll that the amp answers
// reads every family, because the amp may have rebooted or been changed
// while the client could not reach it. The client keeps its
// subscriptions, because an amp that only lost its network still has
// them, and new ones would leave two more subscriptions on the amp for
// each outage. The first poll that the amp answers renews them
// (renewAll), and an amp that rebooted refuses the renewal, so the
// client subscribes again.
func (c *Client) lose() {
	c.mutex.Lock()
	c.resync = true
	// An amp that does not answer has no current readings, so each gauge
	// leaves the scrape and does not keep its last value.
	c.readings = Readings{}
	c.mutex.Unlock()
	c.resetReadRetries()
	c.record(equipment.ConditionFalse)
}

// answered resets the count of failed polls and reports whether any
// polls had failed since the last answer.
func (c *Client) answered() (missed bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	missed = c.misses > 0
	c.misses = 0
	return missed
}

// identityMatches reports whether the uuid that the device reported is
// the one that the spec declares. With no declared UUID every device
// matches. A device that reported no uuid does not match a declared
// one.
func (c *Client) identityMatches(reported string) bool {
	if c.UUID == "" {
		return true
	}
	if reported == "" {
		return false
	}
	return normalizeUUID(c.UUID) == normalizeUUID(reported)
}

// recordVolume stores the volume that the device just accepted, and
// recordMute stores the mute the same way. Without this, a volume press
// would compute from a poll's reading up to pollInterval old, and two
// presses between polls would both start from the same level. The
// setters call these only after the device accepts the command, so the
// state never shows a refused level. The next poll replaces the value
// with the device's own report, unless a later write wins (sequence.go).
func (c *Client) recordVolume(volume int) {
	c.mutex.Lock()
	previous := c.state
	wasReachable := c.reachable
	c.state.Playback.Volume = volume
	c.wrote(&c.written.volume)
	next := c.state
	c.mutex.Unlock()
	c.announce(previous, next, wasReachable)
}

func (c *Client) recordMute(muted bool) {
	c.mutex.Lock()
	previous := c.state
	wasReachable := c.reachable
	c.state.Playback.Mute = muted
	c.wrote(&c.written.mute)
	next := c.state
	c.mutex.Unlock()
	c.announce(previous, next, wasReachable)
}

// announce sends the listener an event for what changed between two
// states, in the fields of the equipment contract. A poll, a device
// event, a setter's read back, recordVolume, and recordMute all call it,
// so the session sees a volume press the same way as a change made on
// the device.
func (c *Client) announce(previous, next Status, wasReachable equipment.ConditionStatus) {
	state := next.equipmentState(equipment.ConditionTrue)
	before := previous.equipmentState(equipment.ConditionTrue)
	beforeZone := before.Zones[equipment.MainZone]
	afterZone := state.Zones[equipment.MainZone]
	switch {
	case beforeZone.Volume != afterZone.Volume:
		c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventVolume, State: state})
	case beforeZone.Mute != afterZone.Mute:
		c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventMute, State: state})
	case beforeZone.Input != afterZone.Input:
		c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventInput, State: state})
	}
	if wasReachable != equipment.ConditionTrue {
		c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventReachable, State: state})
		return
	}
	c.notify(equipment.Event{Zone: equipment.MainZone, State: state})
}

// record sets reachability and notifies the listener when it changed.
func (c *Client) record(status equipment.ConditionStatus) {
	c.mutex.Lock()
	if c.reachable == status {
		c.mutex.Unlock()
		return
	}
	c.reachable = status
	state := c.state.equipmentState(status)
	c.mutex.Unlock()
	c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventReachable, State: state})
}

func (c *Client) report(status string) {
	if c.Reporter != nil {
		c.Reporter(status)
	}
}

// notify calls the listener on the calling goroutine. During Run that
// is the polling goroutine, so a listener that blocks delays the next
// poll.
func (c *Client) notify(event equipment.Event) {
	if c.listener != nil {
		c.listener(event)
	}
}

// errNoAddress is what a client with no address returns: the spec
// declares none and discovery found none or is off.
var errNoAddress = errors.New("the amp has no address: spec.wiim.address is empty and network discovery found none")

// call sends one GET request and returns the body. An error for a
// non-2xx status includes the response body. A client with no address
// sends nothing, because Go dials an empty host as the local machine,
// and the request would reach whatever listens on port 443 of the node.
func (c *Client) call(ctx context.Context, command string) ([]byte, error) {
	if strings.TrimSpace(c.address) == "" {
		return nil, errNoAddress
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, commandURL(c.address, command), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, fmt.Errorf("%s returned %s: %s", command, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// read sends one command and passes the body to parse. A reply that
// means the device does not support the command or cannot serve the read
// is a miss, not an error, and the family keeps its last value.
func (c *Client) read(ctx context.Context, command string, parse func([]byte)) {
	body, err := c.call(ctx, command)
	if err != nil {
		c.readFailed(command, err)
		return
	}
	if c.answeredOnce == nil {
		c.answeredOnce = map[string]bool{}
	}
	c.answeredOnce[command] = true
	delete(c.failedReads, command)
	delete(c.gaveUp, command)
	miss := isMiss(body)
	c.mutex.Lock()
	if miss {
		if c.unread == nil {
			c.unread = map[string]bool{}
		}
		c.unread[command] = true
	} else {
		delete(c.unread, command)
	}
	c.mutex.Unlock()
	if miss {
		return
	}
	parse(body)
}

// readFailed handles one failed request of a read. A command that
// answered before counts in readFailures, so the next poll reads every
// family again, until the command has failed in readRetries full reads
// in a row. After that, and at once for a command that has never
// answered, readFailed logs the failure and adds the command to gaveUp.
// A command that has never answered is probably one that the model does
// not support, and the receiver settings rules make its field
// Unsupported. readFailed logs nothing more for a command in gaveUp
// until the command answers or resetReadRetries removes it.
func (c *Client) readFailed(command string, err error) {
	if c.gaveUp[command] {
		return
	}
	if c.answeredOnce[command] {
		if c.failedReads == nil {
			c.failedReads = map[string]int{}
		}
		c.failedReads[command]++
		if c.failedReads[command] < readRetries {
			c.readFailures++
			return
		}
	}
	if c.gaveUp == nil {
		c.gaveUp = map[string]bool{}
	}
	c.gaveUp[command] = true
	fmt.Fprintf(os.Stderr, "wiim: reading %s from %s: %v. The client logs no more errors for this command until it answers\n", command, c.address, err)
}

// isMiss reports whether a body is the device's reply for a command it
// does not support or a read it cannot serve.
func isMiss(body []byte) bool {
	text := strings.TrimSpace(string(body))
	return text == "" || text == "unknown command" || text == "Failed"
}

// readStatusEx reads getStatusEx into next and returns the raw block
// for readingsFrom. A poll cannot continue without it, so it returns an
// error where the other reads ignore failures.
func (c *Client) readStatusEx(ctx context.Context, next *Status) (statusEx, error) {
	body, err := c.call(ctx, statusCommand)
	if err != nil {
		return statusEx{}, err
	}
	var device statusEx
	if err := json.Unmarshal(body, &device); err != nil {
		return statusEx{}, fmt.Errorf("parsing the reply of %s, %q: %w", statusCommand, strings.TrimSpace(string(body)), err)
	}
	applyStatusEx(next, device)
	return device, nil
}

func (c *Client) readName(ctx context.Context, next *Status) {
	_, _ = c.readStatusEx(ctx, next)
}

func (c *Client) readStaticIP(ctx context.Context, next *Status) {
	c.read(ctx, staticIPCommand, func(body []byte) {
		var info staticIPInfo
		if json.Unmarshal(body, &info) != nil {
			return
		}
		next.Network.Static = &StaticIP{
			WiFi: StaticAddress{
				Address: info.WlanStaticIP, Gateway: info.WlanGateWay,
				DNS: info.WlanDNSServer, Enabled: info.WlanStaticIPEnable == 1,
			},
			Ethernet: StaticAddress{
				Address: info.EthStaticIP, Gateway: info.EthGateWay,
				DNS: info.EthDNSServer, Enabled: info.EthStaticIPEnable == 1,
			},
		}
	})
}

func (c *Client) readPlayback(ctx context.Context, next *Status) {
	c.read(ctx, playerCommand, func(body []byte) {
		var player playerStatus
		if json.Unmarshal(body, &player) != nil {
			return
		}
		applyPlayback(next, player)
	})
}

// readNowPlaying reads the current track with getMetaInfo. A source
// with no track answers "Failed", and the track is cleared so it does
// not stay out of date.
//
// A failed request also clears the track and counts in trackFailures,
// and the next polls send getMetaInfo again, up to trackRetries
// requests in all. So a lost request leaves the track empty until a
// retry succeeds, not until the next track change. Any answer ends the
// retries, whatever title it has. On line-in, optical, and internet
// radio the player and getMetaInfo report different titles, so a poll
// that waited for them to match would send getMetaInfo every time.
func (c *Client) readNowPlaying(ctx context.Context, next *Status) {
	body, err := c.call(ctx, metaCommand)
	if err != nil {
		c.trackFailures++
		next.NowPlaying = nil
		return
	}
	c.trackFailures = 0
	var meta metaInfo
	if isMiss(body) || json.Unmarshal(body, &meta) != nil {
		next.NowPlaying = nil
		return
	}
	next.NowPlaying = &NowPlaying{
		Title:      meta.MetaData.Title,
		Subtitle:   meta.MetaData.Subtitle,
		Artist:     meta.MetaData.Artist,
		Album:      meta.MetaData.Album,
		ArtURI:     meta.MetaData.AlbumArtURI,
		SampleRate: intOr(meta.MetaData.SampleRate, 0),
		BitDepth:   intOr(meta.MetaData.BitDepth, 0),
		BitRate:    intOr(meta.MetaData.BitRate, 0),
		TrackID:    meta.MetaData.TrackID,
	}
}

func (c *Client) readAudio(ctx context.Context, next *Status) {
	c.read(ctx, outputModeCommand, func(body []byte) {
		var mode outputMode
		if json.Unmarshal(body, &mode) != nil {
			return
		}
		next.Audio.Output.Hardware = mode.Hardware
		next.Audio.Output.Mode = intOr(mode.Hardware, 0)
		next.Audio.Output.Source = mode.Source == "1"
		next.Audio.Output.AudioCast = mode.AudioCast == "1"
	})
	c.read(ctx, outputListCommand, func(body []byte) {
		var list []outputModeListEntry
		if json.Unmarshal(body, &list) != nil {
			return
		}
		available := make([]OutputMode, 0, len(list))
		for _, entry := range list {
			available = append(available, OutputMode{
				Index:       entry.Index,
				Mode:        entry.Mode,
				Device:      entry.SoundCard.DeviceName,
				CardID:      entry.SoundCard.CardID,
				BitDepths:   entry.SoundCard.BitDepths,
				SampleRates: entry.SoundCard.SampleRates,
			})
		}
		next.Audio.Output.Available = available
	})
	c.read(ctx, activeOutputCommand, func(body []byte) {
		var active activeOutput
		if json.Unmarshal(body, &active) != nil {
			return
		}
		next.Audio.Output.Name = active.DeviceName
		next.Audio.Output.CardID = active.CardID
	})
	c.readBalance(ctx, next)
	c.read(ctx, subCommand, func(body []byte) {
		var sub subwoofer
		if json.Unmarshal(body, &sub) != nil {
			return
		}
		next.Audio.Subwoofer = &Subwoofer{
			Plugged:    sub.Plugged == 1,
			OutputMode: sub.OutputMode,
			Crossover:  sub.Cross,
			Phase:      sub.Phase,
			Level:      sub.Level,
			Delay:      sub.SubDelay,
			MainFilter: sub.MainFilter == 1,
			SubFilter:  sub.SubFilter == 1,
			MixSub:     sub.MixSub == 1,
		}
	})
	c.read(ctx, spdifCommand, func(body []byte) {
		if value, ok := atoi(string(body)); ok {
			next.Audio.SPDIFDelayMS = value
		}
	})
	c.read(ctx, remoteStepCommand, func(body []byte) {
		var steps stepAnswer
		if json.Unmarshal(body, &steps) == nil {
			next.Audio.RemoteVolumeStep = steps.RemoteVolumeStep
		}
	})
	c.read(ctx, buttonStepCommand, func(body []byte) {
		var steps stepAnswer
		if json.Unmarshal(body, &steps) == nil {
			next.Audio.ButtonVolumeStep = steps.ButtonVolumeStep
		}
	})
	c.read(ctx, playModeEnable, func(body []byte) {
		if value, ok := atoi(string(body)); ok {
			next.Audio.PlayModeVolumeEnabled = value == 1
		}
	})
	c.read(ctx, playModeCommand, func(body []byte) {
		var modes []perInputVolume
		if json.Unmarshal(body, &modes) != nil {
			return
		}
		volumes := make([]InputVolume, 0, len(modes))
		for _, mode := range modes {
			volumes = append(volumes, InputVolume{
				Mode:   mode.Mode,
				Name:   mode.Name,
				Volume: mode.Volume,
				Stored: mode.Stored == 1,
				Active: mode.Active == 1,
			})
		}
		next.Audio.PlayModeVolumes = volumes
	})
}

func (c *Client) readBalance(ctx context.Context, next *Status) {
	c.read(ctx, balanceCommand, func(body []byte) {
		if value, ok := atof(string(body)); ok {
			next.Audio.Balance = value
		}
	})
}

func (c *Client) readEqualizer(ctx context.Context, next *Status) {
	c.read(ctx, eqStatCommand, func(body []byte) {
		var answer eqAnswer
		if json.Unmarshal(body, &answer) == nil && answer.EQStat != "" {
			next.Equalizer.Enabled = answer.EQStat == "On"
		}
	})
	c.read(ctx, eqListCommand, func(body []byte) {
		var presets []string
		if json.Unmarshal(body, &presets) == nil {
			next.Equalizer.Presets = presets
		}
	})
}

func (c *Client) readTimers(ctx context.Context, next *Status) {
	c.readSleep(ctx, next)
	c.read(ctx, powerModeCommand, func(body []byte) {
		var mode powerModeTime
		if json.Unmarshal(body, &mode) != nil {
			return
		}
		next.Timers.PowerMode = PowerMode{
			Idle:       intOr(mode.IdleInterval, 0),
			Standby:    intOr(mode.StandbyInterval, 0),
			Sleep:      intOr(mode.SleepInterval, 0),
			PowerOff:   intOr(mode.PowerOffInterval, 0),
			SemiActive: intOr(mode.SemiActiveInterval, 0),
		}
	})
	alarms := make([]Alarm, 0, 3)
	for index := 0; index < 3; index++ {
		slot := index
		c.read(ctx, alarmCommand+":"+strconv.Itoa(slot), func(body []byte) {
			var alarm alarmClock
			if json.Unmarshal(body, &alarm) != nil || alarm.Enable == "" {
				return
			}
			alarms = append(alarms, Alarm{
				Index:     slot,
				Enabled:   alarm.Enable == "1",
				Trigger:   intOr(alarm.Trigger, 0),
				Operation: intOr(alarm.Operation, 0),
				Time:      alarm.Time,
				Day:       alarm.Day,
				Path:      alarm.Path,
			})
		})
	}
	next.Timers.Alarms = alarms
}

func (c *Client) readSleep(ctx context.Context, next *Status) {
	c.read(ctx, shutdownCommand, func(body []byte) {
		if seconds, ok := atoi(string(body)); ok {
			next.Timers.SleepSeconds = seconds
		}
	})
}

func (c *Client) readBluetooth(ctx context.Context, next *Status) {
	c.read(ctx, btPairCommand, func(body []byte) {
		var pair btPairStatus
		if json.Unmarshal(body, &pair) == nil {
			next.Bluetooth.PairStatus = pair.Result
		}
	})
	c.read(ctx, btHistoryCommand, func(body []byte) {
		var history btHistory
		if json.Unmarshal(body, &history) != nil {
			return
		}
		paired := make([]PairedDevice, 0, len(history.List))
		for _, device := range history.List {
			paired = append(paired, PairedDevice{Name: device.Name, Address: device.Address, Role: device.Role})
		}
		next.Bluetooth.Paired = paired
	})
}

func (c *Client) readPresets(ctx context.Context, next *Status) {
	c.read(ctx, presetCommand, func(body []byte) {
		var info presetInfo
		if json.Unmarshal(body, &info) != nil {
			return
		}
		presets := make([]Preset, 0, len(info.PresetList))
		for _, preset := range info.PresetList {
			presets = append(presets, Preset{Number: preset.Number, Name: preset.Name, Source: preset.Source})
		}
		next.Presets = presets
	})
}

func (c *Client) readControls(ctx context.Context, next *Status) {
	c.read(ctx, ledCommand, func(body []byte) {
		if value, ok := atoi(string(body)); ok {
			next.Controls.LED = value == 1
		}
	})
	c.read(ctx, buttonsCommand, func(body []byte) {
		if value, ok := atoi(string(body)); ok {
			next.Controls.Buttons = value == 1
		}
	})
	c.read(ctx, cecCommand, func(body []byte) {
		if value, ok := atoi(string(body)); ok {
			next.Controls.CECPower = value == 1
		}
	})
}

// applyStatusEx copies the getStatusEx fields into the snapshot.
func applyStatusEx(s *Status, d statusEx) {
	s.Device.Name = d.DeviceName
	s.Device.Group = d.GroupName
	s.Device.Model = d.Project
	s.Device.Firmware = d.Firmware
	s.Device.Build = d.Build
	s.Device.Released = d.Release
	s.Device.Hardware = d.Hardware
	s.Device.BoardVersion = d.PCBVersion
	s.Device.Uboot = d.UbootVer
	s.Device.MCU = d.MCUVer
	s.Device.HDMI = d.HDMIVer
	s.Device.UUID = normalizeUUID(d.UUID)
	s.Device.TempUUID = d.TempUUID
	s.Device.UPnPUUID = d.UPnPUUID
	s.Device.MACs = MACs{WiFi: d.MAC, Bluetooth: d.BTMAC, AccessPoint: d.APMAC, Ethernet: d.ETHMAC}
	s.Device.Internet = d.Internet == "1"
	s.Device.Initialized = d.InitialConfiguration == "1"
	s.Device.PrivacyMode = d.PrivacyMode == "1"
	s.Device.Language = d.Language
	s.Device.Timezone = d.TimezoneID
	s.Device.TimezoneOffset = d.Timezone
	s.Device.CommunicationPort = d.CommunicationPort
	s.Device.Capabilities = Capabilities{
		Cast:          d.CastEnable == "1",
		PresetKeys:    intOr(d.PresetKey, 0),
		MQTT:          d.MQTTSupport == "1",
		AudioCast:     d.AudioCast == "1",
		EQSupport:     d.EQSupport,
		EQVersion:     d.EQVersion,
		MaxVolume:     intOr(d.MaxVolume, 0),
		VolumeControl: intOr(d.VolumeControl, 0),
	}
	s.Device.Security = Security{
		Protocol: d.Security,
		Version:  d.SecurityVersion,
		AES:      d.SecurityCapabilities.AESVer,
	}
	if d.Eth0 != "" && d.Eth0 != "0.0.0.0" {
		s.Network.Interface = "ethernet"
		s.Network.Address = d.Eth0
	} else if d.Apcli0 != "" && d.Apcli0 != "0.0.0.0" {
		s.Network.Interface = "wifi"
		s.Network.Address = d.Apcli0
	}
	s.Network.SSID = decodeHex(d.Essid)
	s.Network.BSSID = d.BSSID
	s.Network.Channel = intOr(d.WifiChannel, 0)
	s.Network.Frequency = intOr(d.WlanFreq, 0)
	s.Audio.MaxVolume = intOr(d.MaxVolume, equipment.Unknown)
	s.Bluetooth.Remote = &BLERemote{
		Supported: d.BleRemoteControl == "1",
		Connected: d.BleRemoteConnected == "1",
		Version:   d.BleRemoteVersion,
		State:     d.BleRemoteState,
	}
}

// applyPlayback copies the getPlayerStatus fields into the snapshot.
// The player sends the title, artist, and album as hex-encoded text.
func applyPlayback(s *Status, p playerStatus) {
	mode, _ := atoi(p.Mode)
	s.Playback = Playback{
		Status:        p.Status,
		Mode:          mode,
		Source:        inputForMode(mode),
		Loop:          intOr(p.Loop, 0),
		EQPreset:      intOr(p.EQ, 0),
		Vendor:        p.Vendor,
		Channel:       intOr(p.Channel, 0),
		Type:          intOr(p.Type, 0),
		Volume:        intOr(p.Volume, equipment.Unknown),
		Mute:          p.Mute == "1",
		LengthMS:      intOr(p.TotalLength, 0),
		AlarmFlag:     p.AlarmFlag == "1",
		PlaylistCount: intOr(p.PlaylistCount, 0),
		PlaylistIndex: intOr(p.PlaylistIndex, 0),
		Title:         decodeHex(p.Title),
		Artist:        decodeHex(p.Artist),
		Album:         decodeHex(p.Album),
	}
	s.Equalizer.Preset = s.Playback.EQPreset
}

// intOr parses a number that the device sent as a string and returns
// fallback when the string is not a number.
func intOr(value string, fallback int) int {
	if number, ok := atoi(value); ok {
		return number
	}
	return fallback
}

// SetPower does nothing for on and returns an error for off, because
// the API has no power command and the amp has no standby. Starting and
// stopping playback belongs to the media session's transport.
func (c *Client) SetPower(zone string, on bool) error {
	if zone != equipment.MainZone {
		return unknownZone(zone)
	}
	if on {
		return nil
	}
	return fmt.Errorf("a WiiM has no standby command, so it cannot power off")
}

func (c *Client) SetInput(zone, input string) error {
	if zone != equipment.MainZone {
		return unknownZone(zone)
	}
	mode, ok := modeForInput(input)
	if !ok {
		return fmt.Errorf("unknown input %q, want one of line-in, bluetooth, optical, hdmi, usb, udisk, or wifi", input)
	}
	return c.send(context.Background(), setInputPrefix+mode)
}

// SetVolume sets the volume in whole steps. A value outside the
// device's 0 to 100 scale is clamped to it and not refused.
func (c *Client) SetVolume(zone string, steps int) error {
	if zone != equipment.MainZone {
		return unknownZone(zone)
	}
	if steps < 0 {
		steps = 0
	}
	if steps > volumeStepMax {
		steps = volumeStepMax
	}
	if err := c.send(context.Background(), setVolumePrefix+strconv.Itoa(steps)); err != nil {
		return err
	}
	c.recordVolume(steps)
	return nil
}

func (c *Client) SetMute(zone string, muted bool) error {
	if zone != equipment.MainZone {
		return unknownZone(zone)
	}
	value := "0"
	if muted {
		value = "1"
	}
	if err := c.send(context.Background(), setMutePrefix+value); err != nil {
		return err
	}
	c.recordMute(muted)
	return nil
}

// SetSoundMode always fails, because the protocol has no sound mode.
func (c *Client) SetSoundMode(zone, mode string) error {
	return fmt.Errorf("a WiiM has no sound mode")
}

// SameSoundMode compares the two strings, because a WiiM reports no
// sound mode and the contract still needs the method.
func (c *Client) SameSoundMode(declared, reported string) bool {
	return declared == reported
}

// SetSleep sets the sleep timer in minutes. Zero cancels it. When the
// timer reaches zero the amp pauses playback, and it does not enter
// standby. SetSleep reads the timer back after the send, because a poll
// reads it only while the last read found it running.
func (c *Client) SetSleep(zone string, minutes int) error {
	if zone != equipment.MainZone {
		return unknownZone(zone)
	}
	if minutes < 0 || minutes > maxSleepMinutes {
		return fmt.Errorf("sleep of %d minutes is outside 0 to %d", minutes, maxSleepMinutes)
	}
	seconds := "-1"
	if minutes > 0 {
		seconds = strconv.Itoa(minutes * 60)
	}
	if err := c.send(context.Background(), setShutdownPrefix+seconds); err != nil {
		return err
	}
	c.readBack(context.Background(), familySleep)
	return nil
}

func unknownZone(zone string) error {
	return fmt.Errorf("unknown zone %q, want %q", zone, equipment.MainZone)
}

// send sends one setter GET request. The device replies OK, and any
// other reply is an error that includes the device's text.
func (c *Client) send(ctx context.Context, command string) error {
	body, err := c.call(ctx, command)
	if err != nil {
		c.report(CommandFailed)
		return err
	}
	if isMiss(body) {
		c.report(CommandFailed)
		return fmt.Errorf("%s returned %s", command, strings.TrimSpace(string(body)))
	}
	c.report(CommandOK)
	return nil
}
