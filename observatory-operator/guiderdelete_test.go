package main

// A Guider has no finalizer, so a person who deletes it with its
// reservation, as `kubectl delete -f` of the whole inventory does,
// removes it at once while its PHD2 still runs on the telescope's
// server.

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2/phd2test"
)

// The StopWork step stops a PHD2 whose Guider is gone, because its pod
// still runs on the server and a guide pulse after the mount's stop
// would move the mount.
func TestAbortStopsThePHD2OfADeletedGuider(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyGuider(t)
		phd2 := w.guiders.phd2("east-guider")
		phd2.Set(func(s *phd2test.Server) { s.AppState = "Guiding" })
		phd2.Broadcast(phd2test.Event("StartGuiding", nil))
		w.until(time.Minute, "the Guider is not Guiding", func() bool { return guider(w).Status.State == observatory.GuiderGuiding })
		w.api.deleteNamed(kindCollection(observatory.GuiderKind), "east")
		synctest.Wait()
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)
		if !strings.Contains(phd2.Received(), "stop_capture") {
			t.Errorf("PHD2 received %q, want stop_capture", phd2.Received())
		}
	})
}
