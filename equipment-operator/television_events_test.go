package main

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/events"
)

// When the TV ignores every power command, PowerApplied ends with the
// reason Unconfirmed and the operator posts a Warning Event.
func TestAPowerTheTVDidNotConfirmPostsAWarning(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := controlling(t, busWithTV(stubbornTV()), lounge(TelevisionOn))

		appliedAt(t, api, "lounge", 1)

		mustDeepEqual(t, postedAbout(api.events, "Television", "lounge"), []posted{{events.TypeWarning, reasonUnconfirmed, 1}})
	})
}

// When the TV obeys the power command, PowerApplied ends with the reason
// Confirmed and the operator posts a Normal Event.
func TestAPowerTheTVConfirmedPostsANormalEvent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := controlling(t, busWithTV(televisionTV(cec.PowerStandby)), lounge(TelevisionOn))

		appliedAt(t, api, "lounge", 1)

		mustDeepEqual(t, postedAbout(api.events, "Television", "lounge"), []posted{{events.TypeNormal, reasonConfirmed, 1}})
	})
}
