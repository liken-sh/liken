package main

// This file ports systemd's input_id builtin
// (src/udev/udev-builtin-input_id.c), which sets the ID_INPUT_*
// properties that say whether an evdev node is a keyboard, a mouse, a
// joystick, and so on. It keeps the builtin's rules and names and adds
// none, so a node gets the classes that `udevadm info` shows for it.
// The functions read only the capabilities that evdev.go reads, so the
// tests need no kernel.

import "slices"

// inputClasses is a bit set of the classes an evdev node has, or that
// a claim asks for. A remote that both types and points has several.
type inputClasses uint16

// Each class corresponds to the udev property ID_INPUT_<NAME>, so
// classTabletPad is ID_INPUT_TABLET_PAD.
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

// everyInputClass is the default for a claim that names no classes.
const everyInputClass = classKey | classKeyboard | classMouse | classPointingStick |
	classTouchpad | classTouchscreen | classTablet | classTabletPad |
	classJoystick | classAccelerometer | classSwitch

// inputClassNames maps each class to its name, which is udev's
// property name without the ID_INPUT_ prefix, in lower case. The order
// is the order that messages and records list the classes in.
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

// names returns the names of the classes in the set, in the order of
// inputClassNames.
func (c inputClasses) names() []string {
	var names []string
	for _, class := range inputClassNames {
		if c&class.class != 0 {
			names = append(names, class.name)
		}
	}
	return names
}

// String returns the class names separated by commas, or "none" for an
// empty set.
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

// The kernel's evdev event type numbers. evdevCapabilities.Codes keys
// each type's codes by its name instead, such as "EV_KEY".
const (
	evSyn = 0x00
	evKey = 0x01
	evRel = 0x02
	evAbs = 0x03
	evMsc = 0x04
	evSw  = 0x05
)

// The device properties input_id tests. The kernel reports them
// through the EVIOCGPROP ioctl, and evdev.go stores the set bits in
// evdevCapabilities.Properties.
const (
	inputPropDirect        = 0x01
	inputPropPointingStick = 0x05
	inputPropAccelerometer = 0x06
)

// The key codes input_id tests.
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

// The relative and absolute axis codes input_id tests.
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

// busI2C is the bus type of an I2C device.
const busI2C = 0x18

// wellKnownKeyboardKeys are input_id's key codes for recognizing a
// keyboard that sets a stray joystick button.
var wellKnownKeyboardKeys = []uint16{29, 58, 69, 110, 113, 140, 144, 155, 164, 224}

// highKeyBlocks are the ranges of KEY_* codes above BTN_MISC. Each
// range includes its first code and excludes its last.
var highKeyBlocks = [][2]uint16{
	{keyOK, btnDpadUp},
	{keyALSToggle, btnTriggerHappy},
}

// nodeInputClasses returns the classes udev would set on an evdev
// node. It runs both test_pointers and test_key on every node, because
// one node can both point and type.
func nodeInputClasses(caps evdevCapabilities) inputClasses {
	keys, relatives, absolutes := caps.Codes["EV_KEY"], caps.Codes["EV_REL"], caps.Codes["EV_ABS"]
	classes, pointer := testPointers(caps, keys, relatives, absolutes)
	keyClasses, key := testKey(keys)
	classes |= keyClasses

	// A node with only a scroll wheel passes neither test above, and
	// udev still classes it as a key device.
	if !pointer && !key && (has(relatives, relWheel) || has(relatives, relHWheel)) {
		classes |= classKey
	}
	if len(caps.Codes["EV_SW"]) > 0 {
		classes |= classSwitch
	}
	return classes
}

// testPointers is input_id's test_pointers. It returns the pointer
// classes of the node and whether the node is any kind of pointer.
func testPointers(caps evdevCapabilities, keys, relatives, absolutes []uint16) (inputClasses, bool) {
	hasKeys := len(keys) > 0
	hasAbsCoordinates := has(absolutes, absX) && has(absolutes, absY)
	has3DCoordinates := hasAbsCoordinates && has(absolutes, absZ)
	isAccelerometer := has(caps.Properties, inputPropAccelerometer)

	// A node with X, Y, and Z axes and no keys is an accelerometer even
	// when the driver did not set the property.
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

	// A device with both ABS_MT_SLOT and the code before it set has
	// set every axis bit, so its multi-touch position bits mean
	// nothing.
	if hasMTCoordinates && has(absolutes, absMTSlot) && has(absolutes, absMTSlot-1) {
		hasMTCoordinates = false
	}
	isDirect := has(caps.Properties, inputPropDirect)
	hasTouch := has(keys, btnTouch)
	hasPadButtons := has(keys, btn0) && has(keys, btn1) && !hasPen
	hasWheel := has(relatives, relWheel) || has(relatives, relHWheel)

	// A mouse with more than 16 buttons sets BTN_JOYSTICK-1 and codes
	// above it, so a node with BTN_JOYSTICK-1 counts no joystick
	// buttons.
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
			// A mouse with absolute coordinates, such as the USB mouse
			// of a virtual machine.
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
	// input_id treats a mouse on the I2C bus as a laptop's
	// pointing stick.
	if isMouse && caps.ID.Bus == busI2C {
		isPointingStick = true
	}

	// Some keyboards set a stray joystick button, so these rules
	// clear isJoystick for a node with keyboard keys or with almost
	// no joystick buttons and axes. libinput applies the same rules.
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

// testKey is input_id's test_key. It returns the key classes of the
// node and whether the node is a key device.
func testKey(keys []uint16) (inputClasses, bool) {
	if len(keys) == 0 {
		return 0, false
	}
	// Only KEY_* codes count, not BTN_* codes.
	found := anyInRange(keys, 0, btnMisc)
	for _, block := range highKeyBlocks {
		found = found || anyInRange(keys, block[0], block[1])
	}

	var classes inputClasses
	if found {
		classes |= classKey
	}
	// Codes 1 through 31 are escape, the digits, and the letter keys
	// from Q to D. Code 0 is KEY_RESERVED.
	for code := uint16(1); code < 32; code++ {
		if !has(keys, code) {
			return classes, found
		}
	}
	return classes | classKeyboard, true
}

func has(codes []uint16, code uint16) bool {
	return slices.Contains(codes, code)
}

// anyInRange reports whether codes has a code in [first, last).
func anyInRange(codes []uint16, first, last uint16) bool {
	return countInRange(codes, first, last) > 0
}

// countInRange counts the codes in [first, last).
func countInRange(codes []uint16, first, last uint16) int {
	found := 0
	for _, code := range codes {
		if code >= first && code < last {
			found++
		}
	}
	return found
}
