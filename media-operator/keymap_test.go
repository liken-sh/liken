package main

// These tests cover the compile from evdev names to the numbers the
// sidecar matches, and the gather that finds the Remotes bound to
// one Player.

import (
	"reflect"
	"strings"
	"testing"
)

// A Keymap for one controller model: two face buttons, both bumpers,
// and the hat in both directions.
func testKeymap() *Keymap {
	return &Keymap{
		Metadata: ObjectMeta{Name: "gamepad", Namespace: "house"},
		Spec: KeymapSpec{
			Buttons: []KeymapButton{
				{Press: "BTN_SOUTH", Action: actionPause},
				{Press: "BTN_EAST", Action: actionMute},
				{Press: "BTN_TL", Action: actionSeek, Amount: -30},
				{Press: "BTN_TR", Action: actionSeek, Amount: 30},
			},
			Axes: []KeymapAxis{
				{Axis: "ABS_HAT0Y", Value: -1, Action: actionVolume, Amount: 5},
				{Axis: "ABS_HAT0Y", Value: 1, Action: actionVolume, Amount: -5},
				{Axis: "ABS_HAT0X", Value: 1, Action: actionChapter, Amount: 1},
			},
		},
	}
}

// The buttons compile to EV_KEY presses and the axes to EV_ABS
// values, in that order, so the sidecar carries numbers alone.
func TestCompileKeymapTurnsEveryNameIntoItsCode(t *testing.T) {
	bindings, err := compileKeymap(testKeymap())
	if err != nil {
		t.Fatal(err)
	}

	want := []compiledBinding{
		{EventType: evKey, Code: 0x130, Value: 1, Action: actionPause},
		{EventType: evKey, Code: 0x131, Value: 1, Action: actionMute},
		{EventType: evKey, Code: 0x136, Value: 1, Action: actionSeek, Amount: -30},
		{EventType: evKey, Code: 0x137, Value: 1, Action: actionSeek, Amount: 30},
		{EventType: evAbs, Code: 0x11, Value: -1, Action: actionVolume, Amount: 5},
		{EventType: evAbs, Code: 0x11, Value: 1, Action: actionVolume, Amount: -5},
		{EventType: evAbs, Code: 0x10, Value: 1, Action: actionChapter, Amount: 1},
	}
	if !reflect.DeepEqual(bindings, want) {
		t.Errorf("bindings = %+v, want %+v", bindings, want)
	}
}

// A compile error becomes the Play's status message, so each one
// must name the Keymap, the entry, and the rule it broke.
func TestCompileKeymapRefusesWhatItCannotCompile(t *testing.T) {
	cases := []struct {
		name   string
		keymap *Keymap
		says   []string
	}{{
		name: "a button name no Linux driver reports",
		keymap: &Keymap{
			Metadata: ObjectMeta{Name: "gamepad"},
			Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_MIDDLE", Action: actionPause}}},
		},
		says: []string{"gamepad", "BTN_MIDDLE"},
	}, {
		name: "an axis outside the two hat axes",
		keymap: &Keymap{
			Metadata: ObjectMeta{Name: "gamepad"},
			Spec:     KeymapSpec{Axes: []KeymapAxis{{Axis: "ABS_X", Value: 1, Action: actionPause}}},
		},
		says: []string{"gamepad", "ABS_X"},
	}, {
		name: "an action outside the vocabulary",
		keymap: &Keymap{
			Metadata: ObjectMeta{Name: "gamepad"},
			Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_SOUTH", Action: "eject"}}},
		},
		says: []string{"gamepad", "BTN_SOUTH", "eject"},
	}, {
		name: "an amount on an action that is complete alone",
		keymap: &Keymap{
			Metadata: ObjectMeta{Name: "gamepad"},
			Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_SOUTH", Action: actionPause, Amount: 3}}},
		},
		says: []string{"gamepad", "BTN_SOUTH", actionPause},
	}, {
		name: "an amount action with no amount",
		keymap: &Keymap{
			Metadata: ObjectMeta{Name: "gamepad"},
			Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_TL", Action: actionSeek}}},
		},
		says: []string{"gamepad", "BTN_TL", actionSeek},
	}, {
		name: "an amount action whose amount is zero",
		keymap: &Keymap{
			Metadata: ObjectMeta{Name: "gamepad"},
			Spec:     KeymapSpec{Axes: []KeymapAxis{{Axis: "ABS_HAT0X", Value: 1, Action: actionChapter, Amount: 0}}},
		},
		says: []string{"gamepad", "ABS_HAT0X", actionChapter},
	}, {
		name:   "a Keymap that binds nothing at all",
		keymap: &Keymap{Metadata: ObjectMeta{Name: "gamepad"}},
		says:   []string{"gamepad"},
	}}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			bindings, err := compileKeymap(one.keymap)
			if err == nil {
				t.Fatalf("bindings = %+v, want an error", bindings)
			}
			for _, word := range one.says {
				if !strings.Contains(err.Error(), word) {
					t.Errorf("err = %q, want it to name %q", err, word)
				}
			}
		})
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

const keymapURL = "/apis/media.liken.sh/v1alpha1/namespaces/house/keymaps/gamepad"

func remoteURL(name string) string {
	return "/apis/media.liken.sh/v1alpha1/namespaces/house/remotes/" + name
}

// The Player's remotes compile in name order whatever order the spec
// lists them in, because the pod spec they become must not change
// between passes.
func TestGatherRemotesReadsThePlayersRemotesInNameOrder(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET " + remoteURL("sofa"):     testRemote("sofa", "gamepad"),
		"GET " + remoteURL("armchair"): testRemote("armchair", "gamepad"),
		"GET " + keymapURL:             *testKeymap(),
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

// Each named remote carries its device selection and its compiled
// table, which is everything the claim and the sidecar need.
func TestGatherRemotesCarriesTheDeviceAndTheCompiledTable(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET " + remoteURL("sofa"): testRemote("sofa", "gamepad"),
		"GET " + keymapURL:         *testKeymap(),
	}}

	remotes, err := gatherRemotes(testAPIClient(t, api.handler()), gatherPlayer("sofa"))
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 1 {
		t.Fatalf("remotes = %+v, want one", remotes)
	}
	device := RemoteDevice{
		Class:    "gamepad",
		Selector: `device.attributes["bluetooth.liken.sh"].address == "04:4A"`,
	}
	if remotes[0].Device != device {
		t.Errorf("device = %+v, want %+v", remotes[0].Device, device)
	}
	first := compiledBinding{EventType: evKey, Code: 0x130, Value: 1, Action: actionPause}
	if len(remotes[0].Bindings) != 7 || remotes[0].Bindings[0] != first {
		t.Errorf("bindings = %+v", remotes[0].Bindings)
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

// A Remote that names a Keymap nobody wrote is a failure the person
// who wrote the Remote can read.
func TestGatherRemotesFailsWhenTheKeymapIsAbsent(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET " + remoteURL("sofa"): testRemote("sofa", "gamepad"),
	}}

	_, err := gatherRemotes(testAPIClient(t, api.handler()), gatherPlayer("sofa"))
	if err == nil {
		t.Fatal("a missing Keymap produced no error")
	}
	for _, word := range []string{"sofa", "gamepad"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("err = %q, want it to name %q", err, word)
		}
	}
}

// A keymap that will not compile fails the gather, and the message is
// the compiler's own.
func TestGatherRemotesFailsOnAKeymapThatWillNotCompile(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET " + remoteURL("sofa"): testRemote("sofa", "gamepad"),
		"GET " + keymapURL: Keymap{
			Metadata: ObjectMeta{Name: "gamepad", Namespace: "house"},
			Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_NOPE", Action: actionPause}}},
		},
	}}

	_, err := gatherRemotes(testAPIClient(t, api.handler()), gatherPlayer("sofa"))
	if err == nil {
		t.Fatal("a keymap that will not compile produced no error")
	}
	if !strings.Contains(err.Error(), "BTN_NOPE") {
		t.Errorf("err = %q, want it to name BTN_NOPE", err)
	}
}

// A repeat block compiles to milliseconds, an empty field takes the
// default, and a binding with no block carries no repeat.
func TestCompileRepeatSetsMillisecondsAndDefaults(t *testing.T) {
	keymap := &Keymap{
		Metadata: ObjectMeta{Name: "pad", Namespace: "house"},
		Spec: KeymapSpec{
			Buttons: []KeymapButton{
				{Press: "BTN_TR", Action: actionSeek, Amount: 10, Repeat: &KeymapRepeat{Delay: "500ms", Interval: "200ms"}},
				{Press: "BTN_TL", Action: actionSeek, Amount: -10, Repeat: &KeymapRepeat{}},
				{Press: "BTN_SOUTH", Action: actionPause},
			},
		},
	}

	bindings, err := compileKeymap(keymap)
	if err != nil {
		t.Fatal(err)
	}
	if bindings[0].RepeatDelay != 500 || bindings[0].RepeatInterval != 200 {
		t.Errorf("explicit repeat = %d/%d, want 500/200", bindings[0].RepeatDelay, bindings[0].RepeatInterval)
	}
	if bindings[1].RepeatDelay != 400 || bindings[1].RepeatInterval != 300 {
		t.Errorf("default repeat = %d/%d, want 400/300", bindings[1].RepeatDelay, bindings[1].RepeatInterval)
	}
	if bindings[2].RepeatInterval != 0 {
		t.Errorf("a binding with no repeat carries interval %d, want 0", bindings[2].RepeatInterval)
	}
}

// A repeat compiles on any action, a toggle included, and a duration that
// does not parse fails the compile with a message.
func TestCompileRepeatAllowsAToggleAndRejectsABadDuration(t *testing.T) {
	toggle := &Keymap{
		Metadata: ObjectMeta{Name: "pad", Namespace: "house"},
		Spec: KeymapSpec{
			Buttons: []KeymapButton{{Press: "BTN_SOUTH", Action: actionPause, Repeat: &KeymapRepeat{Interval: "500ms"}}},
		},
	}
	if _, err := compileKeymap(toggle); err != nil {
		t.Errorf("a repeat on a toggle should compile: %v", err)
	}

	bad := &Keymap{
		Metadata: ObjectMeta{Name: "pad", Namespace: "house"},
		Spec: KeymapSpec{
			Buttons: []KeymapButton{{Press: "BTN_TR", Action: actionSeek, Amount: 10, Repeat: &KeymapRepeat{Interval: "soon"}}},
		},
	}
	if _, err := compileKeymap(bad); err == nil {
		t.Error("a duration that does not parse should fail the compile")
	}
}
