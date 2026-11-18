package main

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The dust cap's pod is deleted while the release parks the mount, and
// stays terminating for 10 seconds after the park ends. The release
// must still delete the cap's ResourceClaimTemplate. The operator
// treats a 409 on creating a template as success, so a template left
// behind would give the next reservation's pod the old claim even if
// the dust cap's spec.claim had changed.
func TestAReleaseDeletesTheClaimOfAPodThatIsTerminating(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.claimDustCap()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.indi.hold("Telescope Simulator", "TELESCOPE_PARK")
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(time.Minute, "the park does not run", func() bool {
			r, _ := w.reservation("east-tonight")
			return strings.Contains(stepText(stepOf(r, observatory.StepDeactivation)), "Mount east state: Parked Running")
		})

		w.holdPod("east-dustcap")
		w.api.deleteNamed(podsCollection, "east-dustcap")
		w.indi.release("Telescope Simulator", "TELESCOPE_PARK")
		parked(w, "Telescope Simulator")
		time.Sleep(10 * time.Second)
		w.releasePod("east-dustcap")
		w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)

		if claims := w.api.names(templatesCollection); len(claims) != 0 {
			t.Errorf("after the release, the claim templates are %v, want none", claims)
		}
	})
}
