package main

// A claim can ask for a screen mode in its opaque parameters. Weston
// reads weston.ini only at startup, so a mode change rewrites the file
// and restarts the compositor, which ends every Wayland client on every
// output of the card.
//
// The operator keeps the requested modes in modes.json beside
// weston.ini, one entry per connector. It builds weston.ini from the
// connector list and that record, and never parses weston.ini back.
//
// Both files are in an emptyDir, so the record survives a compositor
// restart and is deleted with the pod. The declare container of a new
// pod writes each Display's declared mode into the record. A monitor
// whose Display sets no mode starts at the mode the monitor prefers.
//
// When weston cannot match the mode name in the config, it falls back
// to the preferred mode with no log line and no failed exit. So the
// operator checks the mode against the kernel's list before it writes
// the config, and reads the mode back from the compositor afterward.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// modeParameter is the key of a claim's opaque parameters that holds
// the mode: a resolution name, optionally followed by @ and a refresh
// rate, as in 1280x720 or 3840x1600@24. The refresh is a whole number
// of hertz, because weston parses it as an integer.
const modeParameter = "mode"

// preferredMode is the mode that weston.ini sets for a connector with
// no entry in the record, so weston uses the monitor's preferred mode.
const preferredMode = "preferred"

// modeSwitchTimeout is how long the restarted compositor has to report
// the requested mode, counted from the first connection to it. After
// that, the compositor has declined the mode.
const modeSwitchTimeout = 10 * time.Second

// modeSwitchFallback is the longest a mode switch waits before it reads
// the compositor's outputs again. A switch usually wakes sooner, when
// the output watch closes its changed channel. The timer covers the
// case where no output watch is connected and changed is nil.
const modeSwitchFallback = time.Second

// compositorReturnLimit is how long a mode switch waits for the
// restarted compositor to accept a connection. After the operator ends
// weston, the compositor container starts it again at once. When weston
// crashes instead, the kubelet delays the next start with a crash
// backoff of up to five minutes. The limit is longer than that, so a
// compositor in backoff is slow and has not declined the mode. A
// compositor that is not back by the limit fails the switch with an
// error that is not errModeDeclined.
const compositorReturnLimit = 6 * time.Minute

// errModeDeclined means the compositor restarted and still reports a
// different mode, so another restart would give the same result. The
// Display pass records the mode in status.unconfirmed and does not try
// that mode again.
var errModeDeclined = errors.New("the compositor declined the mode")

// AllocatedConfig is one entry of the configuration that the scheduler
// resolved for an allocation. The scheduler copies the opaque block
// into the allocation without validating it. An entry with no requests
// applies to every request in the claim.
type AllocatedConfig struct {
	Source   string              `json:"source"`
	Requests []string            `json:"requests"`
	Opaque   *OpaqueDeviceConfig `json:"opaque"`
}

// The values of AllocatedConfig.Source. A claim's entry takes
// precedence over the DeviceClass's, because the class sets the
// cluster's default and the claim sets one workload's choice. The code
// reads Source and not the order of the list, because the allocator
// does not promise an order.
const (
	configFromClaim = "FromClaim"
	configFromClass = "FromClass"
)

// OpaqueDeviceConfig is one driver's parameters inside a claim. A claim
// for a screen and its speakers has a block for each driver, and each
// driver reads only its own.
type OpaqueDeviceConfig struct {
	Driver     string          `json:"driver"`
	Parameters json.RawMessage `json:"parameters"`
}

type modeChoice struct {
	Mode      string
	FromClaim bool
}

// modeSelection is the modes that a claim's config sets. requests has
// the modes of entries that name their requests, and every has the
// mode of an entry with no requests.
type modeSelection struct {
	requests map[string]modeChoice
	every    modeChoice
}

// set records one entry's mode. A later entry from the same source
// replaces an earlier one, and a class entry never replaces a claim
// entry, so the claim's mode wins whichever comes first in the list.
func (s *modeSelection) set(request string, choice modeChoice) {
	current := s.every
	if request != "" {
		current = s.requests[request]
	}
	if current.Mode != "" && current.FromClaim && !choice.FromClaim {
		return
	}
	if request == "" {
		s.every = choice
		return
	}
	if s.requests == nil {
		s.requests = map[string]modeChoice{}
	}
	s.requests[request] = choice
}

// forRequest returns the mode for one request. A claim's entry takes
// precedence over a class's entry, even when the claim's entry names no
// requests and the class's entry names this one. Within one source, an
// entry that names the request takes precedence.
func (s modeSelection) forRequest(request string) string {
	named := s.requests[request]
	if named.Mode == "" {
		return s.every.Mode
	}
	if s.every.Mode != "" && s.every.FromClaim && !named.FromClaim {
		return s.every.Mode
	}
	return named.Mode
}

// claimModes reads this driver's blocks from an allocation's config.
// An unknown parameter is an error in a class's block as well as in a
// claim's.
func claimModes(config []AllocatedConfig) (modeSelection, error) {
	selection := modeSelection{}
	for _, entry := range config {
		if entry.Opaque == nil || entry.Opaque.Driver != DriverName {
			continue
		}
		mode, err := modeParameters(entry.Opaque.Parameters)
		if err != nil {
			return modeSelection{}, err
		}
		if mode == "" {
			continue
		}
		choice := modeChoice{Mode: mode, FromClaim: entry.Source == configFromClaim}
		if len(entry.Requests) == 0 {
			selection.set("", choice)
			continue
		}
		for _, request := range entry.Requests {
			selection.set(request, choice)
		}
	}
	return selection, nil
}

// modeParameters returns the mode in one opaque block. An unknown key is
// an error, because an ignored typo would leave the screen at a mode
// nobody asked for, with nothing to report it.
func modeParameters(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var parameters map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parameters); err != nil {
		return "", fmt.Errorf("the claim's %s parameters are not an object: %w", DriverName, err)
	}
	mode := ""
	for key, value := range parameters {
		if !slices.Contains(claimParameterNames, key) {
			return "", unknownParameter(key)
		}
		// The same block can hold the keys that controlParameters
		// reads, so both parsers check keys against
		// claimParameterNames and skip each other's keys.
		if key != modeParameter {
			continue
		}
		if err := json.Unmarshal(value, &mode); err != nil {
			return "", fmt.Errorf("the claim's %s parameter is not a string: %s", modeParameter, value)
		}
	}
	return mode, nil
}

// requestedMode is a claim's mode split into its name and its refresh.
// A refresh of zero means the claim set none, and any refresh of that
// name is acceptable.
type requestedMode struct {
	Name    string
	Refresh int
}

// parseMode rejects a refresh that is not a whole number. Weston would
// read 59.94 as 59, find no match, and fall back to the preferred mode
// with no error, so this error is the only report the person gets.
func parseMode(mode string) (requestedMode, error) {
	name, refresh, hasRefresh := strings.Cut(mode, "@")
	if !hasRefresh {
		return requestedMode{Name: mode}, nil
	}
	value, err := strconv.Atoi(refresh)
	if err != nil || value <= 0 {
		return requestedMode{}, fmt.Errorf(
			"the mode %q has the refresh %q, and a refresh must be a whole number of hertz", mode, refresh)
	}
	return requestedMode{Name: name, Refresh: value}, nil
}

// modeMatches reports whether the current mode satisfies the requested
// mode. A request with no refresh matches any refresh of the same name.
func modeMatches(requested, current string) bool {
	if current == "" {
		return false
	}
	if requested == current {
		return true
	}
	name, _, hasRefresh := strings.Cut(requested, "@")
	if hasRefresh {
		return false
	}
	currentName, _, _ := strings.Cut(current, "@")
	return name == currentName
}

// validateMode checks a claim's mode against the connector's modes from
// the DRM connector ioctl. The kernel can list one name and refresh in
// several entries that differ only in aspect ratio flags. When the mode
// is not offered, the error lists the refreshes of that name, or the
// names when the name is wrong, because no published attribute lists
// the refreshes.
func validateMode(connector string, offered []drmMode, mode string) error {
	requested, err := parseMode(mode)
	if err != nil {
		return err
	}
	if len(offered) == 0 {
		return fmt.Errorf("the card reports no modes for %s right now", connector)
	}
	var names []string
	var refreshes []string
	for _, entry := range offered {
		if !slices.Contains(names, entry.Name) {
			names = append(names, entry.Name)
		}
		if entry.Name != requested.Name {
			continue
		}
		if entry.Refresh == requested.Refresh {
			return nil
		}
		refresh := strconv.Itoa(entry.Refresh)
		if !slices.Contains(refreshes, refresh) {
			refreshes = append(refreshes, refresh)
		}
	}
	if requested.Refresh == 0 && slices.Contains(names, requested.Name) {
		return nil
	}
	if len(refreshes) > 0 {
		return fmt.Errorf("%s does not offer the mode %q, only %s at %s",
			connector, mode, requested.Name, strings.Join(refreshes, " "))
	}
	return fmt.Errorf("%s does not offer the mode %q, only %s",
		connector, mode, strings.Join(names, " "))
}

// readModeRecord reads the requested modes, keyed by the sysfs connector
// name. A missing file is an empty record. A file that does not parse
// is an error, because the operator is its only writer, so the content
// came from something else, and overwriting it would hide that.
func readModeRecord(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	record := map[string]string{}
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("parsing the mode record %s: %w", path, err)
	}
	return record, nil
}

// writeModeRecord replaces the record through a temporary file and a
// rename, so the declare container never reads a partial file.
func writeModeRecord(path string, record map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// applyMode sets one screen to the mode a claim asks for: it validates
// the mode, reads the current mode, writes the config, restarts the
// compositor, and waits for the new mode. It reads the current mode
// before it writes, so a kubelet retry of a prepare that already
// succeeded does no write and no restart.
func (p *draPlugin) applyMode(ctx context.Context, output Output, mode string) error {
	p.modeSwitches.Lock()
	defer p.modeSwitches.Unlock()

	// Validate against the ioctl's list. The published modes attribute
	// stops at 64 characters, and sysfs lists names without refreshes.
	offered, err := p.connectorModes()
	if err != nil {
		return fmt.Errorf("reading the modes of %s: %w", output.Connector, err)
	}
	if err := validateMode(output.Connector, offered[output.Connector], mode); err != nil {
		return err
	}
	current, err := p.currentModes()
	if err != nil {
		return fmt.Errorf("reading the current mode of %s: %w", output.Connector, err)
	}
	if modeMatches(mode, current[output.Connector]) {
		p.republishSlice()
		return nil
	}

	record, err := readModeRecord(p.recordPath)
	if err != nil {
		return err
	}
	// A restart for this mode already ran and the screen is not at it,
	// so weston declined it. Another restart would blank every screen
	// on the card for the same result.
	if record[output.Connector] == mode && p.restarted[output.Connector] == mode {
		return fmt.Errorf("%w %s on %s", errModeDeclined, mode, output.Connector)
	}
	record[output.Connector] = mode
	if err := p.rewriteConfig(record); err != nil {
		return err
	}

	// awaitMode ignores reports from this connection, because the
	// compositor it belongs to still reports the previous mode.
	before := p.compositorOutputs().session

	// A restart blanks every screen on the card, so the log and the
	// Event say which screen's mode change caused it.
	fmt.Printf("%s: changing the mode from %s to %s, which restarts the compositor\n",
		output.Connector, reportedMode(current[output.Connector]), mode)
	p.notices.post(output.Connector, events.TypeNormal, ModeChangedReason,
		fmt.Sprintf("Changed the mode of %s from %s to %s. The compositor restarts, and every screen on the card goes blank",
			output.Connector, reportedMode(current[output.Connector]), mode))
	// The compositor container starts weston again, and the new weston
	// reads the rewritten config.
	if err := p.endCompositors(syscall.SIGTERM); err != nil {
		return fmt.Errorf("ending the compositor: %w", err)
	}
	p.metrics.compositorRestarted("mode")
	if p.restarted == nil {
		p.restarted = map[string]string{}
	}
	p.restarted[output.Connector] = mode
	if err := p.awaitMode(ctx, output.Connector, mode, before); err != nil {
		return err
	}
	p.republishSlice()
	return nil
}

// republishSlice runs the operator's publish pass after a prepare that
// read the card. The compositor probes the link as it starts, and the
// kernel sends no uevent when that probe adds modes, so the published
// slice can keep a short mode list until the next read. The pass writes
// the slice only when it differs.
func (p *draPlugin) republishSlice() {
	if p.republish == nil {
		return
	}
	p.republish()
}

// rewriteConfig writes the record first, so a compositor that restarts
// between the two writes starts on a config that the next call
// rebuilds from the record.
func (p *draPlugin) rewriteConfig(record map[string]string) error {
	if err := writeModeRecord(p.recordPath, record); err != nil {
		return fmt.Errorf("writing the mode record: %w", err)
	}
	if err := writeWestonConfig(p.configPath, discoverOutputs(p.sysRoot, p.card), record); err != nil {
		return fmt.Errorf("rewriting the compositor's config: %w", err)
	}
	return nil
}

// awaitMode waits until the restarted compositor reports the requested
// mode. It reads the mode from the compositor's wl_output events and
// not from the kernel, because a client draws at the mode weston
// reports, which can differ from the mode the kernel set on the
// connector. A new connection also proves that clients can connect, so
// the socket needs no separate check.
//
// First it waits up to compositorReturnLimit for a connection newer
// than before. Then the compositor has p.switchTimeout to report the
// mode. Only a timeout in the second wait returns errModeDeclined,
// because a compositor that is not back yet has not reported any mode.
func (p *draPlugin) awaitMode(ctx context.Context, connector, mode string, before uint64) error {
	if err := p.awaitCompositor(ctx, before); err != nil {
		return err
	}
	deadline := time.NewTimer(p.switchTimeout)
	defer deadline.Stop()
	for {
		served := p.compositorOutputs()
		if served.session > before && modeMatches(mode, served.modes[connector]) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			served = p.compositorOutputs()
			if served.session > before && modeMatches(mode, served.modes[connector]) {
				return nil
			}
			return fmt.Errorf("%w: %s did not report the mode %s within %s, and reports %s",
				errModeDeclined, connector, mode, p.switchTimeout, reportedMode(served.modes[connector]))
		case <-served.changed:
		case <-time.After(p.switchFallback):
		}
	}
}

func (p *draPlugin) awaitCompositor(ctx context.Context, before uint64) error {
	limit := time.NewTimer(compositorReturnLimit)
	defer limit.Stop()
	for {
		served := p.compositorOutputs()
		if served.session > before {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-limit.C:
			return fmt.Errorf("the compositor was not running again within %s of its restart", compositorReturnLimit)
		case <-served.changed:
		case <-time.After(p.switchFallback):
		}
	}
}

// reportedMode formats a mode for a message. An empty mode means the
// compositor is not running or has no output for the connector.
func reportedMode(mode string) string {
	if mode == "" {
		return "no mode"
	}
	return mode
}

func (p *draPlugin) compositorOutputs() servedOutputs {
	if p.served == nil {
		return servedOutputs{}
	}
	return p.served()
}

// restartCompositor ends the compositor with no change to the config.
// displayControl calls it to repair the canvas sizes after an output
// was destroyed and re-created.
func (p *draPlugin) restartCompositor() error {
	return p.restart("heal", func() error { return p.endCompositors(syscall.SIGTERM) })
}

// killHungCompositor sends SIGKILL to a compositor that accepts
// connections on its socket and never responds. The socket watch calls
// it after the probe has reported Hung for compositorHungLimit. The
// probe then reports Down until the compositor container starts weston
// again, and Down already sets the taint and lets clients handle the
// outage.
func (p *draPlugin) killHungCompositor() error {
	if err := p.restart("hung", func() error { return p.endCompositors(syscall.SIGKILL) }); err != nil {
		return err
	}
	p.notices.post("", events.TypeWarning, CompositorKilledReason,
		fmt.Sprintf("Ended the compositor, which did not reply for %s. Its container starts it again", compositorHungLimit))
	return nil
}

// restart ends the compositor and counts the restart under reason. It
// takes the modeSwitches lock, because a restart during a mode switch
// would end the compositor that the switch waits for and fail the
// prepare. When end returns an error, nothing is counted.
func (p *draPlugin) restart(reason string, end func() error) error {
	p.modeSwitches.Lock()
	defer p.modeSwitches.Unlock()

	if err := end(); err != nil {
		return err
	}
	p.metrics.compositorRestarted(reason)
	return nil
}

// releaseModes removes the connectors of an ended claim from the record
// and rewrites the config. It does not restart the compositor, because
// no claim holds the screen any more and a restart would blank every
// screen on the card. The screen keeps its mode until the next
// compositor start, and the slice's currentMode attribute shows the
// mode in use.
func (p *draPlugin) releaseModes(devices []string) error {
	p.modeSwitches.Lock()
	defer p.modeSwitches.Unlock()

	record, err := readModeRecord(p.recordPath)
	if err != nil {
		return err
	}
	released := false
	for connector := range record {
		if !slices.Contains(devices, deviceName(connector)) {
			continue
		}
		delete(record, connector)
		// The next claim for this mode gets its own restart.
		delete(p.restarted, connector)
		released = true
	}
	if !released {
		return nil
	}
	return p.rewriteConfig(record)
}
