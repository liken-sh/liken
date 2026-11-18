package main

// The placement pass decides where each window goes on this node's
// screens. The compositor's layout module reports every surface and
// the Wayland socket it connected on. The pass finds the claim that
// holds each socket, matches the regions of the screen's Layout
// against the labels that every pod of that claim shares, sends the
// placements to the module, and writes the result to each Display's
// status.
//
// placeSurfaces decides the placements as a pure function, and this
// file sends its result, so another layout engine could replace
// placeSurfaces with no change here
// (plans/open-problems/an-external-layout-engine.md).
//
// The pass remembers the last placement it sent for each surface and
// sends nothing for a surface that has not changed, because a commit
// makes the compositor repaint even when nothing changed.

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// claimIDPrefix is the number of characters of a claim's UID that a
// surface id includes. Eight characters are enough for a person to
// tell the claims apart, and the module's surface id makes the whole
// id unique.
const claimIDPrefix = 8

// sharedSurface is the id prefix of a surface on the compositor's own
// socket, which has no claim.
const sharedSurface = "shared"

// screenSurface is a surface that the module reported, with the
// claim, pods, and labels behind the socket it connected on.
type screenSurface struct {
	layoutSurface
	id     string
	claim  string
	pods   []string
	labels map[string]string
}

// placedSurface is the last placement that the pass sent for one
// surface.
//
// exit is the exit transition of the region the surface was placed
// in. The pass hides a surface when it matches no region any more, so
// it reads the transition from here.
type placedSurface struct {
	connector string
	where     rect
	exit      LayoutTransitionHalf
}

type placementPass struct {
	client *apiclient.Client
	// displays and stores read the Displays, the Layouts, and this
	// node's pods from the watch caches (objectcache.go). A pass
	// created with no watch reads them from the API server.
	displays *displayStore
	stores   clusterStores
	node     string
	link     *layoutLink
	claims   *claimIndex
	// outputs lists the card's outputs the same way the slice
	// publisher and the Display controller do, so all three see the
	// same connectors and monitors.
	outputs func() []Output
	// sockets returns the claim and the output device of each socket
	// that a prepared claim holds. A test replaces it to run without a
	// CDI directory.
	sockets func() (map[string]preparedSocket, error)
	// compositor probes the compositor's own socket with the handshake
	// that the slice publisher uses to taint devices. A test replaces
	// it to run without a compositor.
	compositor func() compositorLiveness
	now        func() time.Time

	// generation is the link connection that placed and ordered were
	// sent on.
	generation int
	// placed is the last placement sent for each surface, by the
	// module's surface id, and ordered is the last stacking order sent
	// for each connector.
	placed  map[int]placedSurface
	ordered map[string][]int
	// reconnected is true only during the first pass on a new link
	// connection. That pass posts no Event for an arranger it no
	// longer finds (announceArrangers), because a restarted compositor
	// ends every arranger.
	reconnected bool
	// sweepPending is true while a Display on this node whose monitor
	// is disconnected may have an out-of-date status. Each screen's
	// report writes only the Displays of connected monitors, so such a
	// Display keeps its last surfaces and compositor condition until
	// sweep writes it. It starts true because a new process does not
	// know what the previous process wrote.
	sweepPending bool
	// connectedDisplays is the sorted list of the Displays whose monitors were
	// connected in the last pass. A change means that a monitor left
	// or returned, and sets sweepPending.
	connectedDisplays []string
	// metrics is nil in tests, and a nil metrics records nothing.
	metrics *metrics
}

func newPlacementPass(client *apiclient.Client, node, socketPath string, link *layoutLink, claims *claimIndex,
	outputs func() []Output) *placementPass {
	return &placementPass{
		client:       client,
		displays:     newDisplayStore(client, informer.View{}),
		node:         node,
		link:         link,
		claims:       claims,
		outputs:      outputs,
		sockets:      preparedSocketClaims,
		compositor:   func() compositorLiveness { return probeCompositor(socketPath) },
		now:          time.Now,
		placed:       map[int]placedSurface{},
		ordered:      map[string][]int{},
		sweepPending: true,
	}
}

// pass places every surface that the module reports and writes the
// status of every screen.
//
// When the compositor does not reply to the probe, its surfaces are
// gone, so the pass writes every Display with no surfaces and places
// nothing. When the link to the module is down, the pass does nothing,
// because the module keeps its last committed layout and reports every
// surface again on the next connection.
func (p *placementPass) pass() error {
	live := p.compositor()
	if !live.serving {
		return p.reportNoCompositor(live)
	}
	state := p.link.state()
	if !state.Serving {
		return nil
	}
	outputs := p.outputs()
	if connected := connectedDisplayNames(outputs); !slices.Equal(connected, p.connectedDisplays) {
		p.connectedDisplays, p.sweepPending = connected, true
	}
	var failures []error
	if p.sweepPending {
		if err := p.sweep(live); err != nil {
			failures = append(failures, err)
		} else {
			p.sweepPending = false
		}
	}
	// Without the sockets every surface would have no claim, match no
	// region, and be hidden, so the pass stops here.
	sockets, err := p.sockets()
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	// On a new connection the compositor may have restarted, with new
	// surface ids and none of the placements, so the pass sends every
	// placement again.
	p.reconnected = state.Generation != p.generation
	if p.reconnected {
		p.generation = state.Generation
		p.forget()
	}
	for id, was := range p.placed {
		if _, reported := state.Surfaces[id]; !reported {
			delete(p.placed, id)
			continue
		}
		// When an output goes away, the module hides its surfaces and
		// clears their placements while the clients stay connected.
		// Forgetting the placement makes the pass send it again when
		// the output returns.
		if _, present := state.Outputs[was.connector]; !present {
			delete(p.placed, id)
			delete(p.ordered, was.connector)
		}
	}

	screens, unresolved := p.resolve(state, sockets, outputs)
	failures = append(failures, unresolved...)
	changed := false
	for _, connector := range slices.Sorted(maps.Keys(state.Outputs)) {
		onScreen := screens[connector]
		if onScreen != nil && onScreen.unresolved {
			// A surface whose claim could not be read would match no
			// region and be hidden, so one failed request would remove
			// a program from the screen.
			continue
		}
		on := surfacesOn(onScreen)
		p.metrics.recordSurfaces(connector, len(on))
		sent, err := p.screen(state.Outputs[connector], onScreen, outputs, live)
		changed = changed || sent
		if err != nil {
			failures = append(failures, fmt.Errorf("placing the surfaces on %s: %w", connector, err))
		}
	}
	if changed {
		if err := p.link.Commit(); err != nil {
			// Nothing sent in this pass reached the screen, so the next
			// pass sends everything again.
			p.forget()
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (p *placementPass) forget() {
	p.placed = map[int]placedSurface{}
	p.ordered = map[string][]int{}
}

// reportNoCompositor writes every Display of this node with no
// surfaces, no layout, and no arrangers, and with the
// CompositorServing condition from the failed probe. It lists the
// Displays, not the connectors, so that the Display of a disconnected
// monitor gets the same status.
//
// A compositor that starts again has none of the placements and
// numbers its surfaces from 1 again, so the pass forgets what it sent.
func (p *placementPass) reportNoCompositor(live compositorLiveness) error {
	p.forget()
	p.metrics.forgetSurfaces()
	// No screen report writes the Display of a disconnected monitor,
	// so the first pass that finds the compositor serving again must
	// sweep to clear the condition written here.
	p.sweepPending = true
	return p.settleEach(func(_ string, published DisplayStatus) DisplayStatus {
		status := published
		status.Surfaces = nil
		status.Layout = nil
		status.Arrangers = nil
		status.Conditions = setCondition(status.Conditions, p.serving(live))
		return status
	})
}

// screenSurfaces is what the pass found for one screen. unresolved is
// true when reading a claim from the API server failed, and the pass
// then leaves the screen as it is.
type screenSurfaces struct {
	on         []screenSurface
	arrangers  []screenArranger
	departures map[string]string
	unresolved bool
}

func surfacesOn(screen *screenSurfaces) []screenSurface {
	if screen == nil {
		return nil
	}
	return screen.on
}

// resolve assigns each reported surface to the screen that its socket
// was opened for, with the claim, pods, and labels behind it.
//
// Each screen's surfaces are sorted by the module's id, which is the
// order they connected in, because the module numbers surfaces upward
// for the life of the compositor.
func (p *placementPass) resolve(state layoutState, sockets map[string]preparedSocket,
	outputs []Output) (map[string]*screenSurfaces, []error) {
	var failures []error
	connectors := map[string]string{}
	shared := ""
	for _, output := range outputs {
		connectors[deviceName(output.Connector)] = output.Connector
		if shared == "" && output.Connected {
			shared = output.Connector
		}
	}

	holders := newHolderReader(p.client, p.node, p.claims, p.stores)
	screens := map[string]*screenSurfaces{}
	for _, reported := range state.Surfaces {
		on := screenSurface{layoutSurface: reported, id: surfaceID("", reported.ID)}
		// A surface on the compositor's own socket has no claim to say
		// which screen it is for, so it goes on the first connected
		// output.
		connector, unresolved := shared, false
		if socket, claimed := sockets[reported.Socket]; claimed {
			connector = connectors[socket.device]
			on.id = surfaceID(socket.claim, reported.ID)
			by, err := holders.holders(socket.claim)
			if err != nil {
				failures = append(failures, err)
				unresolved = true
			}
			on.claim, on.pods, on.labels = by.key, by.pods, by.labels
		} else if reported.Socket != socketName {
			// The claim of this socket was released while the client
			// kept drawing. The surface is treated like one on the
			// compositor's own socket.
			failures = append(failures, fmt.Errorf("surface %d is on socket %s, which no prepared claim holds",
				reported.ID, reported.Socket))
		}
		if connector == "" {
			continue
		}
		if screens[connector] == nil {
			screens[connector] = &screenSurfaces{}
		}
		screens[connector].on = append(screens[connector].on, on)
		screens[connector].unresolved = screens[connector].unresolved || unresolved
	}
	failures = append(failures, resolveArrangers(state, sockets, connectors, holders, screens)...)
	for _, screen := range screens {
		slices.SortFunc(screen.on, func(a, b screenSurface) int {
			return cmp.Compare(a.ID, b.ID)
		})
	}
	return screens, failures
}

// screen decides the layout of one screen, sends the changes to the
// module, and writes the status. It returns true when it sent the
// module anything, so that the pass commits.
func (p *placementPass) screen(output layoutOutput, onScreen *screenSurfaces, outputs []Output,
	live compositorLiveness) (bool, error) {
	on := surfacesOn(onScreen)
	display, err := p.display(output.Connector, outputs)
	if err != nil {
		return false, err
	}
	named := ""
	if display != nil {
		named = display.Spec.Layout
	}
	layout, resolved, err := p.layoutOf(named)
	if err != nil {
		return false, err
	}
	decision := placeSurfaces(surfacesOf(on), layout)
	sent, failure := p.send(output, decision, moduleIDs(on))
	if display == nil {
		// The monitor reports no identity, or its Display does not
		// exist yet. The surfaces are placed, and no status is written.
		return sent, failure
	}
	return sent, errors.Join(failure,
		p.report(display, onScreen, decision, layoutName(named, layout), resolved, p.serving(live)))
}

// display returns the Display of the monitor on one connector, or nil
// when the monitor reports no identity or the Display does not exist
// yet. It names the Display from the monitor's EDID with monitorID,
// as the Display controller does. The Display controller creates a
// missing Display, and a later pass writes its status.
func (p *placementPass) display(connector string, outputs []Output) (*Display, error) {
	name := ""
	for _, output := range outputs {
		if output.Connector == connector && output.Connected {
			name = monitorID(output.Monitor)
		}
	}
	if name == "" {
		return nil, nil
	}
	display, err := p.displays.get(name)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil, nil
	}
	return display, err
}

// layoutOf returns the Layout that a screen is drawn with, and the
// LayoutResolved condition. With no layout name, or a name that no
// Layout has, it returns nil, which is the default layout.
//
// Any other read failure fails the screen, because falling back to
// the default would rearrange every surface after one failed API
// request.
func (p *placementPass) layoutOf(named string) (*LayoutSpec, DisplayCondition, error) {
	if named == "" {
		return nil, p.condition(true, DefaultLayoutReason,
			"spec.layout is empty, so every surface fills the whole screen with the newest on top"), nil
	}
	layout, err := p.stores.layout(p.client, named)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil, p.condition(false, LayoutNotFoundReason,
			"The Layout "+named+" that spec.layout names does not exist, so this screen uses the default layout"), nil
	}
	if err != nil {
		return nil, DisplayCondition{}, err
	}
	return &layout.Spec, p.condition(true, LayoutFoundReason, "The screen shows the Layout "+named), nil
}

// send sends the changes in one screen's decision to the module and
// returns true when it sent anything.
//
// The stacking order is sent after any placement or hide, because the
// module puts a surface that is new to an output on top until it
// receives an order. Otherwise it is sent only when it differs from
// the last order sent.
func (p *placementPass) send(output layoutOutput, decision screenPlacement, ids map[string]int) (bool, error) {
	var failures []error
	sent := false
	order := make([]int, 0, len(decision.placed))
	for _, place := range inStackOrder(decision.placed) {
		id, known := ids[place.surface]
		if !known {
			continue
		}
		order = append(order, id)
		where := logicalRect(place.rect, output)
		next := placedSurface{connector: output.Connector, where: where, exit: place.transition.Exit}
		last, before := p.placed[id]
		if before && last.connector == next.connector && last.where == next.where {
			// Nothing is sent, but an edit of the Layout can have
			// changed the region's exit, so the new exit is saved.
			p.placed[id] = next
			continue
		}
		transition, milliseconds := enterTransition(place.transition.Enter, before)
		if err := p.link.Place(id, output.Connector, where, transition, milliseconds); err != nil {
			failures = append(failures, err)
			continue
		}
		p.placed[id] = next
		sent = true
	}
	// A surface that no region matches any more is hidden with the
	// exit of the region it was in. A surface that the module reported
	// gone lost its saved placement at the start of the pass, so it
	// gets no hide.
	for _, unplaced := range decision.unplaced {
		id, known := ids[unplaced]
		if !known {
			continue
		}
		last, before := p.placed[id]
		if !before {
			continue
		}
		transition, milliseconds := exitTransition(last.exit)
		if err := p.link.Hide(id, transition, milliseconds); err != nil {
			failures = append(failures, err)
			continue
		}
		delete(p.placed, id)
		sent = true
	}
	if len(order) == 0 {
		delete(p.ordered, output.Connector)
		return sent, errors.Join(failures...)
	}
	if sent || !slices.Equal(p.ordered[output.Connector], order) {
		if err := p.link.Order(output.Connector, order); err != nil {
			failures = append(failures, err)
		} else {
			p.ordered[output.Connector] = order
			sent = true
		}
	}
	return sent, errors.Join(failures...)
}

// surfacesOf converts the surfaces of one screen to the input of
// placeSurfaces.
func surfacesOf(on []screenSurface) []surface {
	surfaces := make([]surface, 0, len(on))
	for _, s := range on {
		surfaces = append(surfaces, surface{
			id:       s.id,
			claimKey: s.claim,
			labels:   s.labels,
			arrival:  s.ID,
		})
	}
	return surfaces
}

// moduleIDs maps the surface ids that status reports to the module's
// own ids, which the requests to the module use.
func moduleIDs(on []screenSurface) map[string]int {
	ids := make(map[string]int, len(on))
	for _, s := range on {
		ids[s.id] = s.ID
	}
	return ids
}

// inStackOrder sorts the placements from the bottom of the stack to
// the top, the order that the module's order request takes.
func inStackOrder(placed []placement) []placement {
	ordered := slices.Clone(placed)
	slices.SortStableFunc(ordered, func(a, b placement) int {
		return cmp.Compare(a.stack, b.stack)
	})
	return ordered
}

// surfaceID returns the id that status reports for a surface: a
// prefix of the claim's UID, then the module's id.
func surfaceID(claimUID string, id int) string {
	prefix := sharedSurface
	if claimUID != "" {
		prefix = claimUID
		if len(prefix) > claimIDPrefix {
			prefix = prefix[:claimIDPrefix]
		}
	}
	return fmt.Sprintf("%s-%d", prefix, id)
}

// logicalRect converts a region's fractions of the screen to whole
// pixels of the output's logical size, which the module reports and
// the compositor lays out in. A 4K panel at scale 2 is 1920 by 1080
// logical pixels and has a kernel mode of 3840 by 2160, so a rectangle
// computed from the mode would be twice the size of the screen.
func logicalRect(fraction LayoutRect, output layoutOutput) rect {
	return rect{
		X: int(math.Round(fraction.Left * float64(output.Width))),
		Y: int(math.Round(fraction.Top * float64(output.Height))),
		W: int(math.Round(fraction.Width * float64(output.Width))),
		H: int(math.Round(fraction.Height * float64(output.Height))),
	}
}

// enterTransition returns the transition and duration to send with a
// placement. When the region's enter is a fade, a surface that is new
// to the screen fades in, and a surface that is already placed moves
// to its new rectangle over the same duration, because a fade would
// make a visible surface blink. Anything else is sent as none, because
// the module refuses a fade or a move of zero milliseconds.
func enterTransition(enter LayoutTransitionHalf, placed bool) (string, int) {
	if enter.Kind != transitionFade || enter.Milliseconds <= 0 {
		return transitionNone, 0
	}
	if placed {
		return transitionMove, enter.Milliseconds
	}
	return transitionFade, enter.Milliseconds
}

// exitTransition returns the transition and duration to send with a
// hide. The module refuses a move in a hide, so the exit is a fade or
// none.
func exitTransition(exit LayoutTransitionHalf) (string, int) {
	if exit.Kind != transitionFade || exit.Milliseconds <= 0 {
		return transitionNone, 0
	}
	return transitionFade, exit.Milliseconds
}
