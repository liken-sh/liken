package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// exampleJob is the name of the Job for the example Observatory's
// activation, which runs at the first second of the synctest bubble's
// clock. The name depends on that time.
const exampleJob = "observatory-lab-activation-a799431dd4"

// exampleJobDone is the step record for the example's job action in
// the Activation step.
const exampleJobDone = "Observatory lab job: busybox:1.38.0 Done: Job " + exampleJob + " succeeded"

// jobNamed returns the Job whose name begins with prefix.
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

// mountWithAJob adds a job action after the east mount's unpark. The
// job sets one variable of its own and one, LIKEN_TRIGGER, that the
// operator's own value overrides.
func mountWithAJob(w *world) {
	w.amend(observatory.MountKind, "east", func(spec map[string]any) {
		spec["activation"] = []any{
			map[string]any{"state": "Unparked"},
			map[string]any{"timeout": "2m", "job": map[string]any{"image": "curlimages/curl:8.23.0", "args": []any{"http://relay.local/1/on"},
				"env": []any{map[string]any{"name": "RELAY", "value": "1"}, map[string]any{"name": "LIKEN_TRIGGER", "value": "mine"}}}},
		}
	})
}

// The operator adds the resource, the trigger, and the INDI server to
// each Job's environment, after the person's own variables.
func TestAJobEnvironmentIdentifiesTheResourceTriggerAndServer(t *testing.T) {
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
			{"image", pod.Containers[0].Image, "busybox:1.38.0"},
			{"terminationMessagePolicy", pod.Containers[0].TerminationMessagePolicy, "FallbackToLogsOnError"},
			{"the pod's managed-by label", j.Spec.Template.Metadata.Labels[labelManagedBy], ""},
		}
		for _, c := range cases {
			if c.got != c.want {
				t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
			}
		}
	})
}

// A pod that the kubelet ends, such as for an eviction, gets the
// condition DisruptionTarget, and the Job's podFailurePolicy runs a new
// pod in its place without counting a failure. The fake API server runs
// no Job controller that reads the policy, so the test reads the Job
// that the operator created.
func TestAJobRunsItsPodAgainWhenTheKubeletEndsIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		j := jobNamed(t, w, exampleJob)
		stored, _ := w.api.object(jobsPath, j.Metadata.Name)

		policy, _ := json.Marshal(stored["spec"].(map[string]any)["podFailurePolicy"])
		want := `{"rules":[{"action":"Ignore","onPodConditions":[{"status":"True","type":"DisruptionTarget"}]}]}`
		if string(policy) != want {
			t.Errorf("podFailurePolicy = %s, want %s", policy, want)
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
		want := "Failed: Observatory lab: job: busybox:1.38.0: Job " + exampleJob + " failed: BackoffLimitExceeded: Job has reached the specified backoff limit"
		if got := stepOf(r, observatory.StepActivation).Summary; got != want {
			t.Errorf("Activation = %q, want %q", got, want)
		}
		w.api.failJobs("", "")
		time.Sleep(time.Minute)
		w.retry("east-tonight")
		r = w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if text := stepText(stepOf(r, observatory.StepActivation)); !strings.Contains(text, exampleJobDone) {
			t.Errorf("Activation =\n%s\nwant it to contain %q", text, exampleJobDone)
		}
		if n := w.api.creates(jobsPath); n != 2 {
			t.Errorf("the operator created %d Jobs, want 2", n)
		}
	})
}

// A failed Job's error includes its pod's exit code and the last lines
// of its termination message, at most messageTail bytes. With
// FallbackToLogsOnError, the kubelet fills the message from the end of
// the container's log. When no pod has a terminated container, the
// error has only the Job's own reason.
func TestAFailedJobErrorIncludesThePodExit(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("reading the relay board\n", 40) + "checking the dew heater\n"
	cases := []struct {
		name    string
		pod     bool
		evicted bool
		message string
		why     string
	}{
		{"a short message", true, false, "checking the dew heater\n", "exit code 3: checking the dew heater"},
		{"a long message", true, false, long, "exit code 3: " + strings.Repeat("reading the relay board; ", 11) + "checking the dew heater"},
		{"no message", true, false, "", "exit code 3"},
		{"an evicted pod before the pod that failed", true, true, "checking the dew heater\n", "exit code 3: checking the dew heater"},
		{"no pod", false, false, "", "BackoffLimitExceeded: Job has reached the specified backoff limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.failJobs("BackoffLimitExceeded", "Job has reached the specified backoff limit")
				if c.pod {
					w.api.terminateJobPods(3, c.message)
				}
				if c.evicted {
					w.api.evictJobPods()
				}
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				r := w.phase("east-tonight", observatory.ReservationFailed, 10*time.Minute)
				want := "Failed: Observatory lab: job: busybox:1.38.0: Job " + exampleJob + " failed: " + c.why
				if got := stepOf(r, observatory.StepActivation).Summary; got != want {
					t.Errorf("Activation = %q\nwant %q", got, want)
				}
			})
		})
	}
}

// An operator that restarts while a Job runs finds the Job it created
// and does not create a second one.
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
		t.Errorf("jobName returns the names %v, want all different", names)
	}
}

// When a person deletes a device's train while the reservation is
// ending, the device's deactivation Job still runs on the device's
// server. Its environment names the INDI server of the device's pod.
func TestAJobOfADeviceWhoseTrainIsDeletedNamesItsServer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.amend(observatory.DustCapKind, "east", func(spec map[string]any) {
			spec["deactivation"] = []any{map[string]any{"job": map[string]any{"image": "busybox:1.38.0", "args": []any{"true"}}}}
		})
		readyOf(w)
		w.deleteDuringAbort(named{observatory.OpticalTrainKind, "east-imaging"})
		w.gone(observatory.ReservationKind, "east-tonight", 15*time.Minute)
		want := []string{"LIKEN_OBSERVATORY=lab", "LIKEN_TELESCOPE=east", "LIKEN_RESOURCE=DustCap/east", "LIKEN_TRIGGER=deactivation",
			"INDI_HOST=east-telescope.observatory.svc", "INDI_PORT=7624"}
		if got := environment(jobNamed(t, w, "dustcap-east-deactivation-")); !slices.Equal(got, want) {
			t.Errorf("env = %v, want %v", got, want)
		}
	})
}
