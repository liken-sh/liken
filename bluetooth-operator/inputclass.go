package main

// The classification of an evdev node, ported from udev.
//
// systemd's input_id builtin decides which ID_INPUT_* properties an
// input device carries, and every Linux desktop reads those
// properties to tell a keyboard from a mouse from a joystick. This
// file is a port of that builtin, src/udev/udev-builtin-input_id.c,
// with udev's names for the classes, so a person who has run
// `udevadm info` on an input device already has the vocabulary. The
// rules are udev's, including its heuristics for keyboards that set
// a stray joystick button; nothing here is liken's own judgment.
//
// Everything in this file is a pure function over the bitmaps and
// properties that evdev.go reads, so the rules test with no kernel.

import "slices"

// inputClasses is a set of the classes one evdev node carries, or one
// claim asks for. udev sets several on a single node, so this is a
// set and never one value.
type inputClasses uint16

// The classes. Each one is an ID_INPUT_* property udev sets, under
// udev's own name in lower case: classTabletPad is ID_INPUT_TABLET_PAD.
const (
	classKey inputClasses = 1 << iota
	classKeyboard
	classMouse
	classPointingStick
	classTouchpad
	classTouchscreen
	classTablet
	classTabletPad
	classJoystick
	classAccelerometer
	classSwitch
)

// everyInputClass is what a claim receives when it names none: every
// class, which is what this driver delivered before the parameter
// existed.
const everyInputClass = classKey | classKeyboard | classMouse | classPointingStick |
	classTouchpad | classTouchscreen | classTablet | classTabletPad |
	classJoystick | classAccelerometer | classSwitch

// inputClassNames is the vocabulary, in the order a message and a
// record list it. The name is udev's property name without the
// ID_INPUT_ prefix, lowercased, so a person who has read
// `udevadm info` about an input device already knows these words.
var inputClassNames = []struct {
	name  string
	class inputClasses
}{
	{"key", classKey},
	{"keyboard", classKeyboard},
	{"mouse", classMouse},
	{"pointingstick", classPointingStick},
	{"touchpad", classTouchpad},
	{"touchscreen", classTouchscreen},
	{"tablet", classTablet},
	{"tablet_pad", classTabletPad},
	{"joystick", classJoystick},
	{"accelerometer", classAccelerometer},
	{"switch", classSwitch},
}

// names lists the classes in a set, in the vocabulary's own order.
func (c inputClasses) names() []string {
	var names []string
	for _, class := range inputClassNames {
		if c&class.class != 0 {
			names = append(names, class.name)
		}
	}
	return names
}

// String is the set as a person reads it in a log line or a test
// failure.
func (c inputClasses) String() string {
	names := c.names()
	if len(names) == 0 {
		return "none"
	}
	out := names[0]
	for _, name := range names[1:] {
		out += ", " + name
	}
	return out
}

// The event types this file tests for. evdev.go reads each one's
// codes into a snapshot keyed by the kernel's own name for the type.
const (
	evSyn = 0x00
	evKey = 0x01
	evRel = 0x02
	evAbs = 0x03
	evMsc = 0x04
	evSw  = 0x05
)

// The device properties input_id tests. The kernel reports them
// through EVIOCGPROP, and evdev.go stores the set bits.
const (
	inputPropDirect        = 0x01
	inputPropPointingStick = 0x05
	inputPropAccelerometer = 0x06
)

// The key codes input_id names. Each range is inclusive of the first
// and exclusive of the last, the way the C code writes its loops.
const (
	btnMisc           = 0x100
	btn0              = 0x100
	btn1              = 0x101
	btnMouse          = 0x110
	btnJoystick       = 0x120
	btnDigi           = 0x140
	btnToolPen        = 0x140
	btnToolFinger     = 0x145
	btnTouch          = 0x14a
	btnStylus         = 0x14b
	keyOK             = 0x160
	btnDpadUp         = 0x220
	btnDpadRight      = 0x223
	keyALSToggle      = 0x230
	btnTriggerHappy   = 0x2c0
	btnTriggerHappy1  = 0x2c0
	btnTriggerHappy40 = 0x2e7
)

// The relative and absolute codes input_id names.
const (
	relX      = 0x00
	relY      = 0x01
	relHWheel = 0x06
	relWheel  = 0x08

	absX           = 0x00
	absY           = 0x01
	absZ           = 0x02
	absRX          = 0x03
	absPressure    = 0x18
	absMTSlot      = 0x2f
	absMTPositionX = 0x35
	absMTPositionY = 0x36
)

// busI2C is the bus a pointing stick uses. The input_id identity test
// treats a device on this bus as a pointing stick because a mouse does
// not use I2C. This test reads the device id rather than the bitmaps.
const busI2C = 0x18

// wellKnownKeyboardKeys is input_id's randomly picked set of key
// groups. A joystick may carry one of them; a keyboard that sets a
// stray joystick button carries several.
var wellKnownKeyboardKeys = []uint16{29, 58, 69, 110, 113, 140, 144, 155, 164, 224}

// highKeyBlocks are the key code ranges above BTN_MISC that hold
// KEY_* codes rather than BTN_* ones. The first is inclusive and the
// second exclusive.
var highKeyBlocks = [][2]uint16{
	{keyOK, btnDpadUp},
	{keyALSToggle, btnTriggerHappy},
}

// nodeInputClasses is every class udev would set on one evdev node.
// The two halves are input_id's test_pointers and test_key, and both
// run on every node, because one node can point and type at once. The
// two rules after them are from the builtin's own body: a node with
// only a wheel counts as a key device, and a node with EV_SW codes is
// a switch.
func nodeInputClasses(caps evdevCapabilities) inputClasses {
	keys, relatives, absolutes := caps.Codes["EV_KEY"], caps.Codes["EV_REL"], caps.Codes["EV_ABS"]
	classes, pointer := testPointers(caps, keys, relatives, absolutes)
	keyClasses, key := testKey(keys)
	classes |= keyClasses

	// A node that carries only a scroll wheel is neither a pointer nor
	// a key device by the tests above, and udev calls it a key device.
	if !pointer && !key && (has(relatives, relWheel) || has(relatives, relHWheel)) {
		classes |= classKey
	}
	if len(caps.Codes["EV_SW"]) > 0 {
		classes |= classSwitch
	}
	return classes
}

// testPointers is input_id's test_pointers. It answers with the
// classes it found and whether the node is a pointer of any kind,
// which the wheel-only fallback above reads.
func testPointers(caps evdevCapabilities, keys, relatives, absolutes []uint16) (inputClasses, bool) {
	hasKeys := len(keys) > 0
	hasAbsCoordinates := has(absolutes, absX) && has(absolutes, absY)
	has3DCoordinates := hasAbsCoordinates && has(absolutes, absZ)
	isAccelerometer := has(caps.Properties, inputPropAccelerometer)

	// Three absolute axes and no keys is an accelerometer even when
	// the driver never set the property.
	if !hasKeys && has3DCoordinates {
		isAccelerometer = true
	}
	if isAccelerometer {
		return classAccelerometer, true
	}

	isPointingStick := has(caps.Properties, inputPropPointingStick)
	hasStylus := has(keys, btnStylus)
	hasPen := has(keys, btnToolPen)
	fingerButNoPen := has(keys, btnToolFinger) && !has(keys, btnToolPen)
	hasMouseButton := anyInRange(keys, btnMouse, btnJoystick)
	hasRelCoordinates := has(relatives, relX) && has(relatives, relY)
	hasMTCoordinates := has(absolutes, absMTPositionX) && has(absolutes, absMTPositionY)

	// A device that sets every absolute axis, the slot and the code
	// before it included, does not report multi-touch positions; it
	// set every bit.
	if hasMTCoordinates && has(absolutes, absMTSlot) && has(absolutes, absMTSlot-1) {
		hasMTCoordinates = false
	}
	isDirect := has(caps.Properties, inputPropDirect)
	hasTouch := has(keys, btnTouch)
	hasPadButtons := has(keys, btn0) && has(keys, btn1) && !hasPen
	hasWheel := has(relatives, relWheel) || has(relatives, relHWheel)

	// The joystick buttons and axes are counted, not tested, because
	// a joystick may have no buttons at all, and a mouse with more than
	// sixteen buttons runs into the joystick button range. The count
	// decides below whether the node is a joystick.
	joystickButtons := 0
	if !has(keys, btnJoystick-1) {
		joystickButtons += countInRange(keys, btnJoystick, btnDigi)
		joystickButtons += countInRange(keys, btnTriggerHappy1, btnTriggerHappy40+1)
		joystickButtons += countInRange(keys, btnDpadUp, btnDpadRight+1)
	}
	joystickAxes := countInRange(absolutes, absRX, absPressure)

	var isMouse, isAbsMouse, isTouchpad, isTouchscreen, isTablet, isTabletPad, isJoystick bool
	if hasAbsCoordinates {
		switch {
		case hasStylus || hasPen:
			isTablet = true
		case fingerButNoPen && !isDirect:
			isTouchpad = true
		case hasMouseButton:
			// A mouse that reports absolute coordinates, such as
			// VMware's virtual USB mouse: axes, mouse buttons, and no
			// touch or pen button.
			isAbsMouse = true
		case hasTouch || isDirect:
			isTouchscreen = true
		case joystickButtons > 0 || joystickAxes > 0:
			isJoystick = true
		}
	} else if joystickButtons > 0 || joystickAxes > 0 {
		isJoystick = true
	}

	if hasMTCoordinates {
		switch {
		case hasStylus || hasPen:
			isTablet = true
		case fingerButNoPen && !isDirect:
			isTouchpad = true
		case hasTouch || isDirect:
			isTouchscreen = true
		}
	}

	if isTablet && hasPadButtons {
		isTabletPad = true
	}
	if hasPadButtons && hasWheel && !hasRelCoordinates {
		isTablet, isTabletPad = true, true
	}
	if !isTablet && !isTouchpad && !isJoystick && hasMouseButton &&
		(hasRelCoordinates || !hasAbsCoordinates) {
		isMouse = true
	}
	// There is no such thing as a mouse on the i2c bus. A mouse there
	// is a laptop's pointing stick.
	if isMouse && caps.ID.Bus == busI2C {
		isPointingStick = true
	}

	// Some keyboards set a stray joystick button. A node that also
	// carries four of the well-known keyboard keys, or fewer than two
	// joystick buttons and axes in total, is not a joystick. libinput
	// applies the same rules.
	if isJoystick {
		wellKnown := 0
		if hasKeys {
			for _, key := range wellKnownKeyboardKeys {
				if has(keys, key) {
					wellKnown++
				}
			}
		}
		if wellKnown >= 4 || joystickButtons+joystickAxes < 2 {
			isJoystick = false
		}
		if hasWheel && hasPadButtons {
			isJoystick = false
		}
	}

	var classes inputClasses
	for _, found := range []struct {
		is    bool
		class inputClasses
	}{
		{isPointingStick, classPointingStick},
		{isMouse || isAbsMouse, classMouse},
		{isTouchpad, classTouchpad},
		{isTouchscreen, classTouchscreen},
		{isJoystick, classJoystick},
		{isTablet, classTablet},
		{isTabletPad, classTabletPad},
	} {
		if found.is {
			classes |= found.class
		}
	}
	pointer := isTablet || isMouse || isAbsMouse || isTouchpad || isTouchscreen || isJoystick || isPointingStick
	return classes, pointer
}

// testKey is input_id's test_key. It answers with the classes it
// found and whether the node is a key device, which the wheel-only
// fallback reads.
func testKey(keys []uint16) (inputClasses, bool) {
	if len(keys) == 0 {
		return 0, false
	}
	// Only KEY_* codes count here, not BTN_* ones, so the search is
	// the block below BTN_MISC and the two blocks of KEY_* codes above
	// it.
	found := anyInRange(keys, 0, btnMisc)
	for _, block := range highKeyBlocks {
		found = found || anyInRange(keys, block[0], block[1])
	}

	var classes inputClasses
	if found {
		classes |= classKey
	}
	// The first 32 codes are escape, the digits, and the keys from Q
	// to D. A node that carries all of them is a full keyboard.
	// KEY_RESERVED, code zero, is not tested.
	for code := uint16(1); code < 32; code++ {
		if !has(keys, code) {
			return classes, found
		}
	}
	return classes | classKeyboard, true
}

// has reports whether a bitmap's set codes include one code.
func has(codes []uint16, code uint16) bool {
	return slices.Contains(codes, code)
}

// anyInRange reports whether any set code falls in a range, which is
// inclusive of first and exclusive of last.
func anyInRange(codes []uint16, first, last uint16) bool {
	return countInRange(codes, first, last) > 0
}

// countInRange counts the set codes in a range, which is inclusive of
// first and exclusive of last.
func countInRange(codes []uint16, first, last uint16) int {
	found := 0
	for _, code := range codes {
		if code >= first && code < last {
			found++
		}
	}
	return found
}
