package main

// The send budget: how many times one declared field is sent in one
// spec generation.

import (
	"testing"

	"github.com/liken-sh/equipment-operator/wiim"
)

// Each pass spends one send of every field it sends. A field that has
// had its three sends is held back and named as not confirmed, the
// fields beside it still go, and a new generation starts every count
// again.
func TestTheSendBudgetHoldsBackAFieldAfterThreeSends(t *testing.T) {
	budget := newSendBudget()
	led, buttons := true, false
	both := wiim.Settings{Device: wiim.DeviceSettings{LED: &led, Buttons: &buttons}}
	onlyButtons := wiim.Settings{Device: wiim.DeviceSettings{Buttons: &buttons}}

	for range sendLimit {
		mustMatch(t, declared(spend(budget, 4, "spec.wiim.settings", wiim.Settings{Device: wiim.DeviceSettings{LED: &led}})),
			declared(wiim.Settings{Device: wiim.DeviceSettings{LED: &led}}))
	}
	mustMatch(t, declared(spend(budget, 4, "spec.wiim.settings", both)), declared(onlyButtons))
	mustDeepEqual(t, budget.unconfirmed(), []string{"spec.wiim.settings.device.led"})

	// The receiver reports the value at last, so the pass has nothing to
	// send for it, and the status stops naming it.
	spend(budget, 4, "spec.wiim.settings", wiim.Settings{})
	mustDeepEqual(t, budget.unconfirmed(), []string(nil))

	mustMatch(t, declared(spend(budget, 5, "spec.wiim.settings", both)), declared(both))
}
