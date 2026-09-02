package main

// These tests cover the compile from a Keymap's evdev names to the
// numbers the standing pod matches, and the gather that finds the
// Remotes one Player owns.

import (
	"reflect"
	"strings"
	"testing"
)

// A Keymap for one controller model: the two face buttons a gamepad
// reports the wrong way round for media, both bumpers, and one hat
// direction the base already covers.
func testKeymap() *Keymap {
	return &Keymap{
		Metadata: ObjectMeta{Name: "gamepad", Namespace: "house"},
		Spec: KeymapSpec{
			Buttons: []KeymapButton{
				{Press: "BTN_SOUTH", Key: "KEY_PLAYPAUSE"},
				{Press: "BTN_EAST", Key: "KEY_MUTE"},
				{Press: "BTN_TL", Key: "KEY_REWIND"},
				{Press: "BTN_TR", Key: "KEY_FASTFORWARD"},
			},
			Axes: []KeymapAxis{
				{Axis: "ABS_HAT0X", Value: 1, Key: keyNone},
			},
		},
	}
}

// The buttons compile to EV_KEY presses and the axes to EV_ABS values,
// in that order, so the standing pod carries numbers alone.
func TestCompileRowsTurnsEveryNameIntoItsCode(t *testing.T) {
	rows, err := compileRows(testKeymap())
	if err != nil {
		t.Fatal(err)
	}

	want := []compiledBinding{
		{EventType: evKey, Code: 0x130, Value: 1, Key: "KEY_PLAYPAUSE"},
		{EventType: evKey, Code: 0x131, Value: 1, Key: "KEY_MUTE"},
		{EventType: evKey, Code: 0x136, Value: 1, Key: "KEY_REWIND"},
		{EventType: evKey, Code: 0x137, Value: 1, Key: "KEY_FASTFORWARD"},
		{EventType: evAbs, Code: 0x10, Value: 1, Key: keyNone},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
}

// A media remote reports transport keys and no gamepad button, so both
// sides of a row can be a KEY_ name.
func TestCompileRowsBindsTheKeysAMediaRemoteReports(t *testing.T) {
	keymap := &Keymap{
		Metadata: ObjectMeta{Name: "remote"},
		Spec: KeymapSpec{
			Buttons: []KeymapButton{
				{Press: "KEY_COMPOSE", Key: "KEY_CYCLEWINDOWS"},
				{Press: "BTN_LEFT", Key: "KEY_ENTER"},
				{Press: "BTN_DPAD_UP", Key: "KEY_UP"},
			},
		},
	}

	rows, err := compileRows(keymap)
	if err != nil {
		t.Fatal(err)
	}

	want := []compiledBinding{
		{EventType: evKey, Code: 0x7f, Value: 1, Key: "KEY_CYCLEWINDOWS"},
		{EventType: evKey, Code: 0x110, Value: 1, Key: "KEY_ENTER"},
		{EventType: evKey, Code: 0x220, Value: 1, Key: "KEY_UP"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
}

// A compile error reaches a person through the operator's log, so
// each one must name the Keymap, the entry, and the rule it broke.
func TestCompileRowsRefusesWhatItCannotCompile(t *testing.T) {
	cases := []struct {
		name   string
		keymap *Keymap
		says   []string
	}{{
		name: "a button name the kernel does not give a code",
		keymap: &Keymap{
			Metadata: ObjectMeta{Name: "gamepad"},
			Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_TRIANGLE", Key: "KEY_ENTER"}}},
		},
		says: []string{"gamepad", "BTN_TRIANGLE"},
	}, {
		name: "an axis outside the two hat axes",
		keymap: &Keymap{
			Metadata: ObjectMeta{Name: "gamepad"},
			Spec:     KeymapSpec{Axes: []KeymapAxis{{Axis: "ABS_X", Value: 1, Key: "KEY_UP"}}},
		},
		says: []string{"gamepad", "ABS_X"},
	}, {
		name: "a key name the kernel does not give a code",
		keymap: &Keymap{
			Metadata: ObjectMeta{Name: "gamepad"},
			Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_SOUTH", Key: "KEY_EJECTCD_PLEASE"}}},
		},
		says: []string{"gamepad", "BTN_SOUTH", "KEY_EJECTCD_PLEASE"},
	}, {
		name: "an axis row that names no key",
		keymap: &Keymap{
			Metadata: ObjectMeta{Name: "gamepad"},
			Spec:     KeymapSpec{Axes: []KeymapAxis{{Axis: "ABS_HAT0X", Value: 1}}},
		},
		says: []string{"gamepad", "ABS_HAT0X"},
	}, {
		name:   "a Keymap that binds nothing at all",
		keymap: &Keymap{Metadata: ObjectMeta{Name: "gamepad"}},
		says:   []string{"gamepad"},
	}}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			rows, err := compileRows(one.keymap)
			if err == nil {
				t.Fatalf("rows = %+v, want an error", rows)
			}
			for _, word := range one.says {
				if !strings.Contains(err.Error(), word) {
					t.Errorf("err = %q, want it to name %q", err, word)
				}
			}
		})
	}
}

// none is the one right side that is not a key name, and it is how a
// Keymap silences a control the base would otherwise pass.
func TestCompileRowsAcceptsNoneOnEitherSide(t *testing.T) {
	keymap := &Keymap{
		Metadata: ObjectMeta{Name: "pad"},
		Spec: KeymapSpec{
			Buttons: []KeymapButton{{Press: "BTN_SOUTH", Key: keyNone}},
			Axes:    []KeymapAxis{{Axis: "ABS_HAT0Y", Value: -1, Key: keyNone}},
		},
	}

	rows, err := compileRows(keymap)
	if err != nil {
		t.Fatal(err)
	}

	want := []compiledBinding{
		{EventType: evKey, Code: 0x130, Value: 1, Key: keyNone},
		{EventType: evAbs, Code: 0x11, Value: -1, Key: keyNone},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
}

// A remote with a device selector and a keymap, named by a Player.
func testRemote(name, keymap string) Remote {
	return Remote{
		Metadata: ObjectMeta{Name: name, Namespace: "house"},
		Spec: RemoteSpec{
			Device: RemoteDevice{Class: "gamepad", Selector: `device.attributes["bluetooth.liken.sh"].address == "04:4A"`},
			Keymap: keymap,
		},
	}
}

// A Player that names the given Remotes in its spec.remotes.
func gatherPlayer(remotes ...string) *Player {
	entries := make([]PlayerRemote, 0, len(remotes))
	for _, name := range remotes {
		entries = append(entries, PlayerRemote{Name: name})
	}
	return &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
		Spec:     PlayerSpec{Remotes: entries},
	}
}

func remoteURL(name string) string {
	return "/apis/media.liken.sh/v1alpha1/namespaces/house/remotes/" + name
}

// The Player's remotes gather in name order whatever order the spec lists
// them in, because the pod spec they become must not change between
// passes.
func TestGatherRemotesReadsThePlayersRemotesInNameOrder(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET " + remoteURL("sofa"):     testRemote("sofa", "gamepad"),
		"GET " + remoteURL("armchair"): testRemote("armchair", "gamepad"),
	}}

	remotes, err := gatherRemotes(testAPIClient(t, api.handler()), gatherPlayer("sofa", "armchair"))
	if err != nil {
		t.Fatal(err)
	}

	names := []string{}
	for _, remote := range remotes {
		names = append(names, remote.Name)
	}
	if !reflect.DeepEqual(names, []string{"armchair", "sofa"}) {
		t.Errorf("remotes = %v, want [armchair sofa]", names)
	}
}

func TestGatherRemotesFindsNoneWhenThePlayerNamesNoRemote(t *testing.T) {
	remotes, err := gatherRemotes(testAPIClient(t, (&cannedAPI{}).handler()), gatherPlayer())
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 0 {
		t.Errorf("remotes = %+v, want none", remotes)
	}
}

// A Player that names a Remote nobody wrote is a failure the person
// who wrote the Player can read.
func TestGatherRemotesFailsWhenTheRemoteIsAbsent(t *testing.T) {
	_, err := gatherRemotes(testAPIClient(t, (&cannedAPI{}).handler()), gatherPlayer("ghost"))
	if err == nil {
		t.Fatal("a missing Remote produced no error")
	}
	for _, word := range []string{"theater", "ghost"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("err = %q, want it to name %q", err, word)
		}
	}
}

// A repeat block compiles to milliseconds, an empty field takes the
// default, and a row with no block carries no repeat.
func TestCompileRepeatSetsMillisecondsAndDefaults(t *testing.T) {
	keymap := &Keymap{
		Metadata: ObjectMeta{Name: "pad", Namespace: "house"},
		Spec: KeymapSpec{
			Buttons: []KeymapButton{
				{Press: "BTN_TR", Key: "KEY_FASTFORWARD", Repeat: &KeymapRepeat{Delay: "500ms", Interval: "200ms"}},
				{Press: "BTN_TL", Key: "KEY_REWIND", Repeat: &KeymapRepeat{}},
				{Press: "BTN_SOUTH", Key: "KEY_PLAYPAUSE"},
			},
		},
	}

	rows, err := compileRows(keymap)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].RepeatDelay != 500 || rows[0].RepeatInterval != 200 {
		t.Errorf("explicit repeat = %d/%d, want 500/200", rows[0].RepeatDelay, rows[0].RepeatInterval)
	}
	if rows[1].RepeatDelay != 400 || rows[1].RepeatInterval != 300 {
		t.Errorf("default repeat = %d/%d, want 400/300", rows[1].RepeatDelay, rows[1].RepeatInterval)
	}
	if rows[2].RepeatInterval != 0 {
		t.Errorf("a row with no repeat carries interval %d, want 0", rows[2].RepeatInterval)
	}
}

// A duration that does not parse fails the compile, because the
// standing pod reads milliseconds and parses nothing.
func TestCompileRepeatRejectsADurationThatDoesNotParse(t *testing.T) {
	bad := &Keymap{
		Metadata: ObjectMeta{Name: "pad", Namespace: "house"},
		Spec: KeymapSpec{
			Buttons: []KeymapButton{{Press: "BTN_TR", Key: "KEY_FASTFORWARD", Repeat: &KeymapRepeat{Interval: "soon"}}},
		},
	}

	_, err := compileRows(bad)

	mustFail(t, err)
}
