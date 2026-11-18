package main

// A claim on a controller can set the `inputs` parameter to the input
// classes it wants, from those that the published device lists. A claim
// without it receives every class. The relay sets an EVIOCSMASK mask on
// the real node, so the kernel drops the events of the other classes
// before the relay reads them.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// inputClassList is the class names for error messages.
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

// parseInputClasses rejects an empty `inputs` list, because a claim
// that wants no input is a mistake, and rejects an unknown name.
func parseInputClasses(names []string) (inputClasses, error) {
	if len(names) == 0 {
		return 0, fmt.Errorf("inputs is empty, so the claim receives no input class. Name at least one of %s, or leave inputs out to receive every class", inputClassList)
	}
	var want inputClasses
	for _, name := range names {
		class, found := inputClassByName[name]
		if !found {
			return 0, fmt.Errorf("%q is not an input class, and the classes are %s", name, inputClassList)
		}
		want |= class
	}
	return want, nil
}

// recordedInputs returns the class names to save in the CDI spec file
// of a prepared claim, which the operator reads back after a restart.
// It returns nil for every class, and a spec file with no list restores
// as every class.
func recordedInputs(want inputClasses) []string {
	if want == everyInputClass {
		return nil
	}
	return want.names()
}

// inputSettings is what a claim wants from one allocated controller: the
// input classes that reach the container, and the overrides for its
// absolute axes.
type inputSettings struct {
	classes inputClasses
	axes    axisOverrides
}

// claimParameters is the JSON of this driver's opaque configuration.
// The fields are pointers so that a missing field differs from an empty
// one.
type claimParameters struct {
	Inputs *[]string                   `json:"inputs"`
	Axes   *map[string]json.RawMessage `json:"axes"`
}

// requestInputSettings returns the input settings for one request of a
// claim. For
// each parameter separately, the claim's value replaces the
// DeviceClass's value, so a DeviceClass can set a default that a claim
// overrides. The loop reads every class block before any claim block,
// so the result does not depend on the order of the allocation's
// configuration.
func requestInputSettings(config []AllocatedConfig, request string) (inputSettings, error) {
	want := inputSettings{classes: everyInputClass}
	for _, source := range []string{"FromClass", "FromClaim"} {
		for _, block := range config {
			if block.Source != source || !block.appliesTo(request) {
				continue
			}
			var parameters claimParameters
			if err := json.Unmarshal(block.Opaque.Parameters, &parameters); err != nil {
				return inputSettings{}, fmt.Errorf("reading the %s configuration: %w", source, err)
			}
			if parameters.Inputs != nil {
				classes, err := parseInputClasses(*parameters.Inputs)
				if err != nil {
					return inputSettings{}, err
				}
				want.classes = classes
			}
			if parameters.Axes != nil {
				axes, err := parseAxisOverrides(*parameters.Axes)
				if err != nil {
					return inputSettings{}, err
				}
				want.axes = axes
			}
		}
	}
	return want, nil
}

// appliesTo reports whether a configuration block applies to a
// request. A block with no requests applies to every request in the
// claim, and a block that lists a request also applies to its
// subrequests, which are named "request/subrequest".
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

// eventMask is the argument of one EVIOCSMASK call: an event type, the
// highest code of that type, and the codes to pass. A mask of type
// EV_SYN selects whole event types.
type eventMask struct {
	event uint32
	max   int
	codes []uint16
}

// classTypes lists the event types that a reader of each class needs.
// The masks select whole event types and never single codes, so a claim
// for one class can also receive the events of another class that uses
// the same type, such as a key on a mouse.
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

// inputMasks returns the event masks to set on a node's file descriptor
// for the wanted classes. A nil result means that the node passes
// every event.
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
		// classTypes has no entry for an accelerometer, so a claim that
		// wants one receives the whole node.
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

// passEverything returns a mask that passes every event type. The
// kernel has no call that removes a mask from a file descriptor, so a
// node that was masked and is then wanted in full needs this mask.
func passEverything() []eventMask {
	types := make([]uint16, 0, eventTypeMax+1)
	for event := range uint16(eventTypeMax + 1) {
		types = append(types, event)
	}
	return []eventMask{{event: evSyn, max: eventTypeMax, codes: types}}
}
