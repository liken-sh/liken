package main

// A reservation that has finished every step saves the phase Released,
// and its runner releases its telescope after that write. Until then the
// telescope is still held, and the status reads it as Deactivating: its
// reservation is about to let it go, and no longer uses the observatory.

import (
	"testing"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// releasedTree returns a tree with the Observatory lab, its Telescope
// east, and the Reservation east-tonight in the given phase, which the
// returned operator's claims name as the holder of east.
func releasedTree(phase observatory.ReservationPhase) (*operator, *tree) {
	o := newOperator(testNamespace, nil, nil)
	reservation := &observatory.Reservation{
		Metadata: observatory.ObjectMeta{Name: "east-tonight", UID: "uid-east-tonight"},
		Spec:     observatory.ReservationSpec{Telescope: "east"},
		Status:   observatory.ReservationStatus{Phase: phase},
	}
	o.claims.holders["east"] = holder{uid: "uid-east-tonight", name: "east-tonight"}
	t := &tree{
		namespace:     testNamespace,
		observatories: map[string]*observatory.Observatory{"lab": {Metadata: observatory.ObjectMeta{Name: "lab"}}},
		telescopes:    map[string]*observatory.Telescope{"east": {Metadata: observatory.ObjectMeta{Name: "east"}, Spec: observatory.TelescopeSpec{Observatory: "lab"}}},
		reservations:  map[string]*observatory.Reservation{"east-tonight": reservation},
	}
	return o, t
}

func TestAReleasedReservationsTelescopeReadsDeactivating(t *testing.T) {
	o, tr := releasedTree(observatory.ReservationReleased)
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"the Telescope's phase", o.telescopeStatus(tr, tr.telescopes["east"], nil, nil).Phase, observatory.PhaseDeactivating},
		{"the Observatory's phase", o.observatoryStatus(tr, tr.observatories["lab"], nil).Phase, observatory.PhaseDeactivating},
		{"the Guider's phase", o.guiderStatus(tr, &observatory.Guider{Metadata: observatory.ObjectMeta{Name: "east"}, Spec: observatory.GuiderSpec{Telescope: "east"}}).Phase, observatory.PhaseDeactivating},
		{"the use of the Telescope's server", o.useOfServer(tr, serverRef{observatory.TelescopeKind, "east"}), releasing},
		{"the use of the Observatory's server", o.useOfServer(tr, serverRef{observatory.ObservatoryKind, "lab"}), releasing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%s is %v while Reservation east-tonight is Released and still holds Telescope east, want %v", c.name, c.got, c.want)
			}
		})
	}
}

// A Released reservation that still holds its telescope does not keep
// the observatory, so the StopSite step of another reservation of the
// observatory runs.
func TestAReleasedReservationDoesNotKeepTheSite(t *testing.T) {
	o, tr := releasedTree(observatory.ReservationReleased)
	tr.reservations["east-tonight"].Status.Steps = []observatory.Step{{Name: observatory.StepStopSite, State: observatory.StepSkipped}}
	tr.telescopes["west"] = &observatory.Telescope{Metadata: observatory.ObjectMeta{Name: "west"}, Spec: observatory.TelescopeSpec{Observatory: "lab"}}
	west := &observatory.Reservation{
		Metadata: observatory.ObjectMeta{Name: "west-tonight", UID: "uid-west-tonight"},
		Spec:     observatory.ReservationSpec{Telescope: "west"},
		Status:   observatory.ReservationStatus{Phase: observatory.ReservationDeactivating},
	}
	tr.reservations["west-tonight"] = west

	if got := o.siteStopFor(tr, west); got.action != siteRun {
		t.Errorf("siteStopFor for west-tonight = %+v while Reservation east-tonight is Released and still holds Telescope east, want siteRun", got)
	}
}
