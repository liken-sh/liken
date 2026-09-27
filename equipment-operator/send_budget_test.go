package main

// The send budget: how many times one declared field is sent at one
// declared value.

import (
	"testing"

	"github.com/liken-sh/equipment-operator/wiim"
)

// Each pass spends one send of every field it sends. A field that has
// had its three sends is held back and named as not confirmed, and the
// fields beside it still go.
func TestTheSendBudgetHoldsBackAFieldAfterThreeSends(t *testing.T) {
	budget := newSendBudget()
	led, buttons := true, false
	onlyLED := wiim.Settings{Device: wiim.DeviceSettings{LED: &led}}
	both := wiim.Settings{Device: wiim.DeviceSettings{LED: &led, Buttons: &buttons}}
	onlyButtons := wiim.Settings{Device: wiim.DeviceSettings{Buttons: &buttons}}

	for range sendLimit {
		mustMatch(t, declared(spend(budget, "spec.wiim.settings", onlyLED)), declared(onlyLED))
	}
	mustMatch(t, declared(spend(budget, "spec.wiim.settings", both)), declared(onlyButtons))
	mustDeepEqual(t, budget.unconfirmed(), []string{"spec.wiim.settings.device.led"})
}

// A count starts again only when its own field changes: the receiver
// reports the declared value, so the field is no longer pending, or a
// person declares another value. A pass that sends the same pending
// field again keeps the count.
func TestTheSendBudgetStartsACountAgainWhenItsFieldChanges(t *testing.T) {
	on, off := true, false
	ledOn := wiim.Settings{Device: wiim.DeviceSettings{LED: &on}}
	ledOff := wiim.Settings{Device: wiim.DeviceSettings{LED: &off}}
	cases := []struct {
		name    string
		between func(*sendBudget)
		next    wiim.Settings
		want    wiim.Settings
	}{
		{"the same value again", func(*sendBudget) {}, ledOn, wiim.Settings{}},
		{"the receiver reported the value", func(budget *sendBudget) { spend(budget, "spec.wiim.settings", wiim.Settings{}) }, ledOn, ledOn},
		{"another declared value", func(*sendBudget) {}, ledOff, ledOff},
		{"another family's pass", func(budget *sendBudget) { spend(budget, "spec.zones.zone2", ZoneSpec{}) }, ledOn, wiim.Settings{}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			budget := newSendBudget()
			for range sendLimit {
				spend(budget, "spec.wiim.settings", ledOn)
			}
			one.between(budget)

			mustMatch(t, declared(spend(budget, "spec.wiim.settings", one.next)), declared(one.want))
		})
	}
}
