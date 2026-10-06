package main

// The Events of the conditions a node workload writes on a Television.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/events"
)

// A TV that ignores every power command makes the application end
// Unconfirmed, a Warning.
func TestAPowerTheTVDidNotConfirmPostsAWarning(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := controlling(t, roomWithTV(stubbornTV()), lounge(TelevisionOn))

		appliedAt(t, api, "lounge", 1)

		mustDeepEqual(t, postedAbout(api.events, "Television", "lounge"), []posted{{events.TypeWarning, reasonUnconfirmed, 1}})
	})
}

// A TV that takes the command makes the application end Confirmed, a
// Normal Event.
func TestAPowerTheTVConfirmedPostsANormalEvent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := controlling(t, roomWithTV(televisionTV(cec.PowerStandby)), lounge(TelevisionOn))

		appliedAt(t, api, "lounge", 1)

		mustDeepEqual(t, postedAbout(api.events, "Television", "lounge"), []posted{{events.TypeNormal, reasonConfirmed, 1}})
	})
}
