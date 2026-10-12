package main

// A device's ResourceClaim goes with its pod, however the pod went.

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The dust cap's pod is evicted while the release parks the mount, and
// stays terminating for 10 seconds into StopDevices. The release still
// deletes the cap's claim, which would otherwise hold the hardware, and
// which the next reservation would find and reuse whatever its
// spec.claim says by then.
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

		if claims := w.api.names(claimsCollection); len(claims) != 0 {
			t.Errorf("claims after the release = %v", claims)
		}
	})
}
