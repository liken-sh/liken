package main

// A Receiver's status.cec describes the receiver as the audio system
// of a CECBus. It has only what the bus reports: the receiver's CEC
// identity and the System Audio Mode, which shows whether the TV sends
// its remote's volume keys to the receiver (HDMI-CEC 1.3a, CEC
// 13.15.2). Power, input, volume, and mute come from the receiver's
// network connection instead.
//
// The operator's Deployment writes status.cec at the end of each
// Television pass, which already finds the via of each Display in the
// Television's status.displays. A Receiver is a via only when it is
// connected between the Display and the TV, so the bus's audio system,
// the device at logical address 5, is that Receiver.

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// ReceiverCECStatus is the device at logical address 5 in the bus's
// status.devices. A field is omitted when the device has not reported
// it.
type ReceiverCECStatus struct {
	Bus             string `json:"bus"`
	LogicalAddress  int    `json:"logicalAddress"`
	PhysicalAddress string `json:"physicalAddress,omitempty"`
	OSDName         string `json:"osdName,omitempty"`
	Vendor          string `json:"vendor,omitempty"`
	CECVersion      string `json:"cecVersion,omitempty"`
	// SystemAudioMode is On or Off, and is omitted until an adapter has
	// read a value. SystemAudioModeAt is when an adapter last read it.
	SystemAudioMode   string `json:"systemAudioMode,omitempty"`
	SystemAudioModeAt string `json:"systemAudioModeAt,omitempty"`
}

// A server-side apply removes every field that its field manager owns
// and leaves out of the request. So status.cec has its own manager, and
// the operator's other status applies do not remove it.
const receiverCECFieldManager = "equipment-operator-receiver-cec"

// ApplyReceiverCEC writes a Receiver's status.cec. A nil status
// removes the field.
func ApplyReceiverCEC(c *Client, name string, status *ReceiverCECStatus) error {
	type stored struct {
		CEC *ReceiverCECStatus `json:"cec,omitempty"`
	}
	body, err := json.Marshal(struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     stored     `json:"status"`
	}{equipmentAPIVersion, "Receiver", ObjectMeta{Name: name}, stored{status}})
	if err != nil {
		return err
	}
	_, err = applyReceiver(c, name, receiverPath(name)+"/status?fieldManager="+receiverCECFieldManager+"&force=true", body)
	return err
}

// receiverBuses maps each Receiver that is the via of a Display to the
// bus of that Display's Television. When a Receiver is a via on more
// than one bus, it gets the bus whose name sorts first, so the result
// does not change between passes.
func receiverBuses(televisions []Television, derived map[string]televisionDerived) map[string]string {
	buses := map[string]string{}
	for index := range televisions {
		television := &televisions[index]
		for _, display := range derived[television.Metadata.Name].displays {
			if display.Via == nil || display.Via.Kind != "Receiver" {
				continue
			}
			current, found := buses[display.Via.Name]
			if !found || television.bus() < current {
				buses[display.Via.Name] = television.bus()
			}
		}
	}
	return buses
}

// receiverCECOf returns nil when the Receiver is not the via of any
// Display, or when its bus has no device at logical address 5.
func receiverCECOf(name string, buses map[string]string, byName map[string]*CECBus) *ReceiverCECStatus {
	bus := byName[buses[name]]
	if bus == nil {
		return nil
	}
	index := slices.IndexFunc(bus.Status.Devices, func(device CECDevice) bool {
		return device.LogicalAddress == int(cec.AddressAudioSystem)
	})
	if index < 0 {
		return nil
	}
	device := bus.Status.Devices[index]
	return &ReceiverCECStatus{
		Bus:               bus.Metadata.Name,
		LogicalAddress:    device.LogicalAddress,
		PhysicalAddress:   device.PhysicalAddress,
		OSDName:           device.OSDName,
		Vendor:            device.Vendor,
		CECVersion:        device.CECVersion,
		SystemAudioMode:   device.SystemAudioMode,
		SystemAudioModeAt: device.SystemAudioModeAt,
	}
}

// passReceiverCEC writes each Receiver's status.cec when it changed, and
// posts a Normal Event when the System Audio Mode changes to On or Off.
// A mode that becomes absent posts nothing, because no adapter read a
// new value.
func (c *cecBusController) passReceiverCEC(receivers []Receiver, buses map[string]string, byName map[string]*CECBus) {
	for index := range receivers {
		receiver := &receivers[index]
		next := receiverCECOf(receiver.Metadata.Name, buses, byName)
		stored := receiver.Status.CEC
		if reflect.DeepEqual(stored, next) {
			continue
		}
		if err := ApplyReceiverCEC(c.client, receiver.Metadata.Name, next); err != nil {
			fmt.Fprintf(os.Stderr, "writing the status.cec of Receiver %s: %v\n", receiver.Metadata.Name, err)
			c.retryLater()
			continue
		}
		before := ""
		if stored != nil {
			before = stored.SystemAudioMode
		}
		if next == nil || next.SystemAudioMode == "" || next.SystemAudioMode == before {
			continue
		}
		reason := reasonSystemAudioModeOff
		if next.SystemAudioMode == "On" {
			reason = reasonSystemAudioModeOn
		}
		message := fmt.Sprintf("the audio system on CECBus %s reports System Audio Mode %s", next.Bus, next.SystemAudioMode)
		if before != "" {
			message += fmt.Sprintf(", which was %s", before)
		}
		message += modeMeaning[next.SystemAudioMode]
		c.recorder.Normal(reference("Receiver", receiver.Metadata), reason, message)
	}
}

// modeMeaning ends the Event message with what the TV does in each
// System Audio Mode (HDMI-CEC 1.3a, CEC 13.15.2).
var modeMeaning = map[string]string{
	"On":  ", so the TV mutes its own speakers and sends its remote's volume keys to the receiver",
	"Off": ", so the TV plays through its own speakers and keeps its remote's volume keys",
}
