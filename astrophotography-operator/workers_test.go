package main

import (
	"strings"
	"testing"
	"testing/synctest"
)

// reservationObject builds a Reservation held by this operator, in one
// phase, with the outbox its session recorded, if any.
func reservationObject(name, phase, outbox string) map[string]any {
	o := object("observatory", name, nil, map[string]any{"telescope": "east", "holder": managedBy})
	status := map[string]any{"phase": phase}
	if outbox != "" {
		status["session"] = map[string]any{"outbox": map[string]any{"claimName": outbox}}
	}
	o["status"] = status
	return o
}

// A reservation that is Ready with an outbox gets a worker that mounts
// the outbox at /outbox, as the drivers' group, owned by the
// Reservation.
func TestAReservationGetsItsWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(reservationsPath, reservationObject("tonight", "Ready", "frames"))
		settle()
		d, held := objectOf[deployment](t, w.api, deploymentsPath, "observatory", "tonight-astrophotography")
		if !held {
			t.Fatal("no Deployment tonight-astrophotography")
		}
		pod := d.Spec.Template.Spec
		cases := []struct {
			name      string
			got, want any
		}{
			{"args", strings.Join(pod.Containers[0].Args, " "), "worker"},
			{"outbox", pod.Volumes[0].PersistentVolumeClaim.ClaimName, "frames"},
			{"mount", pod.Containers[0].VolumeMounts[0].MountPath, "/outbox"},
			{"group", pod.SecurityContext.FSGroup, int64(1000)},
			{"owner", d.Metadata.OwnerReferences[0].Kind + "/" + d.Metadata.OwnerReferences[0].Name, "Reservation/tonight"},
		}
		for _, c := range cases {
			if c.got != c.want {
				t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
			}
		}
		if _, held := objectOf[service](t, w.api, servicesPath, "observatory", "tonight-astrophotography"); !held {
			t.Error("no Service tonight-astrophotography")
		}
	})
}

// A worker runs only while its reservation is Activating or Ready with
// an outbox, and goes when the reservation ends.
func TestAWorkerRunsWhileItsReservationIsActive(t *testing.T) {
	cases := []struct {
		name, phase, outbox string
		runs                bool
	}{
		{"activating", "Activating", "frames", true},
		{"ready", "Ready", "frames", true},
		{"ready with no outbox", "Ready", "", false},
		{"scheduled", "Scheduled", "frames", false},
		{"deactivating", "Deactivating", "frames", false},
		{"released", "Released", "frames", false},
		{"failed", "Failed", "frames", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorkloads(t, nil)
				w.api.put(reservationsPath, reservationObject("tonight", "Ready", "frames"))
				settle()
				w.api.put(reservationsPath, reservationObject("tonight", c.phase, c.outbox))
				settle()
				_, deployed := objectOf[deployment](t, w.api, deploymentsPath, "observatory", "tonight-astrophotography")
				_, served := objectOf[service](t, w.api, servicesPath, "observatory", "tonight-astrophotography")
				if deployed != c.runs || served != c.runs {
					t.Errorf("Deployment %v and Service %v, want %v", deployed, served, c.runs)
				}
			})
		})
	}
}

// A worker whose reservation is gone, or no longer held by this
// operator, goes too.
func TestAWorkerGoesWithItsReservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(reservationsPath, reservationObject("tonight", "Ready", "frames"))
		settle()
		w.api.remove(reservationsPath, "observatory", "tonight")
		settle()
		if _, held := objectOf[deployment](t, w.api, deploymentsPath, "observatory", "tonight-astrophotography"); held {
			t.Error("the worker stayed")
		}
	})
}

// A worker pod that cannot start posts one Warning on the reservation
// with the scheduler's message, and another only when the message
// changes.
func TestAWorkerThatCannotStartWarnsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(reservationsPath, reservationObject("tonight", "Ready", "frames"))
		labels := workloadLabels(roleWorker, "tonight")
		waiting := "0/1 nodes are available: 1 node has pod using PersistentVolumeClaim with the same name and ReadWriteOncePod access mode."
		w.api.put(podsPath, podFixture("tonight-astrophotography-0", "worker-0", labels, false, waiting))
		settle()
		w.api.put(targetsPath, object("observatory", "m42", nil, map[string]any{}))
		settle()
		w.api.put(podsPath, podFixture("tonight-astrophotography-0", "worker-0", labels, false, "0/1 nodes are available: 1 Insufficient cpu."))
		settle()
		w.api.put(podsPath, podFixture("tonight-astrophotography-0", "worker-0", labels, true, ""))
		settle()
		var messages []string
		for _, e := range w.api.recorded.About("Reservation", "tonight") {
			messages = append(messages, e.Type+" "+e.Reason+": "+e.Message)
		}
		want := []string{
			"Warning AstrophotographyWorkerPending: the worker's pod tonight-astrophotography-0 cannot start: " + waiting,
			"Warning AstrophotographyWorkerPending: the worker's pod tonight-astrophotography-0 cannot start: 0/1 nodes are available: 1 Insufficient cpu.",
		}
		if strings.Join(messages, "\n") != strings.Join(want, "\n") {
			t.Errorf("Events\n%s\nwant\n%s", strings.Join(messages, "\n"), strings.Join(want, "\n"))
		}
	})
}

// A Service's name is a DNS label, so a reservation whose name is too
// long for <name>-astrophotography gets no worker, and the log says so.
func TestAReservationWithALongNameGetsNoWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		name := strings.Repeat("a", 48)
		w.api.put(reservationsPath, reservationObject(name, "Ready", "frames"))
		settle()
		if writes := w.api.writes(deploymentsPath); len(writes) != 0 || !strings.Contains(w.logs.String(), "too long") {
			t.Errorf("writes %q and log %q", writes, w.logs.String())
		}
	})
}

// A lead with no worker image starts no worker, and its pass fails, so
// the log says why.
func TestAWorkerWithNoImageIsNotStarted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, func(o *operator) { o.workerImage = "" })
		w.api.put(reservationsPath, reservationObject("tonight", "Ready", "frames"))
		settle()
		if writes := w.api.writes(deploymentsPath); len(writes) != 0 || !strings.Contains(w.logs.String(), errNoWorkerImage.Error()) {
			t.Errorf("writes %q and log %q", writes, w.logs.String())
		}
	})
}
