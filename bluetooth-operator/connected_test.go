package main

// These tests cover the Connected condition: the reason each link end
// gives, the rule that keeps a reason while the link stays down, and
// the wire format the CRD declares.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// held is the Connected condition a Peripheral holds before a pass.
func held(status conditions.Status, reason string) []Condition {
	return []Condition{{Type: conditionConnected, Status: status, Reason: reason, LastTransitionTime: testNow}}
}

// The reason says why a device is not connected, from the reason BlueZ
// gave when the link ended. A pass with no reason from a signal keeps
// the reason the Peripheral holds, so the reason changes only when the
// link changes.
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
		name    string
		present bool
		device  func(*deviceState)
		held    []Condition
		ended   string
		status  conditions.Status
		reason  string
	}{
		{"a connected controller", true, connected, nil, "", conditionTrue, reasonLinkUp},
		{"a connected controller with a reason held from a link that ended", true, connected, held(conditionFalse, reasonAsleep), blueZReasonTimeout, conditionTrue, reasonLinkUp},
		{"a Low Energy remote whose link timed out", true, remote, held(conditionTrue, reasonLinkUp), blueZReasonTimeout, conditionFalse, reasonAsleep},
		{"a HID-over-GATT device on a public address whose link timed out", true, gattOnPublic, held(conditionTrue, reasonLinkUp), blueZReasonTimeout, conditionFalse, reasonAsleep},
		{"a classic controller whose link timed out", true, classic, held(conditionTrue, reasonLinkUp), blueZReasonTimeout, conditionFalse, reasonLinkLost},
		{"a remote that ended the link", true, remote, held(conditionTrue, reasonLinkUp), blueZReasonRemote, conditionFalse, reasonClosedByDevice},
		{"a link this radio ended", true, remote, held(conditionTrue, reasonLinkUp), blueZReasonLocal, conditionFalse, reasonClosedByRadio},
		{"a link whose keys did not match", true, remote, held(conditionTrue, reasonLinkUp), blueZReasonAuthentication, conditionFalse, reasonAuthenticationFailed},
		{"a link BlueZ ended for no known reason", true, remote, held(conditionTrue, reasonLinkUp), "org.bluez.Reason.Unknown", conditionFalse, reasonNotConnected},
		{"a link that ended at a suspend", true, remote, held(conditionTrue, reasonLinkUp), "org.bluez.Reason.Suspend", conditionFalse, reasonNotConnected},
		{"a disconnect with no signal", true, remote, held(conditionTrue, reasonLinkUp), "", conditionFalse, reasonNotConnected},
		{"a first report of a device that is not connected", true, remote, nil, "", conditionFalse, reasonNotConnected},
		{"a remote that stays asleep", true, remote, held(conditionFalse, reasonAsleep), "", conditionFalse, reasonAsleep},
		{"a remote that stays off after the radio closed its link", true, remote, held(conditionFalse, reasonClosedByRadio), "", conditionFalse, reasonClosedByRadio},
		{"a device that returns to bluetoothd with no link", true, remote, held(conditionFalse, reasonNotBonded), "", conditionFalse, reasonNotConnected},
		{"a bond bluetoothd holds no object for", false, connected, held(conditionFalse, reasonAsleep), blueZReasonTimeout, conditionFalse, reasonNotBonded},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			device := pairedDevice(t, testDevice)
			test.device(&device)

			condition := connectedCondition(test.held, device, test.present, test.ended, testNow)

			if condition.Type != conditionConnected || condition.Status != test.status || condition.Reason != test.reason {
				t.Errorf("condition = %+v, want %s %s/%s", condition, conditionConnected, test.status, test.reason)
			}
			if condition.Message != connectedMessages[test.reason] || condition.Message == "" {
				t.Errorf("message = %q, want the message of %s", condition.Message, test.reason)
			}
		})
	}
}

// lastTransitionTime marks when the link state last changed, so a
// reader can measure how long a controller has been asleep. A reason
// that changes under the same status does not move it.
func TestConnectedConditionKeepsTheTimeOfTheLastChange(t *testing.T) {
	device := pairedDevice(t, testDevice)
	device.Connected, device.AddressType = false, "random"
	asleep := connectedCondition(held(conditionTrue, reasonLinkUp), device, true, blueZReasonTimeout, testNow)

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

// The Connected condition keeps the wire format the CRD declares: a
// string status, a date-time to the second, and a message. The time is
// cut to the second, so a status composed again equals the one the API
// server returns.
func TestConnectedConditionKeepsItsWireFormat(t *testing.T) {
	device := pairedDevice(t, testDevice)
	device.Connected, device.AddressType = false, "random"

	condition := connectedCondition(nil, device, true, blueZReasonTimeout, testNow.Add(400*time.Millisecond))

	written, err := json.Marshal(condition)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"Connected","status":"False","reason":"Asleep","message":"` + connectedMessages[reasonAsleep] + `","lastTransitionTime":"2026-08-17T17:30:00Z"}`
	if string(written) != want {
		t.Errorf("condition = %s, want %s", written, want)
	}
	var read Condition
	if err := json.Unmarshal(written, &read); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read, condition) {
		t.Errorf("read back %+v, want %+v", read, condition)
	}
}
