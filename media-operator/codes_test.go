package main

import (
	"testing"
	"testing/synctest"
	"time"
)

func testDeclared() remoteCodes {
	return remoteCodes{Keys: []uint16{0x130, 0x131}, Axes: []uint16{0x10}}
}

// woke reports whether a wake arrives within 50ms on the fake clock of
// the test's synctest bubble.
func woke(wake <-chan struct{}) bool {
	select {
	case <-wake:
		return true
	case <-time.After(50 * time.Millisecond):
		return false
	}
}

func TestNewCodesWakeThePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wake := make(chan struct{}, 1)
		cache := newCodesCache(wake)

		cache.setCodes(controllerKey("house", "sofa"), testDeclared())

		mustMatch(t, woke(wake), true)
	})
}

func TestRepeatedCodesDoNotWakeThePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wake := make(chan struct{}, 1)
		cache := newCodesCache(wake)
		cache.setCodes(controllerKey("house", "sofa"), testDeclared())
		<-wake

		cache.setCodes(controllerKey("house", "sofa"), testDeclared())

		mustMatch(t, woke(wake), false)
	})
}

func TestClearingCodesDeletesThemAndWakesThePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wake := make(chan struct{}, 1)
		cache := newCodesCache(wake)
		cache.setCodes(controllerKey("house", "sofa"), testDeclared())
		<-wake

		cache.clear(controllerKey("house", "sofa"))

		_, held := cache.codesFor(controllerKey("house", "sofa"))
		mustMatch(t, held, false)
		mustMatch(t, woke(wake), true)
	})
}

func TestCodesAreIgnoredWhileThePodIsOffline(t *testing.T) {
	cache := newCodesCache(nil)
	cache.setCodes(controllerKey("house", "sofa"), testDeclared())

	cache.setAvailability(controllerKey("house", "sofa"), false)

	_, held := cache.codesFor(controllerKey("house", "sofa"))
	mustMatch(t, held, false)

	cache.setAvailability(controllerKey("house", "sofa"), true)
	codes, held := cache.codesFor(controllerKey("house", "sofa"))
	mustMatch(t, held, true)
	mustMatchAll(t, codes.Keys, testDeclared().Keys)
}

func TestRetainDeletesTheCodesOfADeletedRemote(t *testing.T) {
	cache := newCodesCache(nil)
	cache.setCodes(controllerKey("house", "sofa"), testDeclared())
	cache.setAvailability(controllerKey("house", "sofa"), true)
	cache.setCodes(controllerKey("house", "gone"), testDeclared())

	cache.retain(map[string]bool{controllerKey("house", "sofa"): true})

	_, held := cache.codesFor(controllerKey("house", "sofa"))
	mustMatch(t, held, true)
	_, stillHeld := cache.codesFor(controllerKey("house", "gone"))
	mustMatch(t, stillHeld, false)
}

func TestUnboundCodesAreTheCodesTheTableMapsToNoKey(t *testing.T) {
	cases := []struct {
		name     string
		declared remoteCodes
		table    []compiledBinding
		want     []UnboundCode
	}{
		{
			name:     "a controller on the base alone",
			declared: remoteCodes{Keys: []uint16{0x130}, Axes: []uint16{0x11}},
			table:    baseKeys,
			want:     nil,
		},
		{
			name:     "a key the table drops with none",
			declared: remoteCodes{Keys: []uint16{0x130, 0x131}},
			table: []compiledBinding{
				{EventType: evKey, Code: 0x131, Value: 1, Key: keyNone},
			},
			want: []UnboundCode{{Code: 0x131, Name: "BTN_EAST", Type: unboundKey}},
		},
		{
			name:     "a key with no row at all passes as itself",
			declared: remoteCodes{Keys: []uint16{0x0a4}},
			want:     nil,
		},
		{
			name:     "one direction of a hat binds the axis",
			declared: remoteCodes{Axes: []uint16{0x10, 0x11}},
			table: []compiledBinding{
				{EventType: evAbs, Code: 0x11, Value: 1, Key: "KEY_DOWN"},
			},
			want: []UnboundCode{{Code: 0x10, Name: "ABS_HAT0X", Type: unboundAxis}},
		},
		{
			name:     "a hat both of whose directions the table drops",
			declared: remoteCodes{Axes: []uint16{0x11}},
			table: []compiledBinding{
				{EventType: evAbs, Code: 0x11, Value: -1, Key: keyNone},
				{EventType: evAbs, Code: 0x11, Value: 1, Key: keyNone},
			},
			want: []UnboundCode{{Code: 0x11, Name: "ABS_HAT0Y", Type: unboundAxis}},
		},
		{
			name:     "a code the kernel names nothing",
			declared: remoteCodes{Keys: []uint16{0x2ff}},
			want:     []UnboundCode{{Code: 0x2ff, Type: unboundKey}},
		},
		{
			name:     "a controller that declares nothing",
			declared: remoteCodes{},
			want:     nil,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatchAll(t, unboundCodes(each.declared, each.table), each.want)
		})
	}
}
