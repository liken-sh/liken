package main

// These tests read the Events the machine operator posts about its
// Machine from eventstest's fake of the events collection, in a
// synctest bubble, so the recorder's goroutine runs on the fake clock.

import (
	"io"
	"net/http"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
)

// recordingClient serves the events fake in front of handler, and
// answers a client and a recorder that writes through it.
func recordingClient(t *testing.T, handler http.Handler) (*apiclient.Client, *events.Recorder, *eventstest.Events) {
	t.Helper()
	recorded := &eventstest.Events{}
	client := testClient(t, recorded.Around(handler))
	recorder := events.New(t.Context(), client, component, events.Options{Instance: "node-1", Log: io.Discard})
	return client, recorder, recorded
}

// posted answers each Event about the Machine node-1 as
// "Type Reason: message", in the order the fake received them.
func posted(recorded *eventstest.Events) []string {
	synctest.Wait()
	var out []string
	for _, e := range recorded.About(machineKind, "node-1") {
		if e.Metadata.Namespace != "default" {
			out = append(out, "outside default: "+e.Reason)
		}
		out = append(out, e.Type+" "+e.Reason+": "+e.Message)
	}
	return out
}

var crashTime = testNow.Add(-time.Hour)

func TestAStatusWritePostsTheEventOfEachChange(t *testing.T) {
	converged := api.Condition{Type: "SpecConverged", Status: api.ConditionTrue, Reason: "Converged", Message: "this boot actuated the current spec"}
	ready := api.Condition{Type: "Ready", Status: api.ConditionTrue, Reason: "Reconciled"}
	cases := []struct {
		name    string
		stored  machine.MachineStatus
		written machine.MachineStatus
		want    []string
	}{
		{
			name:   "a spec the operator refuses to stage",
			stored: machine.MachineStatus{Conditions: []api.Condition{converged, ready}},
			written: machine.MachineStatus{Conditions: []api.Condition{
				{Type: "SpecConverged", Status: api.ConditionFalse, Reason: "StagingRejected", Message: "role data may only grow"},
				{Type: "Ready", Status: api.ConditionFalse, Reason: "Blocked", Message: "SpecConverged is False"},
			}},
			want: []string{
				"Warning StagingRejected: SpecConverged is False: role data may only grow",
				"Warning Blocked: Ready is False: SpecConverged is False",
			},
		},
		{
			name: "a release that fell back at boot",
			stored: machine.MachineStatus{Conditions: []api.Condition{
				{Type: "VersionConverged", Status: api.ConditionFalse, Reason: "RebootRequested"},
			}},
			written: machine.MachineStatus{Conditions: []api.Condition{
				{Type: "VersionConverged", Status: api.ConditionFalse, Reason: "RejectedLastBoot", Message: "the machine tried release 2026.10.1 on slot b and fell back"},
			}},
			want: []string{"Warning RejectedLastBoot: VersionConverged is False: the machine tried release 2026.10.1 on slot b and fell back"},
		},
		{
			name:   "a reboot requested to apply the spec",
			stored: machine.MachineStatus{Conditions: []api.Condition{converged}},
			written: machine.MachineStatus{Conditions: []api.Condition{
				{Type: "SpecConverged", Status: api.ConditionFalse, Reason: "RebootRequested", Message: "rebooting to apply spec 1a2b3c4d"},
			}},
			want: []string{"Normal RebootRequested: SpecConverged is False: rebooting to apply spec 1a2b3c4d"},
		},
		{
			name:    "a new condition with no message",
			stored:  machine.MachineStatus{},
			written: machine.MachineStatus{Conditions: []api.Condition{ready}},
			want:    []string{"Normal Reconciled: Ready is True"},
		},
		{
			name:   "a new message alone, and the conductor's grant",
			stored: machine.MachineStatus{Conditions: []api.Condition{converged}},
			written: machine.MachineStatus{Conditions: []api.Condition{
				{Type: "SpecConverged", Status: api.ConditionTrue, Reason: "Converged", Message: "another message"},
				{Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows"},
			}},
			want: nil,
		},
		{
			name:   "a new kernel crash",
			stored: machine.MachineStatus{},
			written: machine.MachineStatus{LastCrash: &machine.CrashStatus{
				Time: &crashTime, Reason: "Panic", Message: "Kernel panic - not syncing: VFS",
			}},
			want: []string{"Warning KernelCrashed: the kernel recorded a Panic at 2026-07-06T11:00:00Z: Kernel panic - not syncing: VFS; status.lastCrash names the records"},
		},
		{
			name:    "a crash the status already names",
			stored:  machine.MachineStatus{LastCrash: &machine.CrashStatus{Time: &crashTime, Reason: "Panic"}},
			written: machine.MachineStatus{LastCrash: &machine.CrashStatus{Time: &crashTime, Reason: "Panic"}},
			want:    nil,
		},
		{
			name:    "a new refused boot",
			stored:  machine.MachineStatus{LastFailStop: &machine.FailStop{Reason: "an older refusal", Time: crashTime.Add(-time.Hour)}},
			written: machine.MachineStatus{LastFailStop: &machine.FailStop{Reason: "no disk holds machineState", Time: crashTime}},
			want:    []string{"Warning BootRefused: init refused the boot at 2026-07-06T11:00:00Z and powered the machine off: no disk holds machineState"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				_, recorder, recorded := recordingClient(t, http.NotFoundHandler())
				m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1", UID: "uid-1"}}

				postStatusEvents(machineEvents{recorder, machineReference(m)}, &c.stored, &c.written)

				if got := posted(recorded); !slices.Equal(got, c.want) {
					t.Errorf("got %q, want %q", got, c.want)
				}
			})
		})
	}
}

// refusingOnce answers the first request of a method and path with a
// 500, and hands every other request to next.
func refusingOnce(next http.Handler, method, path string) http.Handler {
	refused := false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !refused && r.Method == method && r.URL.Path == path {
			refused = true
			http.Error(w, "etcd is unavailable", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// A pass posts its transitions only after its status write lands. A
// refused write posts nothing, and the next pass finds the same
// transitions and posts them. A settled pass posts nothing more.
func TestAPassPostsItsTransitionsAfterTheWriteLands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		fake := newPassAPI()
		client, recorder, recorded := recordingClient(t,
			refusingOnce(fake, http.MethodPut, kubernetes.MachinesPath+"/node-1/status"))
		r := &reader{client: client, recorder: recorder}
		o := metrics.NewOperator(component, machine.Version, []string{machineKind}, watchKinds)
		mm := newMachineMetrics(o, &fetcher{})
		heartbeat := kubernetes.NewHeartbeat("node-1")
		pass := func() error {
			current, err := r.machine("node-1")
			if err != nil {
				t.Fatal(err)
			}
			return reconcile(r, current, "lab", &fetcher{}, heartbeat, mm, nil)
		}

		if err := pass(); err == nil {
			t.Fatal("the first status write should be refused")
		}
		if got := posted(recorded); len(got) != 0 {
			t.Fatalf("a refused write posted %q", got)
		}

		if err := pass(); err != nil {
			t.Fatal(err)
		}
		written, err := kubernetes.GetMachine(client, "node-1")
		if err != nil {
			t.Fatal(err)
		}
		first := posted(recorded)
		if len(first) != len(written.Status.Conditions) {
			t.Errorf("the landed write posted %d Events for %d conditions: %q", len(first), len(written.Status.Conditions), first)
		}

		if err := pass(); err != nil {
			t.Fatal(err)
		}
		if got := posted(recorded); len(got) != len(first) {
			t.Errorf("a settled pass posted more: %q", got[len(first):])
		}
	})
}

// The drain's cordon is one Event when the operator cordons the Node,
// and none when the Node is already cordoned or the cordon fails.
func TestTheDrainPostsTheCordonItApplies(t *testing.T) {
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	cases := []struct {
		name string
		api  *drainAPI
		node *nodeObject
		want []string
	}{
		{"an uncordoned node", &drainAPI{pods: []kubernetes.Pod{pod("web", "default")}}, drainNode(false, false, ""),
			[]string{"Normal Cordoned: cordoned the Node node-4 ahead of the reboot; 1 pods to move"}},
		{"a node the drain cordoned already", &drainAPI{}, drainNode(true, true, since), nil},
		{"a cordon that fails", &drainAPI{patchFail: true}, drainNode(false, false, ""), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, recorder, recorded := recordingClient(t, c.api.handler())
				m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1"}}

				gateThroughDrain(&reader{client: client}, c.node, rebootingConvergence(), drainNow, machineEvents{recorder, machineReference(m)}, nil)

				if got := posted(recorded); !slices.Equal(got, c.want) {
					t.Errorf("got %q, want %q", got, c.want)
				}
			})
		})
	}
}

// A pass that finds the Node under the drain's own cordon, with no
// reboot ahead, uncordons it and posts one Event.
func TestAPassPostsTheUncordonAfterTheReboot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		node := readyNode()
		node["spec"] = map[string]any{"unschedulable": true}
		node["metadata"].(map[string]any)["annotations"] = map[string]any{cordonedAnnotation: "true"}
		fake := newPassAPIWithNode(node)
		client, recorder, recorded := recordingClient(t, fake)
		r := &reader{client: client, recorder: recorder}
		current, err := r.machine("node-1")
		if err != nil {
			t.Fatal(err)
		}
		mm := newMachineMetrics(metrics.NewOperator(component, machine.Version, []string{machineKind}, watchKinds), &fetcher{})

		if err := reconcile(r, current, "lab", &fetcher{}, kubernetes.NewHeartbeat("node-1"), mm, nil); err != nil {
			t.Fatal(err)
		}

		want := "Normal Uncordoned: uncordoned the Node node-1; its reboot is complete"
		if got := posted(recorded); !slices.Contains(got, want) {
			t.Errorf("got %q, want it to hold %q", got, want)
		}
	})
}

// The Machine that the operator creates from the boot manifest posts
// MachineJoined, and a Machine that exists already posts nothing.
func TestCreatingTheMachinePostsMachineJoined(t *testing.T) {
	cases := []struct {
		name   string
		exists bool
		want   []string
	}{
		{"an absent Machine", false, []string{"Normal MachineJoined: created the Machine from the boot manifest, because the cluster held none"}},
		{"a Machine that exists", true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, recorder, recorded := recordingClient(t, (&machineAPI{exists: c.exists}).handler())

				if _, err := ensureMachine(client, seedMachine(), recorder); err != nil {
					t.Fatal(err)
				}

				if got := posted(recorded); !slices.Equal(got, c.want) {
					t.Errorf("got %q, want %q", got, c.want)
				}
			})
		})
	}
}
