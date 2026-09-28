package main

// These tests cover the standing pod's fold: what reaches the events
// topic for one raw evdev event, and the repeat the pod synthesises for
// a control that cannot autorepeat.

import (
	"encoding/json"
	"testing"
	"time"
)

// holdBase hands the reader the table the base gives a Remote with no
// Keymap, the way the operator publishes it on the keys topic.
func holdBase(t *testing.T, r *reader) {
	t.Helper()
	payload, err := json.Marshal(baseKeys)
	mustSucceed(t, err)
	r.handle(r.keysTopic, payload)
}

// nextKey reads the next key event off the broker, so a test asserts
// on the name and the value rather than on JSON.
func nextKey(t *testing.T, broker *fakeBroker) keyEvent {
	t.Helper()
	published := waitForPublish(t, broker.pubs)
	if published.retained {
		t.Error("a key event was retained; a press is an event and not a state")
	}
	var event keyEvent
	mustSucceed(t, json.Unmarshal(published.payload, &event))
	return event
}

// A pod that has read no table yet folds on the base, the same rows
// compiled into this binary, so a controller works from the first press.
func TestAReaderFoldsOnTheBaseBeforeAnyTableArrives(t *testing.T) {
	r, broker := testReader(t)

	r.fold(inputEvent{Type: evAbs, Code: axisCodes["ABS_HAT0Y"], Value: -1})

	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_UP", Value: 1})
}

// A key code the table says nothing about passes as itself, so a
// keyboard remote works with no Keymap at all.
func TestAKeyCodeWithNoRowPassesAsItself(t *testing.T) {
	r, broker := testReader(t)
	holdBase(t, r)

	r.fold(inputEvent{Type: evKey, Code: buttonCodes["KEY_PLAYPAUSE"], Value: 1})

	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_PLAYPAUSE", Value: 1})
}

// A control the table maps publishes the key the table names, so a
// gamepad's south button arrives as the enter every consumer binds.
func TestAMappedControlPublishesTheKeyTheTableNames(t *testing.T) {
	r, broker := testReader(t)
	holdBase(t, r)

	r.fold(inputEvent{Type: evKey, Code: buttonCodes["BTN_SOUTH"], Value: 1})

	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_ENTER", Value: 1})
}

// The release of a mapped control carries the mapped name too, so a
// consumer that counts edges sees the pair it expects.
func TestTheReleaseOfAMappedControlCarriesTheSameKey(t *testing.T) {
	r, broker := testReader(t)
	holdBase(t, r)

	r.fold(inputEvent{Type: evKey, Code: buttonCodes["BTN_SOUTH"], Value: 0})

	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_ENTER", Value: 0})
}

// A control the table maps to none publishes nothing, which is how a
// Keymap silences a key the base would otherwise pass.
func TestAControlMappedToNonePublishesNothing(t *testing.T) {
	r, broker := testReader(t)
	table := append([]compiledBinding{{
		EventType: evKey, Code: buttonCodes["KEY_COMPOSE"], Value: 1, Key: keyNone,
	}}, baseKeys...)
	payload, err := json.Marshal(table)
	mustSucceed(t, err)
	r.handle(r.keysTopic, payload)

	r.fold(inputEvent{Type: evKey, Code: buttonCodes["KEY_COMPOSE"], Value: 1})
	r.fold(inputEvent{Type: evKey, Code: buttonCodes["KEY_PLAYPAUSE"], Value: 1})

	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_PLAYPAUSE", Value: 1})
}

// A code the kernel gives no name publishes nothing, because the name
// is the whole of what a consumer reads.
func TestACodeTheNameTableDoesNotKnowPublishesNothing(t *testing.T) {
	r, broker := testReader(t)
	holdBase(t, r)

	r.fold(inputEvent{Type: evKey, Code: 0x2ff, Value: 1})
	r.fold(inputEvent{Type: evKey, Code: buttonCodes["KEY_PLAYPAUSE"], Value: 1})

	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_PLAYPAUSE", Value: 1})
}

// A hat direction publishes an arrow's press, and the return to center
// publishes that same arrow's release, because the center names no
// direction of its own.
func TestAHatPublishesAnArrowAndItsReturnToCenterReleasesIt(t *testing.T) {
	r, broker := testReader(t)
	holdBase(t, r)

	r.fold(inputEvent{Type: evAbs, Code: axisCodes["ABS_HAT0X"], Value: -1})
	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_LEFT", Value: 1})

	r.fold(inputEvent{Type: evAbs, Code: axisCodes["ABS_HAT0X"], Value: 0})
	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_LEFT", Value: 0})
}

// A hat that returns to center with no press before it publishes
// nothing, so a controller that reports a resting axis at connect is
// quiet.
func TestAHatAtCenterWithNoPressPublishesNothing(t *testing.T) {
	r, broker := testReader(t)
	holdBase(t, r)

	r.fold(inputEvent{Type: evAbs, Code: axisCodes["ABS_HAT0Y"], Value: 0})
	r.fold(inputEvent{Type: evKey, Code: buttonCodes["KEY_PLAYPAUSE"], Value: 1})

	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_PLAYPAUSE", Value: 1})
}

// A row with a repeat block makes the pod publish value 2 while the
// control is held, because a hat never autorepeats in the kernel.
func TestARowWithARepeatPublishesValueTwoWhileTheControlIsHeld(t *testing.T) {
	r, broker := testReader(t)
	payload, err := json.Marshal([]compiledBinding{{
		EventType: evAbs, Code: axisCodes["ABS_HAT0Y"], Value: -1, Key: "KEY_UP",
		RepeatDelay: 1, RepeatInterval: 1,
	}})
	mustSucceed(t, err)
	r.handle(r.keysTopic, payload)

	r.fold(inputEvent{Type: evAbs, Code: axisCodes["ABS_HAT0Y"], Value: -1})
	defer r.stopAllRepeats()

	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_UP", Value: 1})
	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_UP", Value: 2})
}

// The release stops the synthesised repeat, so a control let go of
// stops stepping.
func TestTheReleaseStopsTheSynthesisedRepeat(t *testing.T) {
	r, broker := testReader(t)
	payload, err := json.Marshal([]compiledBinding{{
		EventType: evAbs, Code: axisCodes["ABS_HAT0Y"], Value: -1, Key: "KEY_UP",
		RepeatDelay: 10, RepeatInterval: 10,
	}})
	mustSucceed(t, err)
	r.handle(r.keysTopic, payload)

	r.fold(inputEvent{Type: evAbs, Code: axisCodes["ABS_HAT0Y"], Value: -1})
	r.fold(inputEvent{Type: evAbs, Code: axisCodes["ABS_HAT0Y"], Value: 0})

	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_UP", Value: 1})
	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_UP", Value: 0})
	time.Sleep(30 * time.Millisecond)
	select {
	case published := <-broker.pubs:
		t.Errorf("the repeat published %q after the release", published.payload)
	default:
	}
}

// A keyboard's own autorepeat passes through as value 2, and the pod
// synthesises nothing beside it.
func TestAKernelAutorepeatPassesThroughAsValueTwo(t *testing.T) {
	r, broker := testReader(t)
	holdBase(t, r)

	r.fold(inputEvent{Type: evKey, Code: buttonCodes["KEY_UP"], Value: 2})

	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_UP", Value: 2})
}

// A table that does not decode leaves the last good one in place, so a
// broken publish does not stop a controller mid-film.
func TestATableThatDoesNotDecodeLeavesTheLastOneInPlace(t *testing.T) {
	r, broker := testReader(t)
	holdBase(t, r)

	r.handle(r.keysTopic, []byte("not json"))
	r.fold(inputEvent{Type: evKey, Code: buttonCodes["BTN_SOUTH"], Value: 1})

	mustMatch(t, nextKey(t, broker), keyEvent{Key: "KEY_ENTER", Value: 1})
}

// A repeat off the bus is held under the ceiling, so a table from
// anywhere else cannot overflow the ticker a held control starts.
func TestARepeatOffTheBusIsHeldUnderTheCeiling(t *testing.T) {
	r, _ := testReader(t)
	payload, err := json.Marshal([]compiledBinding{{
		EventType: evKey, Code: buttonCodes["BTN_SOUTH"], Value: 1, Key: "KEY_ENTER",
		RepeatDelay: 1 << 40, RepeatInterval: 1 << 40,
	}})
	mustSucceed(t, err)

	r.handle(r.keysTopic, payload)

	held := r.table()
	mustMatch(t, held[0].RepeatDelay, maxRepeatMillis)
	mustMatch(t, held[0].RepeatInterval, maxRepeatMillis)
}

// A press earns one line: the control, and the key the pod published
// or the reason it published none. The release and every repeat earn
// none, so a held control is one line.
func TestAPressEarnsOneLineAndItsRepeatAndReleaseNone(t *testing.T) {
	events := remoteEventsTopic(defaultTopicBase, "house", "sofa")
	cases := []struct {
		name  string
		table []compiledBinding
		event inputEvent
		want  string
	}{
		{
			name:  "a key the table maps",
			event: inputEvent{Type: evKey, Code: buttonCodes["BTN_SOUTH"], Value: 1},
			want:  "BTN_SOUTH (304) pressed, published KEY_ENTER to " + events,
		},
		{
			name:  "a key that passes as itself",
			event: inputEvent{Type: evKey, Code: buttonCodes["KEY_PLAYPAUSE"], Value: 1},
			want:  "KEY_PLAYPAUSE (164) pressed, published KEY_PLAYPAUSE to " + events,
		},
		{
			name:  "a key the table maps to none",
			table: []compiledBinding{{EventType: evKey, Code: buttonCodes["BTN_TL"], Value: 1, Key: keyNone}},
			event: inputEvent{Type: evKey, Code: buttonCodes["BTN_TL"], Value: 1},
			want:  "BTN_TL (310) pressed, published nothing, because the key table maps it to none",
		},
		{
			name:  "a code the kernel gives no name",
			event: inputEvent{Type: evKey, Code: 0x2ff, Value: 1},
			want:  "767 pressed, published nothing, because the kernel gives the code no name and the key table has no row for it",
		},
		{
			name:  "a hat direction the table maps",
			event: inputEvent{Type: evAbs, Code: axisCodes["ABS_HAT0Y"], Value: -1},
			want:  "ABS_HAT0Y (17) -1 pressed, published KEY_UP to " + events,
		},
		{
			name:  "a hat direction with no row",
			table: []compiledBinding{},
			event: inputEvent{Type: evAbs, Code: axisCodes["ABS_HAT0Y"], Value: -1},
			want:  "ABS_HAT0Y (17) -1 pressed, published nothing, because the key table has no row for it",
		},
		{
			name:  "a hat direction the table maps to none",
			table: []compiledBinding{{EventType: evAbs, Code: axisCodes["ABS_HAT0X"], Value: 1, Key: keyNone}},
			event: inputEvent{Type: evAbs, Code: axisCodes["ABS_HAT0X"], Value: 1},
			want:  "ABS_HAT0X (16) 1 pressed, published nothing, because the key table maps it to none",
		},
		{
			name:  "a kernel repeat",
			event: inputEvent{Type: evKey, Code: buttonCodes["KEY_UP"], Value: 2},
		},
		{
			name:  "a release",
			event: inputEvent{Type: evKey, Code: buttonCodes["KEY_UP"], Value: 0},
		},
		{
			name:  "the repeat of a key the table maps to none",
			table: []compiledBinding{{EventType: evKey, Code: buttonCodes["BTN_TL"], Value: 1, Key: keyNone}},
			event: inputEvent{Type: evKey, Code: buttonCodes["BTN_TL"], Value: 2},
		},
		{
			name:  "a hat return to center",
			event: inputEvent{Type: evAbs, Code: axisCodes["ABS_HAT0Y"], Value: 0},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			r, _ := testReader(t)
			if each.table != nil {
				r.keys.table = each.table
			}

			mustMatch(t, r.fold(each.event), each.want)
		})
	}
}

// A new table is a person's edit reaching the pod, so it gets a line.
// The same table again, as a reconnect delivers it, gets none, and a
// table that does not decode says why in the decoder's own words.
func TestTheReaderLogsANewTableOnce(t *testing.T) {
	r, _ := testReader(t)
	var log logBuffer
	r.log = &log
	table := []compiledBinding{{EventType: evKey, Code: buttonCodes["BTN_TL"], Value: 1, Key: keyNone}}
	payload, err := json.Marshal(table)
	mustSucceed(t, err)

	r.handle(r.keysTopic, payload)
	r.handle(r.keysTopic, payload)

	mustLogOnce(t, &log, "remote: key table on "+r.keysTopic+" applied: 1 row")
}

func TestTheReaderLogsATableItCannotRead(t *testing.T) {
	r, _ := testReader(t)
	var log logBuffer
	r.log = &log

	r.handle(r.keysTopic, []byte("not json"))

	mustLogOnce(t, &log, "remote: key table on "+r.keysTopic+" ignored, the last good table stays: invalid character 'o' in literal null")
}
