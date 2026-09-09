package main

// These tests prove two pure functions: the classes a claim asks for
// follow from its allocation alone, and the mask the kernel receives
// follows from that answer and the node's own classes.

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestParseInputClasses(t *testing.T) {
	cases := []struct {
		name    string
		names   []string
		want    inputClasses
		problem string
	}{
		{name: "one", names: []string{"joystick"}, want: classJoystick},
		{name: "two", names: []string{"key", "mouse"}, want: classKey | classMouse},
		{name: "repeated", names: []string{"key", "key"}, want: classKey},
		{name: "an underscore in a name", names: []string{"tablet_pad"}, want: classTabletPad},
		{
			name: "every class",
			names: []string{
				"key", "keyboard", "mouse", "pointingstick", "touchpad", "touchscreen",
				"tablet", "tablet_pad", "joystick", "accelerometer", "switch",
			},
			want: everyInputClass,
		},
		{name: "empty", names: []string{}, problem: "empty"},
		{name: "unknown", names: []string{"key", "buttons"}, problem: `"buttons"`},
		{name: "the udev spelling", names: []string{"ID_INPUT_KEY"}, problem: "ID_INPUT_KEY"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseInputClasses(c.names)
			if c.problem != "" {
				if err == nil {
					t.Fatalf("parseInputClasses(%v) = %s, want a refusal", c.names, got)
				}
				if !strings.Contains(err.Error(), c.problem) {
					t.Errorf("the refusal does not say %s: %v", c.problem, err)
				}
				// A refusal lists every class, so a person can fix the
				// claim from the message alone.
				for _, class := range inputClassNames {
					if !strings.Contains(err.Error(), class.name) {
						t.Errorf("the refusal does not name %s: %v", class.name, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("classes = %s, want %s", got, c.want)
			}
		})
	}
}

// The names round trip, because a prepared claim's record on disk
// holds them and a restart parses them back.
func TestInputClassNamesRoundTrip(t *testing.T) {
	for _, want := range []inputClasses{classKey, classKey | classMouse, everyInputClass} {
		got, err := parseInputClasses(want.names())
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("parseInputClasses(%v) = %s, want %s", want.names(), got, want)
		}
	}
}

// deliveredTypes are the event types one node's masks let through. A
// node with no mask delivers every type, which is what a nil answer
// says.
func deliveredTypes(t *testing.T, masks []eventMask) []uint16 {
	t.Helper()
	if len(masks) != 1 || masks[0].event != evSyn {
		t.Fatalf("masks = %+v, want one mask of event types", masks)
	}
	return masks[0].codes
}

func TestInputMasks(t *testing.T) {
	cases := []struct {
		name  string
		caps  evdevCapabilities
		want  inputClasses
		types []uint16
		whole bool
	}{
		{
			// Every class the node carries is demanded, so the kernel
			// filters nothing.
			name:  "a gamepad with joystick demanded",
			caps:  dualSenseGamepad(),
			want:  classJoystick | classKey,
			whole: true,
		},
		{
			// The whole node is the accelerometer, so a demand that
			// names it delivers all of it.
			name:  "the motion node with the accelerometer demanded",
			caps:  dualSenseMotion(),
			want:  classAccelerometer | classKey,
			whole: true,
		},
		{
			name:  "the motion node with the accelerometer not demanded",
			caps:  dualSenseMotion(),
			want:  classJoystick | classKey,
			types: nil,
		},
		{
			name:  "a gamepad with nothing it carries demanded",
			caps:  dualSenseGamepad(),
			want:  classKeyboard,
			types: nil,
		},
		{
			// No prepared claim demands anything, so the kernel queues
			// nothing on this fd.
			name:  "nothing demanded at all",
			caps:  dualSenseGamepad(),
			want:  0,
			types: nil,
		},
		{
			// An air mouse types and points on one node, so a claim on
			// its keys narrows the node to the key events.
			name: "an air mouse narrowed to its keys",
			caps: evdevCapabilities{
				Codes: map[string][]uint16{
					"EV_KEY": append(codeRange(103, 116), 0x110, 0x111),
					"EV_REL": {0x00, 0x01, 0x08},
					"EV_MSC": {0x04},
				},
			},
			want:  classKey,
			types: []uint16{evKey, evMsc},
		},
		{
			name: "an air mouse narrowed to its pointer",
			caps: evdevCapabilities{
				Codes: map[string][]uint16{
					"EV_KEY": append(codeRange(103, 116), 0x110, 0x111),
					"EV_REL": {0x00, 0x01, 0x08},
					"EV_MSC": {0x04},
				},
			},
			want:  classMouse,
			types: []uint16{evKey, evRel},
		},
		{
			// A node that carries no class has nothing to select, so
			// it is delivered whole.
			name:  "a node with no class at all",
			caps:  evdevCapabilities{Name: "Empty"},
			want:  classKey,
			whole: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			masks := inputMasks(c.caps, c.want)
			if c.whole {
				if masks != nil {
					t.Fatalf("masks = %+v, want none", masks)
				}
				return
			}
			if got := deliveredTypes(t, masks); !slices.Equal(got, c.types) {
				t.Errorf("types = %v, want %v", got, c.types)
			}
		})
	}
}

// The mask that passes everything names every event type, so widening
// a node's demand needs no record of what the fd carried before.
func TestPassEverythingCoversEveryEventType(t *testing.T) {
	types := deliveredTypes(t, passEverything())
	if len(types) != eventTypeMax+1 || int(types[eventTypeMax]) != eventTypeMax {
		t.Fatalf("the mask holds %d types, want %d", len(types), eventTypeMax+1)
	}
}

// driverConfig is one opaque configuration block for this driver.
func driverConfig(source string, parameters string, requests ...string) AllocatedConfig {
	return AllocatedConfig{
		Source:   source,
		Requests: requests,
		Opaque: &OpaqueConfig{
			Driver:     DriverName,
			Parameters: []byte(parameters),
		},
	}
}

func TestClaimInputs(t *testing.T) {
	cases := []struct {
		name    string
		config  []AllocatedConfig
		request string
		want    inputClasses
		problem string
	}{
		{
			name:    "no configuration at all",
			request: "controller",
			want:    everyInputClass,
		},
		{
			name:    "the class states the classes",
			config:  []AllocatedConfig{driverConfig("FromClass", `{"inputs":["key","joystick"]}`)},
			request: "controller",
			want:    classKey | classJoystick,
		},
		{
			name: "the claim wins over the class",
			config: []AllocatedConfig{
				driverConfig("FromClaim", `{"inputs":["joystick"]}`),
				driverConfig("FromClass", `{"inputs":["key","accelerometer"]}`),
			},
			request: "controller",
			want:    classJoystick,
		},
		{
			// A claim block that says nothing about inputs leaves the
			// class's block standing.
			name: "a claim block with no inputs key",
			config: []AllocatedConfig{
				driverConfig("FromClass", `{"inputs":["key"]}`),
				driverConfig("FromClaim", `{}`),
			},
			request: "controller",
			want:    classKey,
		},
		{
			name: "a block scoped to another request",
			config: []AllocatedConfig{
				driverConfig("FromClaim", `{"inputs":["key"]}`, "remote"),
			},
			request: "controller",
			want:    everyInputClass,
		},
		{
			name: "a block scoped to this request",
			config: []AllocatedConfig{
				driverConfig("FromClaim", `{"inputs":["key"]}`, "remote", "controller"),
			},
			request: "controller",
			want:    classKey,
		},
		{
			// A request that a subrequest satisfied is still governed
			// by a block that names the request itself.
			name: "a block that names the parent of a subrequest",
			config: []AllocatedConfig{
				driverConfig("FromClaim", `{"inputs":["key"]}`, "controller"),
			},
			request: "controller/bluetooth",
			want:    classKey,
		},
		{
			name: "another driver's block",
			config: []AllocatedConfig{{
				Source: "FromClaim",
				Opaque: &OpaqueConfig{Driver: "liken.sh", Parameters: []byte(`{"inputs":["key"]}`)},
			}},
			request: "controller",
			want:    everyInputClass,
		},
		{
			name:    "an empty list",
			config:  []AllocatedConfig{driverConfig("FromClaim", `{"inputs":[]}`)},
			request: "controller",
			problem: "empty",
		},
		{
			name:    "an unknown class",
			config:  []AllocatedConfig{driverConfig("FromClaim", `{"inputs":["buttons"]}`)},
			request: "controller",
			problem: `"buttons"`,
		},
		{
			name:    "parameters this driver cannot read",
			config:  []AllocatedConfig{driverConfig("FromClaim", `{"inputs":"key"}`)},
			request: "controller",
			problem: "inputs",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := claimInputs(c.config, c.request)
			if c.problem != "" {
				if err == nil {
					t.Fatalf("claimInputs = %s, want a refusal", got)
				}
				if !strings.Contains(err.Error(), c.problem) {
					t.Errorf("the refusal does not say %s: %v", c.problem, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("classes = %s, want %s", got, c.want)
			}
		})
	}
}

// A claim that receives every class records no list, so a spec file
// written before this parameter existed restores as the default.
func TestInputClassesRecordOnlyANarrowedDemand(t *testing.T) {
	cases := []struct {
		want     inputClasses
		recorded []string
	}{
		{want: everyInputClass, recorded: nil},
		{want: classKey | classJoystick, recorded: []string{"key", "joystick"}},
	}
	for _, c := range cases {
		if got := recordedInputs(c.want); !reflect.DeepEqual(got, c.recorded) {
			t.Errorf("recordedInputs(%s) = %v, want %v", c.want, got, c.recorded)
		}
	}
}
