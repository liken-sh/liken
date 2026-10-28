package main

// A Receiver's status.cec: the receiver as the audio system of a
// CECBus. The network block owns the receiver's power, input, volume,
// and mute, so status.cec carries none of them. It reports what only
// the bus states, the System Audio Mode, with the receiver's CEC
// identity, so a person can see whether the TV sends its remote's
// volume keys to the receiver (HDMI-CEC 1.3a, CEC 13.15.2).
//
// The Deployment writes it at the end of each Television pass, because
// the pass already decides which Receiver a Display's picture passes
// through: the via of the Display in the Television's status.displays.
// A Receiver is that via only when the bus has an audio system above
// the Display in the HDMI tree, so the bus's device at logical address
// 5 is that Receiver.

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"

	"github.com/liken-sh/equipment-operator/cec"
)

// ReceiverCECStatus is the bus's audio system, from the device at
// logical address 5 in the bus's merged status.devices. A fact the
// device has not stated is absent.
type ReceiverCECStatus struct {
	Bus             string `json:"bus"`
	LogicalAddress  int    `json:"logicalAddress"`
	PhysicalAddress string `json:"physicalAddress,omitempty"`
	OSDName         string `json:"osdName,omitempty"`
	Vendor          string `json:"vendor,omitempty"`
	CECVersion      string `json:"cecVersion,omitempty"`
	// SystemAudioMode is On or Off, and absent while no adapter holds a
	// value. SystemAudioModeAt is when an adapter last read or heard it.
	SystemAudioMode   string `json:"systemAudioMode,omitempty"`
	SystemAudioModeAt string `json:"systemAudioModeAt,omitempty"`
}

// receiverCECFieldManager is the Deployment's field manager for a
// Receiver's status.cec. The operator's status apply runs under
// fieldManager and removes each field its manager owns and does not
// state, so status.cec has a manager of its own.
const receiverCECFieldManager = "equipment-operator-receiver-cec"

// ApplyReceiverCEC writes a Receiver's status.cec. A nil status
// removes the field, because the apply then states no field its manager
// owns.
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
// bus of that Display's Television. A Receiver on the path of two buses
// takes the first bus by name, so the choice is stable.
func receiverBuses(televisions []Television, derived map[string]televisionDerived) map[string]string {
	buses := map[string]string{}
	for index := range televisions {
		television := &televisions[index]
		for _, display := range derived[television.Metadata.Name].displays {
			if display.Via == nil || display.Via.Kind != "Receiver" {
				continue
			}
			held, found := buses[display.Via.Name]
			if !found || television.bus() < held {
				buses[display.Via.Name] = television.bus()
			}
		}
	}
	return buses
}

// receiverCECOf answers a Receiver's status.cec from its bus's audio
// system, and nil when the Receiver is no via or the bus holds no
// device at logical address 5.
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

// passReceiverCEC writes each Receiver's status.cec when it differs
// from the stored one, and posts a Normal Event for each change of the
// System Audio Mode to a value. A mode that becomes absent posts
// nothing, because no adapter stated a new one.
func (c *cecBusController) passReceiverCEC(receivers []Receiver, buses map[string]string, byName map[string]*CECBus) {
	for index := range receivers {
		receiver := &receivers[index]
		next := receiverCECOf(receiver.Metadata.Name, buses, byName)
		held := receiver.Status.CEC
		if reflect.DeepEqual(held, next) {
			continue
		}
		if err := ApplyReceiverCEC(c.client, receiver.Metadata.Name, next); err != nil {
			fmt.Fprintf(os.Stderr, "writing the status.cec of Receiver %s: %v\n", receiver.Metadata.Name, err)
			continue
		}
		before := ""
		if held != nil {
			before = held.SystemAudioMode
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

// modeMeaning says what the TV does in each mode (HDMI-CEC 1.3a, CEC
// 13.15.2), for the Event of a change.
var modeMeaning = map[string]string{
	"On":  ", so the TV mutes its own speakers and sends its remote's volume keys to the receiver",
	"Off": ", so the TV plays through its own speakers and keeps its remote's volume keys",
}
