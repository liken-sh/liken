package main

// The card's own mixer: the control elements a sound card declares,
// read and written through the control interface without libasound.
//
// A card publishes its hardware controls as elements on
// /dev/snd/controlC<N>: a USB DAC's PCM Playback Volume, a Realtek
// codec's Master Playback Switch, an Auto-Mute Mode selector. Nothing
// else in this pod touches them. The declared adapter nodes carry no
// ACP device, so PipeWire never opens the mixer, and every volume it
// applies is a software gain. That leaves the hardware wherever ALSA
// left it at boot, and it makes this operator the one writer of these
// registers.
//
// This file reads what each element declares, spells one value the
// way spec.controls and status.observed.controls carry it, and writes
// a declared value back. A jack element is read here too, because it
// is on the same device, and reported elsewhere: it is not a
// capability, because nothing can set it. It reports whether a plug
// is in, and the Connected condition reads it.

import (
	"encoding/binary"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"unsafe"
)

// The element types the control interface publishes, from
// include/uapi/sound/asound.h. These three are the three words
// status.capabilities uses. The others, bytes, an IEC958 channel
// status, and a 64-bit integer, have no spelling in the API and are
// not capabilities.
const (
	ctlElemTypeBoolean    = 1
	ctlElemTypeInteger    = 2
	ctlElemTypeEnumerated = 3
)

// The interfaces an element belongs to. A jack is on the card
// interface, and a control a person sets is on the mixer interface.
// The PCM interface carries the ELD and the channel map, which
// alsa.go reads and this file leaves alone.
const (
	ctlElemIfaceCard  = 0
	ctlElemIfaceMixer = 2
)

// The access bits this file reads: whether an element takes a write,
// whether it declares a dB range through TLV, and whether the card has
// switched it off for now. An inactive element is one a routing
// change disabled, and a write to it does nothing.
const (
	ctlAccessWrite    = 1 << 1
	ctlAccessTLVRead  = 1 << 4
	ctlAccessInactive = 1 << 8
)

// The request that writes one element's value. It takes the same
// 1224-byte structure the read does, and alsa.go builds the other
// three requests the same way.
var ctlIoctlElemWrite = iowr(0x13, ctlElemValueSize)

// Where each member of the info value union starts, from
// include/uapi/sound/asound.h. An integer element carries three longs,
// and an enumerated element carries its item count, the item a caller
// asks about, and that item's name.
const (
	infoIntegerMin      = 0
	infoIntegerMax      = 8
	infoIntegerStep     = 16
	infoEnumeratedItems = 0
	infoEnumeratedItem  = 4
	infoEnumeratedName  = 8
	enumeratedNameSize  = 64
)

// The type words status.capabilities uses. They are the CRD's
// enumeration, and a reader of the resource never sees the kernel's
// numbers.
const (
	capabilityInteger    = "integer"
	capabilityBoolean    = "boolean"
	capabilityEnumerated = "enumerated"
)

// The spelling a boolean control takes in spec.controls and reports
// in status.observed.controls. amixer uses the same two words, so a
// person who has set a switch by hand has seen them.
const (
	controlOn  = "on"
	controlOff = "off"
)

// controlCapability is one control as status.capabilities reports it.
//
// The three number fields are pointers because a minimum of zero is
// the common case, and a reader has to tell it from no minimum at
// all: a boolean declares no range, and an integer always does.
type controlCapability struct {
	Type        string   `json:"type"`
	Min         *int64   `json:"min,omitempty"`
	Max         *int64   `json:"max,omitempty"`
	Step        *int64   `json:"step,omitempty"`
	MinDecibels string   `json:"minDecibels,omitempty"`
	MaxDecibels string   `json:"maxDecibels,omitempty"`
	Values      []string `json:"values,omitempty"`
	Channels    int      `json:"channels,omitempty"`
}

// control is one control element: the identity the kernel gives it,
// and what it accepts.
//
// The index is kept beside the name because a name is not unique on
// a card. An Intel HDMI codec declares one IEC958 Playback Switch per
// slot, all under one name, and the index is what tells the slots
// apart. The attach rule hands each HDMI sink the element with its
// own index, and a write through that element reaches the right slot.
type control struct {
	NumID      uint32
	Interface  int32
	Device     uint32
	Index      uint32
	Name       string
	Capability controlCapability
}

// mixer is one card's control device, open, with everything the card
// declares read once.
type mixer struct {
	card     int
	device   *os.File
	controls []control
	jacks    []control
	byName   map[string]control
}

// openMixer opens a card's control device and reads every element it
// declares.
//
// The device is opened for reading and writing because a declared
// control is written through the same descriptor it is read through,
// and the kernel checks the element's own write access, not the
// descriptor's mode.
func openMixer(card int) (*mixer, error) {
	device, err := os.OpenFile(fmt.Sprintf("%s/controlC%d", sndDir, card), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	m := &mixer{card: card, device: device, byName: map[string]control{}}
	if err := m.enumerate(); err != nil {
		device.Close()
		return nil, err
	}
	return m, nil
}

func (m *mixer) Close() error { return m.device.Close() }

// enumerate reads every element the card declares and sorts it into
// the controls a spec may write and the jacks the Connected condition
// reads.
//
// Four rules leave an element out. It is inactive, so a write would
// do nothing. It takes no write and is not a jack, so nothing can be
// declared for it. Its type has no spelling in the API. Or the card
// refuses to describe it, in which case the rest of the card still
// reads.
func (m *mixer) enumerate() error {
	ids, err := listControlElements(m.device)
	if err != nil {
		return err
	}
	for _, id := range ids {
		info := ctlElemInfo{ID: id}
		if err := ioctl(m.device, ctlIoctlElemInfo, unsafe.Pointer(&info)); err != nil {
			fmt.Fprintf(os.Stderr, "reading the information of control %q on card %d: %v\n",
				id.name(), m.card, err)
			continue
		}
		if info.Access&ctlAccessInactive != 0 {
			continue
		}

		element := control{
			NumID:     id.NumID,
			Interface: id.Interface,
			Device:    id.Device,
			Index:     id.Index,
			Name:      id.name(),
		}
		if isJackControl(element.Interface, element.Name) && info.Type == ctlElemTypeBoolean {
			element.Capability = controlCapability{Type: capabilityBoolean, Channels: int(info.Count)}
			m.jacks = append(m.jacks, element)
			continue
		}
		if info.Access&ctlAccessWrite == 0 {
			continue
		}

		items, err := m.enumeratedItems(info)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the values of control %q on card %d: %v\n",
				element.Name, m.card, err)
			continue
		}
		capability, spelled := capabilityFrom(info, items, m.decibels(info))
		if !spelled {
			continue
		}
		element.Capability = capability
		m.controls = append(m.controls, element)
		if _, taken := m.byName[element.Name]; !taken {
			// The first element of a name keeps the name. A card with
			// several HDMI slots declares one IEC958 control per slot
			// under one name, and a caller that means a particular slot
			// holds the element itself and reads or writes through it.
			m.byName[element.Name] = element
		}
	}
	return nil
}

// isJackControl reports whether an element is a jack: a boolean on
// the card interface whose name ends in the word. The kernel's jack
// layer creates one for every socket the codec can sense, and the
// element reports whether a plug is in.
func isJackControl(iface int32, name string) bool {
	return iface == ctlElemIfaceCard && strings.HasSuffix(name, " Jack")
}

// capabilityFrom turns one element's information into the capability
// the API reports, and says whether the element has a spelling at all.
func capabilityFrom(info ctlElemInfo, items []string, levels *decibelRange) (controlCapability, bool) {
	capability := controlCapability{Channels: int(info.Count)}
	switch info.Type {
	case ctlElemTypeBoolean:
		capability.Type = capabilityBoolean
	case ctlElemTypeEnumerated:
		capability.Type = capabilityEnumerated
		capability.Values = items
	case ctlElemTypeInteger:
		capability.Type = capabilityInteger
		min, max, step := integerRange(info.Value[:])
		capability.Min, capability.Max = &min, &max
		if step != 0 {
			// A step of zero means the control accepts every number
			// in its range, so the API leaves the field out.
			capability.Step = &step
		}
		if levels != nil {
			if levels.Low != gainMute {
				capability.MinDecibels = formatDecibels(levels.Low)
			}
			if levels.High != gainMute {
				capability.MaxDecibels = formatDecibels(levels.High)
			}
		}
	default:
		return controlCapability{}, false
	}
	return capability, true
}

// integerRange reads an integer element's three numbers out of the
// value union.
func integerRange(value []byte) (min, max, step int64) {
	return int64(binary.NativeEndian.Uint64(value[infoIntegerMin : infoIntegerMin+8])),
		int64(binary.NativeEndian.Uint64(value[infoIntegerMax : infoIntegerMax+8])),
		int64(binary.NativeEndian.Uint64(value[infoIntegerStep : infoIntegerStep+8]))
}

// enumeratedItems reads the name of every value an enumerated control
// accepts.
//
// This is one ioctl per item because the information call answers
// for the one item number the caller writes into the union. The card
// returns one name at a time, and a selector with three values costs
// three calls, once, when the mixer opens.
func (m *mixer) enumeratedItems(info ctlElemInfo) ([]string, error) {
	if info.Type != ctlElemTypeEnumerated {
		return nil, nil
	}
	count := binary.NativeEndian.Uint32(info.Value[infoEnumeratedItems : infoEnumeratedItems+4])
	items := make([]string, 0, count)
	for item := uint32(0); item < count; item++ {
		query := ctlElemInfo{ID: ctlElemID{NumID: info.ID.NumID}}
		binary.NativeEndian.PutUint32(query.Value[infoEnumeratedItem:infoEnumeratedItem+4], item)
		if err := ioctl(m.device, ctlIoctlElemInfo, unsafe.Pointer(&query)); err != nil {
			return nil, fmt.Errorf("reading value %d: %w", item, err)
		}
		items = append(items, nullTerminated(query.Value[infoEnumeratedName:infoEnumeratedName+enumeratedNameSize]))
	}
	return items, nil
}

// readControl reads one control by the name the card gave it.
func (m *mixer) readControl(name string) (string, error) {
	element, declared := m.byName[name]
	if !declared {
		return "", fmt.Errorf("card %d declares no control named %q", m.card, name)
	}
	return m.readElement(element)
}

// readElement reads one element's current value in the API's spelling.
func (m *mixer) readElement(element control) (string, error) {
	value := ctlElemValue{ID: ctlElemID{NumID: element.NumID}}
	if err := ioctl(m.device, ctlIoctlElemRead, unsafe.Pointer(&value)); err != nil {
		return "", fmt.Errorf("reading control %q on card %d: %w", element.Name, m.card, err)
	}
	return controlValueFrom(element.Capability, value.Value[:])
}

// writeControl writes one control by the name the card gave it.
func (m *mixer) writeControl(name, value string) error {
	element, declared := m.byName[name]
	if !declared {
		return fmt.Errorf("card %d declares no control named %q", m.card, name)
	}
	return m.writeElement(element, value)
}

// writeElement writes one value into every channel of one element.
//
// Every channel takes the same value because spec.controls declares
// one value per control. Per-channel levels are a balance control,
// and no consumer has asked for one.
func (m *mixer) writeElement(element control, value string) error {
	raw := ctlElemValue{ID: ctlElemID{NumID: element.NumID}}
	if err := encodeControlValue(element.Capability, value, raw.Value[:]); err != nil {
		return fmt.Errorf("writing control %q on card %d: %w", element.Name, m.card, err)
	}
	if err := ioctl(m.device, ctlIoctlElemWrite, unsafe.Pointer(&raw)); err != nil {
		return fmt.Errorf("writing control %q on card %d: %w", element.Name, m.card, err)
	}
	return nil
}

// jackStates reads every jack the card senses, keyed by the jack's
// name. The Connected condition reads the result, and a card that
// senses no jack, such as a USB device, answers with an empty map.
func (m *mixer) jackStates() (map[string]bool, error) {
	states := map[string]bool{}
	for _, jack := range m.jacks {
		value, err := m.readElement(jack)
		if err != nil {
			return nil, err
		}
		states[jack.Name] = value == controlOn
	}
	return states, nil
}

// controlValueFrom spells one element's value the way the API carries
// it.
//
// A control with several channels reports the first channel's value,
// because the operator writes every channel alike and a person set
// them together. A card whose channels a hand-run tool set apart
// shows the first one here, and a declared value brings them back
// together.
func controlValueFrom(capability controlCapability, raw []byte) (string, error) {
	switch capability.Type {
	case capabilityInteger, capabilityBoolean:
		if len(raw) < 8 {
			return "", fmt.Errorf("the value holds %d bytes, and a number takes 8", len(raw))
		}
		number := int64(binary.NativeEndian.Uint64(raw[:8]))
		if capability.Type == capabilityInteger {
			return strconv.FormatInt(number, 10), nil
		}
		if number != 0 {
			return controlOn, nil
		}
		return controlOff, nil
	case capabilityEnumerated:
		if len(raw) < 4 {
			return "", fmt.Errorf("the value holds %d bytes, and an item takes 4", len(raw))
		}
		item := int(binary.NativeEndian.Uint32(raw[:4]))
		if item < 0 || item >= len(capability.Values) {
			return "", fmt.Errorf("the control reads value %d, and it declares %d", item, len(capability.Values))
		}
		return capability.Values[item], nil
	}
	return "", fmt.Errorf("a control of type %q has no spelling", capability.Type)
}

// encodeControlValue parses the API's spelling back and writes it into
// every channel of an element's value union.
func encodeControlValue(capability controlCapability, value string, raw []byte) error {
	channels := capability.Channels
	if channels < 1 {
		channels = 1
	}
	switch capability.Type {
	case capabilityInteger:
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("%q is not a number, and this control takes one", value)
		}
		if capability.Min != nil && number < *capability.Min ||
			capability.Max != nil && number > *capability.Max {
			return fmt.Errorf("%d is outside the control's range %s to %s",
				number, rangeEnd(capability.Min), rangeEnd(capability.Max))
		}
		return fillChannels(raw, 8, channels, uint64(number))
	case capabilityBoolean:
		switch value {
		case controlOn:
			return fillChannels(raw, 8, channels, 1)
		case controlOff:
			return fillChannels(raw, 8, channels, 0)
		}
		return fmt.Errorf("%q is neither %q nor %q", value, controlOn, controlOff)
	case capabilityEnumerated:
		item := slices.Index(capability.Values, value)
		if item < 0 {
			return fmt.Errorf("%q is not one of %s", value, strings.Join(capability.Values, ", "))
		}
		return fillChannels(raw, 4, channels, uint64(item))
	}
	return fmt.Errorf("a control of type %q takes no value", capability.Type)
}

// rangeEnd spells one end of a range for an error message, and says
// so where the control declares no end.
func rangeEnd(end *int64) string {
	if end == nil {
		return "none"
	}
	return strconv.FormatInt(*end, 10)
}

// fillChannels writes one number into every channel of a value union.
func fillChannels(raw []byte, width, channels int, number uint64) error {
	if len(raw) < width*channels {
		return fmt.Errorf("the control carries %d channels, and the value holds %d", channels, len(raw)/width)
	}
	for channel := range channels {
		if width == 8 {
			binary.NativeEndian.PutUint64(raw[channel*8:], number)
		} else {
			binary.NativeEndian.PutUint32(raw[channel*4:], uint32(number))
		}
	}
	return nil
}

// nullTerminated reads a string out of a fixed-length field.
func nullTerminated(field []byte) string {
	for at, character := range field {
		if character == 0 {
			return string(field[:at])
		}
	}
	return string(field)
}
