package main

// The job action: a container that the operator runs once as a Job,
// owned by the resource, with the environment that names what ran it.

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// exampleJob is the Job of the example's Observatory, whose activation
// runs at the first second of the bubble's clock.
const exampleJob = "observatory-lab-activation-a799431dd4"

// exampleJobDone is the record of the example's job in the Activation
// step.
const exampleJobDone = "Observatory lab job: busybox:1.37 Done: Job " + exampleJob + " succeeded"

// jobNamed answers the Job whose name begins with prefix.
func jobNamed(t *testing.T, w *world, prefix string) job {
	t.Helper()
	for _, name := range w.api.names(jobsPath) {
		if strings.HasPrefix(name, prefix) {
			j, _ := decode[job](t, w.api, jobsPath, name)
			return j
		}
	}
	t.Fatalf("no Job %s* in %v", prefix, w.api.names(jobsPath))
	return job{}
}

func environment(j job) []string {
	var out []string
	for _, v := range j.Spec.Template.Spec.Containers[0].Env {
		out = append(out, v.Name+"="+v.Value)
	}
	return out
}

// mountWithAJob gives the east mount a job after its unpark, with a
// variable of its own and one that the operator's own replaces.
func mountWithAJob(w *world) {
	w.amend(observatory.MountKind, "east", func(spec map[string]any) {
		spec["activation"] = []any{
			map[string]any{"state": "Unparked"},
			map[string]any{"timeout": "2m", "job": map[string]any{"image": "curlimages/curl:8.10.1", "args": []any{"http://relay.local/1/on"},
				"env": []any{map[string]any{"name": "RELAY", "value": "1"}, map[string]any{"name": "LIKEN_TRIGGER", "value": "mine"}}}},
		}
	})
}

// The operator adds the resource, the trigger, and the INDI server to
// each Job's environment, after the person's own variables.
func TestAJobNamesWhatRanIt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		setUp    func(w *world)
		prefix   string
		owner    string
		deadline int64
		env      []string
	}{
		{name: "the example's observatory", setUp: func(*world) {}, prefix: exampleJob, owner: "Observatory lab", deadline: 600,
			env: []string{"LIKEN_OBSERVATORY=lab", "LIKEN_RESOURCE=Observatory/lab", "LIKEN_TRIGGER=activation",
				"INDI_HOST=lab-observatory.observatory.svc", "INDI_PORT=7624"}},
		{name: "a mount of a telescope", setUp: mountWithAJob, prefix: "mount-east-activation-", owner: "Mount east", deadline: 120,
			env: []string{"RELAY=1", "LIKEN_OBSERVATORY=lab", "LIKEN_TELESCOPE=east", "LIKEN_RESOURCE=Mount/east", "LIKEN_TRIGGER=activation",
				"INDI_HOST=east-telescope.observatory.svc", "INDI_PORT=7624"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.setUp(w)
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
				j := jobNamed(t, w, c.prefix)
				if got := environment(j); !slices.Equal(got, c.env) {
					t.Errorf("env = %v, want %v", got, c.env)
				}
				owner := j.Metadata.OwnerReferences[0]
				if got := owner.Kind + " " + owner.Name; got != c.owner || !owner.Controller {
					t.Errorf("owner = %+v, want %s", owner, c.owner)
				}
				if got := *j.Spec.ActiveDeadlineSeconds; got != c.deadline {
					t.Errorf("activeDeadlineSeconds = %d, want %d", got, c.deadline)
				}
			})
		})
	}
}

// A Job runs once, Kubernetes deletes it an hour after it ends, and its
// pod meets the restricted Pod Security level.
func TestAJobRunsOnceAndRestricted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		j := jobNamed(t, w, exampleJob)
		pod := j.Spec.Template.Spec
		security := pod.Containers[0].SecurityContext
		cases := []struct{ field, got, want string }{
			{"backoffLimit", fmt.Sprint(*j.Spec.BackoffLimit), "0"},
			{"ttlSecondsAfterFinished", fmt.Sprint(*j.Spec.TTLSecondsAfterFinished), "3600"},
			{"restartPolicy", pod.RestartPolicy, "Never"},
			{"automountServiceAccountToken", fmt.Sprint(*pod.AutomountServiceAccountToken), "false"},
			{"runAsNonRoot", fmt.Sprint(security.RunAsNonRoot), "true"},
			{"allowPrivilegeEscalation", fmt.Sprint(security.AllowPrivilegeEscalation), "false"},
			{"capabilities.drop", fmt.Sprint(security.Capabilities.Drop), "[ALL]"},
			{"seccompProfile.type", security.SeccompProfile.Type, "RuntimeDefault"},
			{"image", pod.Containers[0].Image, "busybox:1.37"},
			{"the pod's managed-by label", j.Spec.Template.Metadata.Labels[labelManagedBy], ""},
		}
		for _, c := range cases {
			if c.got != c.want {
				t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
			}
		}
	})
}

// A Job that fails fails its action and the step, with the Job's reason
// and message. The retry annotation deletes that Job and runs a new
// one.
func TestAFailedJobFailsTheStepAndARetryRunsItAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.failJobs("BackoffLimitExceeded", "Job has reached the specified backoff limit")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationFailed, 10*time.Minute)
		want := "Failed: Observatory lab: job: busybox:1.37: Job " + exampleJob + " failed: BackoffLimitExceeded: Job has reached the specified backoff limit"
		if got := stepOf(r, observatory.StepActivation).Summary; got != want {
			t.Errorf("Activation = %q, want %q", got, want)
		}
		w.api.failJobs("", "")
		time.Sleep(time.Minute)
		w.retry("east-tonight")
		r = w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if text := stepText(stepOf(r, observatory.StepActivation)); !strings.Contains(text, exampleJobDone) {
			t.Errorf("Activation =\n%s", text)
		}
		if n := w.api.creates(jobsPath); n != 2 {
			t.Errorf("the operator created %d Jobs, want 2", n)
		}
	})
}

// An operator that restarts while a Job runs finds the Job it created,
// and creates no second one.
func TestANewOperatorFindsTheJobItCreated(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.holdJobs()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.until(time.Minute, "the observatory's activation does not record its job", func() bool {
			run := lastRun(t, w, observatory.ObservatoryKind, "lab", observatory.TriggerActivation)
			return len(run.Actions) == 1 && run.Actions[0].Summary == "Running Job "+exampleJob
		})
		w.restart()
		w.api.completeJob(exampleJob)
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if n := w.api.creates(jobsPath); n != 1 {
			t.Errorf("the operator created %d Jobs, want 1", n)
		}
	})
}

// A Job's name is a DNS label of at most 63 characters, and differs
// for each transition and each action of a run.
func TestAJobsNameIsADNSLabel(t *testing.T) {
	t.Parallel()
	label := regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)
	since := time.Date(2026, 10, 6, 21, 0, 0, 0, time.UTC)
	dome := resource{kind: observatory.DomeKind, meta: observatory.ObjectMeta{Name: "lab"}}
	long := resource{kind: observatory.WeatherStationKind, meta: observatory.ObjectMeta{Name: strings.Repeat("roof-station-", 5) + "north"}}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"a short name", jobName(dome, "triggers[0]", since, 0), "dome-lab-triggers-0-"},
		{"a long name", jobName(long, "deactivation", since, 0), "weatherstation-roof-station-roof-station-roof-statio-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.HasPrefix(c.got, c.want) || len(c.got) > maxName || !label.MatchString(c.got) {
				t.Errorf("jobName = %q (%d characters), want a DNS label that begins %q", c.got, len(c.got), c.want)
			}
		})
	}
	names := []string{
		jobName(dome, "triggers[0]", since, 0),
		jobName(dome, "triggers[0]", since, 1),
		jobName(dome, "triggers[0]", since.Add(time.Second), 0),
		jobName(dome, "triggers[1]", since, 0),
	}
	if unique := slices.Compact(slices.Sorted(slices.Values(names))); len(unique) != len(names) {
		t.Errorf("names repeat: %v", names)
	}
}
