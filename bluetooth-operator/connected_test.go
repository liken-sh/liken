package main

// Tests of the Connected condition of a Peripheral.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// connectedWith returns a Peripheral's conditions with one Connected
// condition.
func connectedWith(status conditions.Status, reason string) []Condition {
	return []Condition{{Type: conditionConnected, Status: status, Reason: reason, LastTransitionTime: testNow}}
}

func TestConnectedConditionReason(t *testing.T) {
	remote := func(d *deviceState) { d.Connected, d.AddressType = false, "random" }
	gattOnPublic := func(d *deviceState) {
		d.Connected, d.AddressType, d.UUIDs = false, "public", []string{fullUUID("1812")}
	}
	classic := func(d *deviceState) {
		d.Connected, d.AddressType, d.UUIDs = false, "public", []string{fullUUID("1124")}
	}
	connected := func(*deviceState) {}
	cases := []struct {
		name     string
		present  bool
		device   func(*deviceState)
		existing []Condition
		ended    string
		status   conditions.Status
		reason   string
	}{
		{"a connected controller", true, connected, nil, "", conditionTrue, reasonLinkUp},
		{"a connected controller whose condition still has the reason of an earlier link end", true, connected, connectedWith(conditionFalse, reasonAsleep), blueZReasonTimeout, conditionTrue, reasonLinkUp},
		{"a Low Energy remote whose link timed out", true, remote, connectedWith(conditionTrue, reasonLinkUp), blueZReasonTimeout, conditionFalse, reasonAsleep},
		{"a HID-over-GATT device on a public address whose link timed out", true, gattOnPublic, connectedWith(conditionTrue, reasonLinkUp), blueZReasonTimeout, conditionFalse, reasonAsleep},
		{"a classic controller whose link timed out", true, classic, connectedWith(conditionTrue, reasonLinkUp), blueZReasonTimeout, conditionFalse, reasonLinkLost},
		{"a remote that ended the link", true, remote, connectedWith(conditionTrue, reasonLinkUp), blueZReasonRemote, conditionFalse, reasonClosedByDevice},
		{"a link this radio ended", true, remote, connectedWith(conditionTrue, reasonLinkUp), blueZReasonLocal, conditionFalse, reasonClosedByRadio},
		{"a link whose keys did not match", true, remote, connectedWith(conditionTrue, reasonLinkUp), blueZReasonAuthentication, conditionFalse, reasonAuthenticationFailed},
		{"a link that BlueZ ended with the reason Unknown", true, remote, connectedWith(conditionTrue, reasonLinkUp), "org.bluez.Reason.Unknown", conditionFalse, reasonNotConnected},
		{"a link that ended at a suspend", true, remote, connectedWith(conditionTrue, reasonLinkUp), "org.bluez.Reason.Suspend", conditionFalse, reasonNotConnected},
		{"a disconnect with no signal", true, remote, connectedWith(conditionTrue, reasonLinkUp), "", conditionFalse, reasonNotConnected},
		{"the first condition of a device that is not connected", true, remote, nil, "", conditionFalse, reasonNotConnected},
		{"a remote that stays asleep", true, remote, connectedWith(conditionFalse, reasonAsleep), "", conditionFalse, reasonAsleep},
		{"a remote that stays off after the radio closed its link", true, remote, connectedWith(conditionFalse, reasonClosedByRadio), "", conditionFalse, reasonClosedByRadio},
		{"a device that returns to bluetoothd with no link", true, remote, connectedWith(conditionFalse, reasonNotBonded), "", conditionFalse, reasonNotConnected},
		{"a bond that bluetoothd has no device object for", false, connected, connectedWith(conditionFalse, reasonAsleep), blueZReasonTimeout, conditionFalse, reasonNotBonded},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			device := pairedDevice(t, testDevice)
			test.device(&device)

			condition := connectedCondition(test.existing, device, test.present, test.ended, testNow)

			if condition.Type != conditionConnected || condition.Status != test.status || condition.Reason != test.reason {
				t.Errorf("condition = %+v, want %s %s/%s", condition, conditionConnected, test.status, test.reason)
			}
			if condition.Message != connectedMessages[test.reason] || condition.Message == "" {
				t.Errorf("message = %q, want the message of %s", condition.Message, test.reason)
			}
		})
	}
}

// lastTransitionTime tells a reader how long a controller has been
// disconnected, so a new reason under the same status keeps it.
func TestConnectedConditionOnlyMovesLastTransitionTimeWhenStatusChanges(t *testing.T) {
	device := pairedDevice(t, testDevice)
	device.Connected, device.AddressType = false, "random"
	asleep := connectedCondition(connectedWith(conditionTrue, reasonLinkUp), device, true, blueZReasonTimeout, testNow)

	later := testNow.Add(time.Hour)
	same := connectedCondition([]Condition{asleep}, device, true, blueZReasonRemote, later)
	if !same.LastTransitionTime.Equal(asleep.LastTransitionTime) {
		t.Errorf("lastTransitionTime = %s, want the time of the last change %s",
			same.LastTransitionTime, asleep.LastTransitionTime)
	}
	if same.Reason != reasonClosedByDevice {
		t.Errorf("reason = %q, want the reason to follow the new signal", same.Reason)
	}

	device.Connected = true
	changed := connectedCondition([]Condition{asleep}, device, true, "", later)
	if !changed.LastTransitionTime.Equal(later) {
		t.Errorf("lastTransitionTime = %s, want the moment the link came up", changed.LastTransitionTime)
	}
}

// The condition serializes to the format the CRD declares, and reads
// back equal, because its time is truncated to the second as the API
// server stores it.
func TestConnectedConditionSerializesToTheCRDFormat(t *testing.T) {
	device := pairedDevice(t, testDevice)
	device.Connected, device.AddressType = false, "random"

	condition := connectedCondition(nil, device, true, blueZReasonTimeout, testNow.Add(400*time.Millisecond))

	written, err := json.Marshal(condition)
	if err != nil {
		t.Fatalf("marshaling the condition: %v", err)
	}
	want := `{"type":"Connected","status":"False","reason":"Asleep","message":"` + connectedMessages[reasonAsleep] + `","lastTransitionTime":"2026-08-17T17:30:00Z"}`
	if string(written) != want {
		t.Errorf("condition = %s, want %s", written, want)
	}
	var read Condition
	if err := json.Unmarshal(written, &read); err != nil {
		t.Fatalf("unmarshaling the condition: %v", err)
	}
	if !reflect.DeepEqual(read, condition) {
		t.Errorf("read back %+v, want %+v", read, condition)
	}
}
