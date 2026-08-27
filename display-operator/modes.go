package main

// Choosing the mode a screen runs.
//
// What a claim states, how the operator makes it true, and what
// it costs. A mode arrives as an opaque parameter on the claim, the
// same channel the audio operator reads a codec on. Weston parses
// weston.ini once at startup, so the only way to change a mode is to
// write the file and restart the compositor, and that ends every
// Wayland client on every output of the card.
//
// The record is a small JSON file beside weston.ini in the
// pod's own volume, one entry per connector. weston.ini is always
// derived from the connector walk plus the record, and never parsed
// back, so the operator holds one writer and one format to read.
//
// What survives what. The volume is the pod's own, so a
// compositor restart keeps the record and a pod restart erases it. A
// machine that comes up with no consumer left runs every screen at
// the mode its monitor prefers, which is what an unclaimed screen
// should run.
//
// Weston falls back to the preferred mode silently when it
// cannot match the name in the config, with no log line and no failed
// exit. So the operator validates before it writes and reads the mode
// back after, and trusts neither the log nor the exit status.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The one key this driver reads out of a claim's opaque
// parameters. The value is a resolution name, with a refresh after
// an @ when the claim cares which rate the name runs at: 1280x720,
// or 3840x1600@24. The refresh is a whole number of hertz, because
// weston parses it as an integer.
const modeParameter = "mode"

// What weston.ini states for a connector the record says
// nothing about. Weston reads the monitor's own preference then, which
// is what an unclaimed screen should run.
const preferredMode = "preferred"

// ModeSwitchTimeout bounds the wait for the screen to come back
// at the requested mode. The drill measured 134 milliseconds of
// compositor startup and about a second of kubelet turnaround, so ten
// seconds has room. A timeout fails the prepare, and the kubelet's
// retry starts a fresh wait.
const modeSwitchTimeout = 10 * time.Second

// ModeSwitchInterval is how often that wait reads the socket
// and the card again. Neither a compositor's return nor a mode change
// raises an event a program can wait on, so the wait polls, and a
// quarter second adds little to the second the restart already takes.
const modeSwitchInterval = 250 * time.Millisecond

// AllocatedConfig is one entry of the configuration the scheduler
// resolved for an allocation.
//
// An opaque block is DRA's channel for a driver's own
// parameters: the scheduler validates none of it and copies it into
// the allocation unread. An entry with no requests applies to every
// request in the claim, and the source says whether the claim's author
// wrote the entry or the DeviceClass carried it in.
type AllocatedConfig struct {
	Source   string              `json:"source"`
	Requests []string            `json:"requests"`
	Opaque   *OpaqueDeviceConfig `json:"opaque"`
}

// The two places a resolved config entry comes from: the claim
// its author wrote, and the DeviceClass the claim allocates through.
//
// The claim's own choice wins over the class's, because a class
// is the cluster's default and a claim is the workload's say. The
// precedence reads the source field rather than the list's order,
// because the order is the allocator's habit and no API promises it.
const (
	configFromClaim = "FromClaim"
	configFromClass = "FromClass"
)

// OpaqueDeviceConfig is one driver's own parameters inside a claim.
//
// The driver name decides whose parameters these are. A claim
// that pairs two drivers, a screen with that screen's speakers, holds
// one block per driver, and each driver reads only its own.
type OpaqueDeviceConfig struct {
	Driver     string          `json:"driver"`
	Parameters json.RawMessage `json:"parameters"`
}

// ModeChoice is one mode a config entry stated, and whether the
// claim stated it or the class did.
type modeChoice struct {
	Mode      string
	FromClaim bool
}

// ModeSelection is the mode each request asks for. The requests
// map holds what a block that names its requests stated, and every
// holds what a block with no requests stated, which applies to every
// request in the claim.
type modeSelection struct {
	requests map[string]modeChoice
	every    modeChoice
}

// State records one entry's mode, and refuses to let a class's
// entry overwrite the claim's own.
//
// A later entry of the same source overwrites an earlier one,
// the plain reading of a list. An entry from the class never
// overwrites one from the claim, whatever the order.
func (s *modeSelection) state(request string, choice modeChoice) {
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

// ForRequest answers what one allocation result must run.
//
// Two rules, applied in this order: the claim's own choice
// beats the class's, and within one source a block that names the
// request beats a block that names none. So a claim's every-request
// block still beats a class block that names this request.
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

// ClaimModes reads this driver's own config blocks out of the
// configuration the scheduler resolved for an allocation.
//
// A block of another driver is not this driver's to judge, so
// it is skipped. A parameter this driver does not read fails whichever
// source wrote it: a typo in cluster policy drives the wrong mode as
// surely as a typo in a claim.
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
			selection.state("", choice)
			continue
		}
		for _, request := range entry.Requests {
			selection.state(request, choice)
		}
	}
	return selection, nil
}

// ModeParameters reads one opaque block's parameters.
//
// An unknown key fails instead of being ignored, because a
// silently dropped typo would leave the screen at a mode nobody asked
// for with nothing said anywhere.
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
		// A control parameter is this block's too, and
		// controlParameters is the parser that reads it. Both parsers
		// judge an unknown key against the one list of what this
		// driver reads, so a typo fails from either side and a real
		// key is never mistaken for one.
		if key != modeParameter {
			continue
		}
		if err := json.Unmarshal(value, &mode); err != nil {
			return "", fmt.Errorf("the claim's %s parameter is not a string: %s", modeParameter, value)
		}
	}
	return mode, nil
}

// A requestedMode is a claim's mode taken apart: the name every
// mode list here speaks, and the refresh. A refresh of zero means
// the claim stated none, and any refresh of that name will do.
type requestedMode struct {
	Name    string
	Refresh int
}

// ParseMode takes a claim's mode apart. A refresh that is not a
// whole number is refused here, because weston reads 59.94 as 59,
// matches nothing, and falls back with no log line and no failed
// exit, so this refusal is the only signal a person would get.
func parseMode(mode string) (requestedMode, error) {
	name, refresh, stated := strings.Cut(mode, "@")
	if !stated {
		return requestedMode{Name: mode}, nil
	}
	value, err := strconv.Atoi(refresh)
	if err != nil || value <= 0 {
		return requestedMode{}, fmt.Errorf(
			"the mode %q states the refresh %q, and a refresh is a whole number of hertz", mode, refresh)
	}
	return requestedMode{Name: name, Refresh: value}, nil
}

// ModeMatches compares a claim's mode against the card's readback.
// A claim that stated no refresh matches whichever refresh the card
// runs under that name, because the claim left that choice to
// weston.
func modeMatches(requested, current string) bool {
	if current == "" {
		return false
	}
	if requested == current {
		return true
	}
	name, _, stated := strings.Cut(requested, "@")
	if stated {
		return false
	}
	currentName, _, _ := strings.Cut(current, "@")
	return name == currentName
}

// ValidateMode compares a claim's mode against the connector's
// live list from the kernel, never against the published attribute:
// the attribute stops at 64 characters and carries no refresh, so
// it advertises and the ioctl is the truth. One name can appear in
// several entries, split apart by the kernel's aspect-ratio flags,
// and the claim passes when any of them matches. The failure names
// what does exist, refreshes when the name was right and names when
// it was not, because the error is the one place a person reads the
// list.
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
		return fmt.Errorf("%s does not offer the mode %q; it offers %s at %s",
			connector, mode, requested.Name, strings.Join(refreshes, " "))
	}
	return fmt.Errorf("%s does not offer the mode %q; it offers %s",
		connector, mode, strings.Join(names, " "))
}

// ReadModeRecord reads the modes the claims on this machine
// have stated, keyed by the connector name sysfs uses.
//
// A file that is not there answers an empty record, because a
// pod that has stated no mode yet has none, and every caller goes on
// to write one. A file that will not parse is an error: this operator
// is the file's only writer, so content it cannot read means something
// else wrote it, and a write that overwrote it would hide that.
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
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return record, nil
}

// WriteModeRecord replaces the record.
//
// The write is atomic, because the compositor's container may
// restart at any moment and its declare container reads this file to
// build the config it starts from.
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

// ApplyMode makes one screen run the mode a claim states.
//
// The order is validate, read back, write, restart, wait. The
// readback before the write is what makes the flow idempotent and the
// kubelet's retry free: a screen already at the requested mode
// delivers with no write and no restart.
//
// One claim at a time holds this. The record is one file for
// every connector, and two prepares that rewrote it at once would
// restart the compositor twice for one config.
func (p *draPlugin) applyMode(ctx context.Context, output Output, mode string) error {
	p.modeSwitches.Lock()
	defer p.modeSwitches.Unlock()

	// Validation reads the connector's own kernel list through the
	// ioctl, never the published attribute and never sysfs. The
	// attribute stops at 64 characters and drops real modes, and
	// sysfs prints names with no refresh beside them, so only the
	// ioctl can judge an @refresh.
	offered, err := p.connectorModes()
	if err != nil {
		return fmt.Errorf("reading the modes %s offers: %w", output.Connector, err)
	}
	if err := validateMode(output.Connector, offered[output.Connector], mode); err != nil {
		return err
	}
	current, err := p.currentModes()
	if err != nil {
		return fmt.Errorf("reading the mode %s runs: %w", output.Connector, err)
	}
	if modeMatches(mode, current[output.Connector]) {
		p.republishSlice()
		return nil
	}

	record, err := readModeRecord(p.recordPath)
	if err != nil {
		return err
	}
	// The restart budget. The config already asks for this mode
	// and a restart already ran, so weston declined the mode, and a
	// second restart would blank every screen on the machine for the
	// same wrong answer.
	if record[output.Connector] == mode && p.restarted[output.Connector] == mode {
		return fmt.Errorf("the compositor declined the mode %s on %s", mode, output.Connector)
	}
	record[output.Connector] = mode
	if err := p.rewriteConfig(record); err != nil {
		return err
	}

	// The connection standing now, read before the restart ends
	// it. The readback below takes no answer from this connection,
	// because this compositor still serves the mode the claim
	// replaced.
	before := p.compositorOutputs().session

	// The blast. The kubelet restarts the container, the new
	// compositor parses the rewritten config, and every client on every
	// output of this card loses its connection. That is the accepted
	// cost of a mode change, and the manual's claim guide states it.
	if err := p.endCompositor(); err != nil {
		return fmt.Errorf("ending the compositor: %w", err)
	}
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

// RepublishSlice runs the operator's own reconcile pass, on every
// prepare that reached the card. The compositor probes the link on
// its way up, and a mode list that grows in that probe raises no
// uevent, so the slice can hold a short list until something reads
// again. A prepare has just read the kernel, which makes it the
// moment to look, and the pass writes only on divergence, so the
// common case costs one read.
func (p *draPlugin) republishSlice() {
	if p.republish == nil {
		return
	}
	p.republish()
}

// RewriteConfig writes the record and regenerates weston.ini
// from the connector walk plus the record.
//
// The record is written first, so a compositor that restarts
// between the two writes reads a config the next call rebuilds from
// the record it already holds.
func (p *draPlugin) rewriteConfig(record map[string]string) error {
	if err := writeModeRecord(p.recordPath, record); err != nil {
		return fmt.Errorf("writing the mode record: %w", err)
	}
	if err := writeWestonConfig(p.configPath, discoverOutputs(p.sysRoot, p.card), record); err != nil {
		return fmt.Errorf("rewriting the compositor's config: %w", err)
	}
	return nil
}

// AwaitMode waits for the compositor that started after the
// restart to serve the requested mode.
//
// The compositor is the source, not the card. The kernel
// syncing a mode on a connector and weston serving canvases at that
// mode are two different facts, and a client draws at the second
// one. The compositor's own wl_output events carry that second fact.
//
// The wait requires a connection newer than the one the
// restart ended, because the compositor on its way out still reports
// the mode the claim replaced. A compositor with a standing
// connection is also a compositor a consumer can connect to, so the
// socket needs no separate check.
func (p *draPlugin) awaitMode(ctx context.Context, connector, mode string, before uint64) error {
	deadline := time.Now().Add(p.switchTimeout)
	for {
		served := p.compositorOutputs()
		if served.session > before && modeMatches(mode, served.modes[connector]) {
			return nil
		}
		if !time.Now().Before(deadline) {
			// The failure names the connector, the mode the
			// claim stated, the budget it had, and the mode the
			// compositor serves instead, because a person reads
			// this line to learn which of the two the screen runs.
			return fmt.Errorf("%s did not report the mode %s within %s; it reports %s",
				connector, mode, p.switchTimeout, reportedMode(served.modes[connector]))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.switchInterval):
		}
	}
}

// What a failure states for a connector the compositor named
// no mode for, which is a compositor that never came back or an
// output it never re-created.
func reportedMode(mode string) string {
	if mode == "" {
		return "no mode"
	}
	return mode
}

// What the compositor reports it serves, and nothing at all
// when this operator holds no connection to one.
func (p *draPlugin) compositorOutputs() servedOutputs {
	if p.served == nil {
		return servedOutputs{}
	}
	return p.served()
}

// ReleaseModes takes the connectors of one ended claim out of
// the record and regenerates the config.
//
// Nothing restarts. The device allocates to one claim at a
// time, and a revert would restart every screen on the machine to
// serve nobody. The screen keeps the mode until the next compositor
// start, which comes up at the mode the monitor prefers, and the
// slice's currentMode says what it runs meanwhile.
// restartCompositor ends the compositor with no config change,
// the restart half of the path a mode prepare takes. It holds the
// same lock, so a restart and a mode switch never run at once: a
// restart in the middle of a switch would end the compositor the
// switch is waiting on and fail the prepare.
func (p *draPlugin) restartCompositor() error {
	p.modeSwitches.Lock()
	defer p.modeSwitches.Unlock()

	return p.endCompositor()
}

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
		// The restart this operator ordered is forgotten with
		// the entry, so the next claim that states this mode gets its
		// own restart and its own budget.
		delete(p.restarted, connector)
		released = true
	}
	if !released {
		return nil
	}
	return p.rewriteConfig(record)
}
