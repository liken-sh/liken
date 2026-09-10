package main

// The placement pass: the surfaces the compositor's module reports
// become rectangles on each screen.
//
// One pass reads three things and writes two. It reads the module's
// store, which is every surface on this node's screens with the
// socket each one arrived on; the claim behind each socket and the
// labels its holders share; and each screen's Display and the Layout
// it names. It writes placements to the module, and it writes what it
// decided to the Display's status.
//
// The decision is placeSurfaces, which is a pure function, and this
// file is what executes its result. Keeping the two apart is the seam
// a different layout engine would plug into: another engine would
// produce the same placement from the same inputs, and this file
// would commit it the same way (plans/open-problems/an-external-layout-engine.md).
//
// The pass keeps a memo of the last placement it sent for each
// surface, so a pass that decides the same thing again sends nothing.
// Most wakes carry no news for the screen, and a commit with nothing
// in it would still cost the compositor a repaint. A new connection to
// the module empties the memo, because a compositor that restarted
// holds nothing the operator sent before, and the module re-reports
// every surface it holds so the pass places them all again.

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"time"
)

// claimIDPrefix is how much of a claim's UID the surface id carries.
// Eight characters of a UUID name the claim to a person reading the
// resource, and the module's own id makes the whole thing unique.
const claimIDPrefix = 8

// sharedSurface stands in for the claim prefix of a surface on the
// compositor's own socket, which belongs to no claim.
const sharedSurface = "shared"

// One surface on one screen: what the module reported about it, and
// what the claim behind its socket answered.
type screenSurface struct {
	layoutSurface
	id     string
	claim  string
	pods   []string
	labels map[string]string
}

// Where one surface was last placed. The pass compares the connector
// and the rectangle with what it decided, and sends nothing for a
// surface whose rectangle and screen did not move.
//
// The exit is the transition the region stated for a surface that
// leaves it. The memo keeps it because the pass that hides a surface
// is a later pass than the one that placed it: by then the surface
// matches no region, and the Layout may state none, so what runs is
// what the region stated while the surface was in it.
type placedSurface struct {
	connector string
	where     rect
	exit      LayoutTransitionHalf
}

// placementPass runs the whole placement: it reads the surfaces the
// module reports, resolves each one to the pods that hold its claim,
// runs the decision for each screen, states what changed to the
// module, and writes each Display's status.
type placementPass struct {
	client *Client
	node   string
	link   *layoutLink
	claims *claimIndex
	// Outputs is the same walk of the card the slice publisher and the
	// Display controller make, so all three name one set of
	// connectors and monitors.
	outputs func() []Output
	// Sockets names the claim and the output device of every socket a
	// prepared claim holds. It is a field so a test drives a pass with
	// no CDI directory of its own.
	sockets func() (map[string]preparedSocket, error)
	now     func() time.Time

	// Generation is the module connection the memo below belongs to.
	generation int
	// Placed is the memo: the rectangle each surface was last placed
	// at, and ordered is the stacking order each connector was last
	// sent. A pass that decides what the last one decided sends
	// nothing at all.
	placed  map[int]placedSurface
	ordered map[string][]int
	// Metrics counts the surfaces this pass places on each output. It
	// is nil in every test that drives a pass with no listener behind
	// it, and a nil metrics records nothing.
	metrics *metrics
}

func newPlacementPass(client *Client, node string, link *layoutLink, claims *claimIndex,
	outputs func() []Output) *placementPass {
	return &placementPass{
		client:  client,
		node:    node,
		link:    link,
		claims:  claims,
		outputs: outputs,
		sockets: preparedSocketClaims,
		now:     time.Now,
		placed:  map[int]placedSurface{},
		ordered: map[string][]int{},
	}
}

// pass places every surface the module reports and reports every
// screen.
//
// A pass with no module serving does nothing. The module keeps the
// last layout it committed while nothing is connected to it, and the
// connection that follows reports every surface again, so there is
// nothing to read and nowhere to send in the meantime.
func (p *placementPass) pass() error {
	state := p.link.state()
	if !state.Serving {
		return nil
	}
	// The record of which claim holds which socket is what turns a
	// surface into a claim. A read of it that failed would leave every
	// surface with no claim, which falls out of every region and hides
	// the whole screen, so the pass ends here instead.
	sockets, err := p.sockets()
	if err != nil {
		return err
	}
	// A new connection is a new compositor: every surface and every id
	// went with the old one, so the memo goes and this pass states
	// every placement again.
	if state.Generation != p.generation {
		p.generation = state.Generation
		p.forget()
	}
	for id := range p.placed {
		if _, held := state.Surfaces[id]; !held {
			delete(p.placed, id)
		}
	}

	outputs := p.outputs()
	screens, failures := p.resolve(state, sockets, outputs)
	stated := false
	for _, connector := range slices.Sorted(maps.Keys(state.Outputs)) {
		held := screens[connector]
		if held != nil && held.unresolved {
			// A screen is rearranged from a whole reading and no other.
			// The surface whose claim this pass could not read would
			// fall out of every region, and one request that failed
			// must not take a program off the screen.
			continue
		}
		on := surfacesOn(held)
		p.metrics.recordSurfaces(connector, len(on))
		sent, err := p.screen(state.Outputs[connector], on, outputs)
		stated = stated || sent
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", connector, err))
		}
	}
	if stated {
		if err := p.link.Commit(); err != nil {
			// Nothing this pass sent reached the screen, so the memo
			// goes and the next pass states all of it again.
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

// The surfaces of one screen, and whether every one of them
// resolved. A claim the API server could not answer for leaves the
// screen unresolved, and the pass leaves such a screen as it is.
type screenSurfaces struct {
	on         []screenSurface
	unresolved bool
}

// The surfaces of one screen, and none for a screen the module
// reports with nothing on it.
func surfacesOn(screen *screenSurfaces) []screenSurface {
	if screen == nil {
		return nil
	}
	return screen.on
}

// resolve puts each surface the module reported on the screen its
// socket was opened for, with the claim and the labels that place it.
//
// The surfaces of one screen come out in the order the module
// assigned their ids, which is the order they arrived in: the ids
// count up for the compositor's whole life.
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

	holders := newHolderReader(p.client, p.node, p.claims)
	screens := map[string]*screenSurfaces{}
	for _, reported := range state.Surfaces {
		on := screenSurface{layoutSurface: reported, id: surfaceID("", reported.ID)}
		// A surface on the compositor's own socket belongs to no
		// claim, so nothing in its connection says which screen it was
		// meant for, and it goes on the first connected output.
		connector, unresolved := shared, false
		if socket, held := sockets[reported.Socket]; held {
			connector = connectors[socket.device]
			on.id = surfaceID(socket.claim, reported.ID)
			by, err := holders.holders(socket.claim)
			if err != nil {
				failures = append(failures, err)
				unresolved = true
			}
			on.claim, on.pods, on.labels = by.key, by.pods, by.labels
		} else if reported.Socket != socketName {
			// The claim was given back while its client kept drawing.
			// The surface belongs to no claim from here on, which is
			// what a surface on the compositor's own socket is.
			failures = append(failures, fmt.Errorf("surface %d arrived on %s, which no prepared claim holds",
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
	for _, screen := range screens {
		slices.SortFunc(screen.on, func(a, b screenSurface) int {
			return cmp.Compare(a.ID, b.ID)
		})
	}
	return screens, failures
}

// screen decides the layout of one screen, states what changed to the
// module, and writes the status. It answers whether anything reached
// the module, which is what earns the commit.
func (p *placementPass) screen(output layoutOutput, on []screenSurface, outputs []Output) (bool, error) {
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
	stated, failure := p.send(output, decision, moduleIDs(on))
	if display == nil {
		// A connector whose monitor answers no identity carries no
		// resource of its own. The screen is still placed, and nothing
		// reports what it shows.
		return stated, failure
	}
	return stated, errors.Join(failure, p.report(display, on, decision, layoutName(named, layout), resolved))
}

// The resource of the panel on one connector, and nothing at all for
// a connector whose monitor answers no identity. The name is built
// the way the Display controller builds it, from the monitor's own
// EDID, so the two never write two resources for one panel.
//
// A resource that is not there yet is the Display controller's own
// pass to create, and this pass reports the screen on the pass after
// that.
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
	display, err := getDisplay(p.client, name)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return display, err
}

// The Layout one screen is drawn to, and the condition that reports
// how the name resolved. A screen that names none and a name the
// cluster does not hold are both drawn to the default, which is what
// a nil layout means to the decision.
//
// A read that failed for any other reason fails the screen. A pass
// that read the default here would rearrange every surface because
// one request to the API server failed.
func (p *placementPass) layoutOf(named string) (*LayoutSpec, DisplayCondition, error) {
	if named == "" {
		return nil, p.condition(true, DefaultLayoutReason,
			"this screen names no layout, so every surface is drawn over the whole screen with the newest on top"), nil
	}
	layout, err := getLayout(p.client, named)
	if errors.Is(err, ErrNotFound) {
		return nil, p.condition(false, LayoutNotFoundReason,
			"no Layout is named "+named+", so this screen is drawn to the default"), nil
	}
	if err != nil {
		return nil, DisplayCondition{}, err
	}
	return &layout.Spec, p.condition(true, LayoutFoundReason, named+" is the layout this screen shows"), nil
}

// send states one screen's decision to the module, and answers
// whether anything reached it.
//
// A placement the memo already holds sends nothing, which is what
// makes a pass over an unchanged screen silent. The order goes with
// every batch that placed a surface here, because the module puts a
// surface that is new to an output on top until an order arrives.
func (p *placementPass) send(output layoutOutput, decision screenPlacement, ids map[string]int) (bool, error) {
	var failures []error
	stated := false
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
			// A Layout a person edited may state another exit for the
			// same rectangle, so the memo takes the new one. The
			// screen is where the decision says, and nothing goes to
			// the module.
			p.placed[id] = next
			continue
		}
		transition, milliseconds := enterTransition(place.transition.Enter, before)
		if err := p.link.Place(id, output.Connector, where, transition, milliseconds); err != nil {
			failures = append(failures, err)
			continue
		}
		p.placed[id] = next
		stated = true
	}
	// A surface no region took leaves the screen with the exit of the
	// region it was in. A surface the module reported gone is already
	// off the screen and lost its memo at the top of the pass, so
	// nothing is sent for it: there is no surface left to fade.
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
		stated = true
	}
	if len(order) == 0 {
		delete(p.ordered, output.Connector)
		return stated, errors.Join(failures...)
	}
	if stated || !slices.Equal(p.ordered[output.Connector], order) {
		if err := p.link.Order(output.Connector, order); err != nil {
			failures = append(failures, err)
		} else {
			p.ordered[output.Connector] = order
			stated = true
		}
	}
	return stated, errors.Join(failures...)
}

// report writes what one screen shows. The write happens only where
// the status this pass computed differs from the published one, so a
// steady screen writes nothing.
//
// The Display controller writes the same status from its own loop,
// and the two write different fields of it: this pass writes the
// surfaces, the layout, and the LayoutResolved condition, and the
// controller writes the panel's own facts. Each starts from the
// status it read, so neither drops what the other wrote, and a write
// the two raced costs one pass.
func (p *placementPass) report(display *Display, on []screenSurface, decision screenPlacement,
	name string, resolved DisplayCondition) error {
	status := display.Status
	status.Surfaces = surfaceStatus(on, decision)
	status.Layout = &DisplayLayout{Name: name, Regions: regionStatus(decision)}
	status.Conditions = setCondition(status.Conditions, resolved)
	if reflect.DeepEqual(display.Status, status) {
		return nil
	}
	_, err := writeDisplayStatus(p.client, display, status)
	return err
}

func (p *placementPass) condition(met bool, reason, message string) DisplayCondition {
	status := conditionFalse
	if met {
		status = conditionTrue
	}
	return DisplayCondition{
		Type:               LayoutResolvedCondition,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: p.now().UTC().Format(time.RFC3339),
	}
}

// Every surface on the screen, whether or not a region took it. A
// surface with no region is a program that is running and not on the
// screen, which is the first thing to read when a program draws
// nothing a person can see.
func surfaceStatus(on []screenSurface, decision screenPlacement) []DisplaySurface {
	regions := map[string]string{}
	for _, place := range decision.placed {
		regions[place.surface] = place.region
	}
	var surfaces []DisplaySurface
	for _, surface := range on {
		surfaces = append(surfaces, DisplaySurface{
			ID:     surface.id,
			Claim:  surface.claim,
			Pods:   surface.pods,
			Labels: surface.labels,
			Size:   &SurfaceSize{Width: surface.Width, Height: surface.Height},
			Region: regions[surface.id],
		})
	}
	return surfaces
}

// Each region in stacking order, with the surface it shows or the
// word empty.
func regionStatus(decision screenPlacement) []DisplayRegion {
	var regions []DisplayRegion
	for _, region := range decision.regions {
		regions = append(regions, DisplayRegion{Name: region.name, Surface: region.surface})
	}
	return regions
}

// The name status reports: the Layout in force, or the word default
// for a screen that names none and for a name that resolved to
// nothing.
func layoutName(named string, layout *LayoutSpec) string {
	if layout == nil {
		return defaultLayoutName
	}
	return named
}

// The decision's own input: the surfaces of one screen, each with the
// labels a region's selector reads.
func surfacesOf(on []screenSurface) []surface {
	surfaces := make([]surface, 0, len(on))
	for _, held := range on {
		surfaces = append(surfaces, surface{
			id:       held.id,
			claimKey: held.claim,
			labels:   held.labels,
			// The module's id counts up for the compositor's whole
			// life, so it is the order the surfaces arrived in.
			arrival: held.ID,
		})
	}
	return surfaces
}

// The module's own id for each surface on the screen, which is what
// every request names. The decision speaks in the ids status reports.
func moduleIDs(on []screenSurface) map[string]int {
	ids := make(map[string]int, len(on))
	for _, held := range on {
		ids[held.id] = held.ID
	}
	return ids
}

// The placements bottom first, which is the order the module's own
// render order takes.
func inStackOrder(placed []placement) []placement {
	ordered := slices.Clone(placed)
	slices.SortStableFunc(ordered, func(a, b placement) int {
		return cmp.Compare(a.stack, b.stack)
	})
	return ordered
}

// surfaceID is the id status reports for one surface: the claim's UID
// prefix, and the id the module assigned. A surface with no claim
// reports the word shared in place of the prefix.
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

// The rectangle in the output's logical pixels, rounded to whole
// ones.
//
// The logical size is the one the module reports for the output,
// which is the size the compositor lays out in. A 4K panel at scale 2
// lays out as 1920 by 1080, where the kernel mode says 3840 by 2160,
// so a rectangle computed from the mode would be twice the screen.
func logicalRect(fraction LayoutRect, output layoutOutput) rect {
	return rect{
		X: int(math.Round(fraction.Left * float64(output.Width))),
		Y: int(math.Round(fraction.Top * float64(output.Height))),
		W: int(math.Round(fraction.Width * float64(output.Width))),
		H: int(math.Round(fraction.Height * float64(output.Height))),
	}
}

// The transition one placement states to the module, and its
// duration. A fade enters a surface the screen did not hold, and a
// move glides one whose rectangle changed, because a fade on a
// surface that is already visible would blink it.
//
// A transition with no duration is none, over no milliseconds. The
// module refuses a fade or a move over zero milliseconds, and a
// region that states a kind and no duration means an entrance with no
// animation.
func enterTransition(enter LayoutTransitionHalf, placed bool) (string, int) {
	if enter.Kind != transitionFade || enter.Milliseconds <= 0 {
		return transitionNone, 0
	}
	if placed {
		return transitionMove, enter.Milliseconds
	}
	return transitionFade, enter.Milliseconds
}

// The transition one hide states to the module. A hide runs no move,
// because the surface is leaving the screen and there is no rectangle
// to glide it to, so an exit is a fade or nothing.
func exitTransition(exit LayoutTransitionHalf) (string, int) {
	if exit.Kind != transitionFade || exit.Milliseconds <= 0 {
		return transitionNone, 0
	}
	return transitionFade, exit.Milliseconds
}
