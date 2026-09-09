package main

// The `inputs` parameter of a claim on a controller.
//
// The device's slice advertises every class the hardware has, and the
// claim states which of them to deliver. No statement means every
// class, which is what this driver delivered before the parameter
// existed. The answer becomes one EVIOCSMASK on the real node, so a
// class nobody asked for costs the kernel one bit test per event and
// this operator nothing.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// inputClassByName reads a name from a claim's configuration, and
// inputClassList names every class for a message that refuses one.
var (
	inputClassByName = classesByName()
	inputClassList   = strings.Join(everyInputClass.names(), ", ")
)

func classesByName() map[string]inputClasses {
	byName := make(map[string]inputClasses, len(inputClassNames))
	for _, class := range inputClassNames {
		byName[class.name] = class.class
	}
	return byName
}

// parseInputClasses reads the `inputs` list of a claim's
// configuration. An empty list is refused, because a claim that
// delivers nothing is a mistake and not a request, and a name outside
// the vocabulary is refused with the vocabulary.
func parseInputClasses(names []string) (inputClasses, error) {
	if len(names) == 0 {
		return 0, fmt.Errorf("inputs is empty; name at least one of %s, or leave inputs out to receive every class", inputClassList)
	}
	var want inputClasses
	for _, name := range names {
		class, found := inputClassByName[name]
		if !found {
			return 0, fmt.Errorf("%q is not an input class; the classes are %s", name, inputClassList)
		}
		want |= class
	}
	return want, nil
}

// recordedInputs is the list a prepared claim's CDI spec file holds,
// which a restart of this operator reads its demand back from. A
// claim that receives every class records nothing, so a file written
// before this parameter existed restores as the default.
func recordedInputs(want inputClasses) []string {
	if want == everyInputClass {
		return nil
	}
	return want.names()
}

// delivery is what one allocated controller's claim asked for: which
// classes of input reach the container, and how each absolute axis is
// tuned on the way.
type delivery struct {
	classes inputClasses
	axes    axisOverrides
}

// claimParameters is the shape this driver reads out of an opaque
// configuration block. Each field is a pointer so that a block which
// states nothing about it is not read as a block that states an empty
// value.
type claimParameters struct {
	Inputs *[]string                   `json:"inputs"`
	Axes   *map[string]json.RawMessage `json:"axes"`
}

// claimDelivery is what one allocated device of a claim receives. The
// blocks arrive from the DeviceClass and from the claim, and the
// claim's own block wins for each parameter on its own, so a cluster
// owner can put a default on a class and a workload can still ask for
// something else.
func claimDelivery(config []AllocatedConfig, request string) (delivery, error) {
	want := delivery{classes: everyInputClass}
	// The class's block is read first and the claim's second, so the
	// claim's answer overwrites the class's whichever order the API
	// server lists them in.
	for _, source := range []string{"FromClass", "FromClaim"} {
		for _, block := range config {
			if block.Source != source || !block.appliesTo(request) {
				continue
			}
			var parameters claimParameters
			if err := json.Unmarshal(block.Opaque.Parameters, &parameters); err != nil {
				return delivery{}, fmt.Errorf("reading the %s configuration: %w", source, err)
			}
			if parameters.Inputs != nil {
				classes, err := parseInputClasses(*parameters.Inputs)
				if err != nil {
					return delivery{}, err
				}
				want.classes = classes
			}
			if parameters.Axes != nil {
				axes, err := parseAxisOverrides(*parameters.Axes)
				if err != nil {
					return delivery{}, err
				}
				want.axes = axes
			}
		}
	}
	return want, nil
}

// appliesTo reports whether one configuration block governs an
// allocated device. A block with no requests governs every request in
// the claim. A block that names a request also governs the
// subrequests below it, which is how the API spells a request that
// several device classes can satisfy.
func (c AllocatedConfig) appliesTo(request string) bool {
	if c.Opaque == nil || c.Opaque.Driver != DriverName {
		return false
	}
	if len(c.Requests) == 0 {
		return true
	}
	parent, _, _ := strings.Cut(request, "/")
	return slices.Contains(c.Requests, request) || slices.Contains(c.Requests, parent)
}

// eventMask is one EVIOCSMASK call: the event type whose codes it
// limits, the highest code the kernel counts for that type, and the
// codes to keep. The mask for EV_SYN is the mask of event types, and
// EV_SYN itself is never filtered.
type eventMask struct {
	event uint32
	max   int
	codes []uint16
}

// classTypes is what each class delivers: the event types a consumer
// of that class reads. Narrowing works at this grain and no finer,
// because a type is what a program opens a node for and the codes
// within a type are that program's own business.
var classTypes = map[inputClasses][]uint16{
	classKey:           {evKey, evMsc},
	classKeyboard:      {evKey, evMsc},
	classMouse:         {evKey, evRel},
	classPointingStick: {evKey, evRel},
	classTouchpad:      {evKey, evAbs},
	classTouchscreen:   {evKey, evAbs},
	classTablet:        {evKey, evAbs},
	classTabletPad:     {evKey, evAbs},
	classJoystick:      {evKey, evAbs},
	classSwitch:        {evSw},
}

// inputMasks is what the kernel must queue on one node's fd for a
// demand. A nil answer means no mask at all, which is every event the
// node emits.
//
// The cases, in the order the code tests them: nothing is demanded,
// so nothing is queued; the node carries no class, so there is
// nothing to select and it is delivered whole; every class it carries
// is demanded, so nothing is filtered; none of its classes is
// demanded, so nothing is queued; and the accelerometer is the whole
// node, so a demand that names it delivers all of it.
func inputMasks(caps evdevCapabilities, want inputClasses) []eventMask {
	nothing := []eventMask{{event: evSyn, max: eventTypeMax}}
	carried := nodeInputClasses(caps)
	switch {
	case want == 0:
		return nothing
	case carried == 0:
		return nil
	case carried&^want == 0:
		return nil
	case want&carried == 0:
		return nothing
	case want&carried&classAccelerometer != 0:
		return nil
	}

	var types []uint16
	for _, class := range inputClassNames {
		if want&carried&class.class == 0 {
			continue
		}
		for _, event := range classTypes[class.class] {
			if !slices.Contains(types, event) {
				types = append(types, event)
			}
		}
	}
	slices.Sort(types)
	return []eventMask{{event: evSyn, max: eventTypeMax, codes: types}}
}

// passEverything is the mask that filters nothing. The kernel keeps a
// mask on an fd until another one replaces it and has no call that
// removes one, so widening a node's demand back to every class sets
// this rather than setting nothing.
func passEverything() []eventMask {
	types := make([]uint16, 0, eventTypeMax+1)
	for event := range uint16(eventTypeMax + 1) {
		types = append(types, event)
	}
	return []eventMask{{event: evSyn, max: eventTypeMax, codes: types}}
}
