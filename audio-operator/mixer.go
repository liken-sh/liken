package main

// Reads and writes the control elements that a sound card declares on
// /dev/snd/controlC<N>, with the ioctls of the control interface and
// without libasound. Examples are a USB DAC's PCM Playback Volume, a
// Realtek codec's Master Playback Switch, and an Auto-Mute Mode
// selector.
//
// PipeWire sets no volume or switch on the card, because its ALSA
// monitor is off and the adapter nodes that the operator declares carry
// no ACP device. So every volume that PipeWire applies is a software
// gain, and a hardware control keeps the value that ALSA gave it at boot
// until a spec changes it. The exception is IEC958 Playback Default,
// which alsa-lib's hdmi device writes for a sink that passes encoded
// formats (bitstream.go).
//
// A jack element is read through the same device. It is not a
// capability, because nothing can set it. Its value reports whether a
// plug is in, and the Connected condition reads it.

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"unsafe"
)

// The element types that status.capabilities reports, from
// include/uapi/sound/asound.h. The other types (bytes, IEC958 channel
// status, and 64-bit integer) have no representation in the API and are
// not capabilities.
const (
	ctlElemTypeBoolean    = 1
	ctlElemTypeInteger    = 2
	ctlElemTypeEnumerated = 3
)

// The interfaces an element belongs to. A jack is on the card interface
// and a control that a spec sets is on the mixer interface. The ELD
// element is on the PCM interface (alsa.go).
const (
	ctlElemIfaceCard  = 0
	ctlElemIfaceMixer = 2
)

// Access bits from include/uapi/sound/asound.h. The card marks an
// element inactive when a routing change disables it, and a write to an
// inactive element has no effect.
const (
	ctlAccessWrite    = 1 << 1
	ctlAccessTLVRead  = 1 << 4
	ctlAccessInactive = 1 << 8
)

// SNDRV_CTL_IOCTL_ELEM_WRITE. Its argument is the same ctlElemValue
// structure that the read in alsa.go uses.
var ctlIoctlElemWrite = iowr(0x13, ctlElemValueSize)

// The offsets of the fields inside the value union of ctlElemInfo, from
// include/uapi/sound/asound.h. An integer element has min, max, and
// step as three longs. An enumerated element has its item count, then
// the number of the item to look up, then that item's name.
const (
	infoIntegerMin      = 0
	infoIntegerMax      = 8
	infoIntegerStep     = 16
	infoEnumeratedItems = 0
	infoEnumeratedItem  = 4
	infoEnumeratedName  = 8
	enumeratedNameSize  = 64
)

// The values of the type field in status.capabilities, as the CRD
// enumerates them.
const (
	capabilityInteger    = "integer"
	capabilityBoolean    = "boolean"
	capabilityEnumerated = "enumerated"
)

// The values of a boolean control in spec.controls and
// status.observed.controls. amixer uses the same words.
const (
	controlOn  = "on"
	controlOff = "off"
)

// controlCapability is one control as status.capabilities reports it.
//
// Min, Max, and Step are pointers so that a minimum of zero is not
// confused with no minimum. A boolean declares no range and an integer
// always does.
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

// control is one control element, with the identity that the kernel
// gives it and its capability.
//
// Index is kept next to Name because a name is not unique on a card. An
// Intel HDMI codec declares one IEC958 Playback Switch for each HDMI
// output under the same name. attachControls gives each HDMI sink the
// element whose index matches its output, so a write through that
// element reaches the right output.
type control struct {
	NumID      uint32
	Interface  int32
	Device     uint32
	Index      uint32
	Name       string
	Capability controlCapability
}

type mixer struct {
	card     int
	device   *os.File
	controls []control
	jacks    []control
	byName   map[string]control
}

// openMixer opens the device read-write because writeElement writes
// through the same descriptor that enumerate reads. The kernel still
// refuses a write to an element without its write access flag.
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

// enumerate puts the elements that a spec can write in m.controls and
// the jacks in m.jacks. It skips an inactive element, because a write to
// it has no effect. It skips an element that accepts no write and is not
// a jack, and an element whose type has no representation in the API.
// It logs and skips an element that the card fails to describe, so one
// bad element does not stop the rest of the card from reading.
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
		capability, supported := capabilityFrom(info, items, m.decibels(info))
		if !supported {
			continue
		}
		element.Capability = capability
		m.controls = append(m.controls, element)
		if _, taken := m.byName[element.Name]; !taken {
			// A card with several HDMI outputs declares one IEC958
			// control for each output under the same name, and byName
			// keeps the first. A caller that needs a particular output
			// reads and writes through its element from m.controls.
			m.byName[element.Name] = element
		}
	}
	return nil
}

// isJackControl reports whether an element is on the card interface and
// has a name that ends in " Jack". enumerate also checks that the
// element is boolean. The kernel's jack layer creates one such element
// for each socket that the codec can sense.
func isJackControl(iface int32, name string) bool {
	return iface == ctlElemIfaceCard && strings.HasSuffix(name, " Jack")
}

// capabilityFrom returns false for an element whose type has no
// representation in the API.
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
			// A step of zero means every number in the range is valid,
			// so Step stays nil.
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

func integerRange(value []byte) (min, max, step int64) {
	return int64(binary.NativeEndian.Uint64(value[infoIntegerMin : infoIntegerMin+8])),
		int64(binary.NativeEndian.Uint64(value[infoIntegerMax : infoIntegerMax+8])),
		int64(binary.NativeEndian.Uint64(value[infoIntegerStep : infoIntegerStep+8]))
}

// enumeratedItems reads the name of every value that an enumerated
// control accepts. The info ioctl returns the name of one item, the one
// whose number the caller writes into the union, so this makes one ioctl
// for each item. It runs only when the mixer opens.
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

func (m *mixer) readControl(name string) (string, error) {
	element, declared := m.byName[name]
	if !declared {
		return "", fmt.Errorf("card %d declares no control named %q", m.card, name)
	}
	return m.readElement(element)
}

func (m *mixer) readElement(element control) (string, error) {
	value := ctlElemValue{ID: ctlElemID{NumID: element.NumID}}
	if err := ioctl(m.device, ctlIoctlElemRead, unsafe.Pointer(&value)); err != nil {
		return "", fmt.Errorf("reading control %q on card %d: %w", element.Name, m.card, err)
	}
	return controlValueFrom(element.Capability, value.Value[:])
}

func (m *mixer) writeControl(name, value string) error {
	element, declared := m.byName[name]
	if !declared {
		return fmt.Errorf("card %d declares no control named %q", m.card, name)
	}
	return m.writeElement(element, value)
}

// writeElement writes one value into every channel of an element,
// because spec.controls has one value for each control. A different
// level for each channel, as a balance control needs, is not supported.
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

// jackStates reads every jack of the card, keyed by the jack's name. A
// card that senses no jacks, such as a USB device, returns an empty map.
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

// controlValueFrom converts the raw value of an element to the string
// that spec.controls and status.observed.controls use.
//
// A control with several channels reports the value of the first
// channel, because the operator writes the same value to every channel.
// When someone sets the channels to different values by hand, the next
// write of the spec's value sets them all to it again.
func controlValueFrom(capability controlCapability, raw []byte) (string, error) {
	switch capability.Type {
	case capabilityInteger, capabilityBoolean:
		if len(raw) < 8 {
			return "", fmt.Errorf("the value has %d bytes, and a number needs 8", len(raw))
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
			return "", fmt.Errorf("the value has %d bytes, and an item needs 4", len(raw))
		}
		item := int(binary.NativeEndian.Uint32(raw[:4]))
		if item < 0 || item >= len(capability.Values) {
			return "", fmt.Errorf("the control reads value %d, and it declares %d", item, len(capability.Values))
		}
		return capability.Values[item], nil
	}
	return "", fmt.Errorf("a control of type %q has no string form in spec.controls", capability.Type)
}

func encodeControlValue(capability controlCapability, value string, raw []byte) error {
	channels := capability.Channels
	if channels < 1 {
		channels = 1
	}
	switch capability.Type {
	case capabilityInteger:
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("%q is not a number, and this control accepts only a number", value)
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
	return fmt.Errorf("a control of type %q accepts no value", capability.Type)
}

func rangeEnd(end *int64) string {
	if end == nil {
		return "none"
	}
	return strconv.FormatInt(*end, 10)
}

func fillChannels(raw []byte, width, channels int, number uint64) error {
	if len(raw) < width*channels {
		return fmt.Errorf("the control has %d channels, and the value contains %d", channels, len(raw)/width)
	}
	for channel := range channels {
		if width == 8 {
			binary.NativeEndian.PutUint64(raw[channel*8:], number)
		} else {
			if number > math.MaxUint32 {
				return fmt.Errorf("%d does not fit in a channel of %d bytes", number, width)
			}
			binary.NativeEndian.PutUint32(raw[channel*4:], uint32(number))
		}
	}
	return nil
}

func nullTerminated(field []byte) string {
	for at, character := range field {
		if character == 0 {
			return string(field[:at])
		}
	}
	return string(field)
}
