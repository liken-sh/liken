package main

// The job action runs a container once as a batch/v1 Job, for work
// that a state action cannot do, such as switching a dew heater's relay
// or calling a webhook. The operator watches its Jobs by label, as it
// watches its pods, and the action ends when the Job's Complete or
// Failed condition is True.
//
// A Job's name is derived from the resource, the trigger, the time the
// run's transition began, and the action's index, so an operator that
// restarts during a run finds the Job it already created. A retry of a
// failed run computes the same name, finds a Job created before the
// retry started, and deletes it before it creates a new one.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// roleJob is the value of the role label on a Job and its pod.
const roleJob = "job"

// jobTTL is how long Kubernetes keeps a finished Job, so a person can
// still read its logs.
const jobTTL = int32(time.Hour / time.Second)

func jobsCollection(namespace string) string {
	return "/apis/batch/v1/namespaces/" + namespace + "/jobs"
}

// notLabel matches each run of characters that a DNS label cannot
// contain.
var notLabel = regexp.MustCompile(`[^a-z0-9]+`)

// jobName returns the name of the Job for one action of one run, such
// as dome-lab-triggers-0-3f9a1c2b7e. The readable prefix comes from the
// resource and the trigger, and the hash also covers the transition
// time and the action index. The prefix is cut so the name stays a DNS
// label of at most 63 characters.
func jobName(r resource, trigger string, since time.Time, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n%d", r.key(), trigger, since.Unix(), index)))
	hash := hex.EncodeToString(sum[:])[:10]
	readable := strings.Trim(notLabel.ReplaceAllString(strings.ToLower(r.kind.Name+"-"+r.name()+"-"+trigger), "-"), "-")
	if limit := maxName - len(hash) - 1; len(readable) > limit {
		readable = strings.TrimRight(readable[:limit], "-")
	}
	return readable + "-" + hash
}

// jobEnvironment returns the variables that the operator adds to a
// Job's environment: the resource and trigger that started the Job,
// and the address of the resource's INDI server.
func jobEnvironment(t *tree, r resource, trigger string) []envVar {
	env := []envVar{{observatory.EnvObservatory, r.observatory}}
	if r.telescope != "" {
		env = append(env, envVar{observatory.EnvTelescope, r.telescope})
	}
	env = append(env, envVar{observatory.EnvResource, r.key()}, envVar{observatory.EnvTrigger, trigger})
	if ref, ok := serverOf(t, r); ok {
		env = append(env, envVar{observatory.EnvINDIHost, serviceHost(ref.String(), t.namespace)}, envVar{observatory.EnvINDIPort, strconv.Itoa(serverPort)})
	}
	return env
}

// serverOf returns the INDI server of a resource. For a device, the
// server of its driver pod comes before the server of its parents
// (tree.runningOn), because a device that left its server, or whose
// train a person deleted, runs its deactivation where its driver runs.
func serverOf(t *tree, r resource) (serverRef, bool) {
	if r.device != nil {
		if ref, running := t.runningOn(r.device); running {
			return ref, true
		}
		return t.server(r.device)
	}
	return serverRef{r.kind, r.name()}, true
}

// jobFor builds the Job for one action. It drops a variable of the
// person's that has the name of one of the operator's, so the container
// sees the operator's value.
func jobFor(t *tree, name string, r resource, trigger string, spec observatory.Job, limit time.Duration) *job {
	own := jobEnvironment(t, r, trigger)
	var env []envVar
	for _, v := range spec.Env {
		if !hasVariable(own, v.Name) {
			env = append(env, envVar{v.Name, v.Value})
		}
	}
	env = append(env, own...)
	security := restricted()
	// The Job runs in the resource's namespace, whatever Pod Security
	// level it enforces. The pod meets restricted, the strictest
	// level, so it starts under any level.
	security.SeccompProfile = &seccompProfile{Type: "RuntimeDefault"}
	jobLabels := map[string]string{
		labelManagedBy: managedBy, labelPartOf: partOf, labelName: name, labelRole: roleJob,
		labelKind: r.kind.Name, labelResource: r.name(),
	}
	// The pod has no managed-by label, so the operator's pod watch,
	// which selects on that label, leaves it to the Job controller.
	podLabels := map[string]string{labelPartOf: partOf, labelName: name, labelRole: roleJob}
	return &job{
		APIVersion: "batch/v1", Kind: "Job",
		Metadata: jobMeta{meta: meta{
			Name: name, Namespace: t.namespace, Labels: jobLabels,
			Annotations:     map[string]string{Group + "/trigger": trigger},
			OwnerReferences: []ownerReference{owner(observatory.APIVersion, r.kind, r.name(), r.meta.UID)},
		}},
		Spec: jobSpec{
			// A failed action runs again only when a person sets the
			// retry annotation, which creates a new Job.
			BackoffLimit:            int32Pointer(0),
			ActiveDeadlineSeconds:   int64Pointer(int64(limit / time.Second)),
			TTLSecondsAfterFinished: int32Pointer(jobTTL),
			// A pod that the kubelet ends, such as for an eviction, a
			// preemption, or a node's shutdown, has the condition
			// DisruptionTarget. Its container did not fail, so the Job
			// controller runs a new pod and counts no failure. The
			// deadline above still bounds the action. The API server
			// accepts a podFailurePolicy only with the restartPolicy Never
			// below.
			PodFailurePolicy: &podFailurePolicy{Rules: []podFailurePolicyRule{{
				Action:          "Ignore",
				OnPodConditions: []podConditionMatch{{Type: "DisruptionTarget", Status: "True"}},
			}}},
			Template: podTemplate{
				Metadata: meta{Labels: podLabels},
				Spec: podSpec{
					RestartPolicy:                "Never",
					EnableServiceLinks:           boolPointer(false),
					AutomountServiceAccountToken: boolPointer(false),
					Containers: []container{{
						Name: "job", Image: spec.Image, Command: spec.Command, Args: spec.Args, Env: env,
						SecurityContext: security,
						// A failed script usually prints the reason and exits,
						// so the end of its log is the most useful message.
						TerminationMessagePolicy: "FallbackToLogsOnError",
						VolumeMounts:             []volumeMount{{Name: "tmp", MountPath: tmpDir}},
					}},
					Volumes: []volume{{Name: "tmp", EmptyDir: &emptyDir{}}},
				},
			},
		},
	}
}

func hasVariable(env []envVar, name string) bool {
	for _, v := range env {
		if v.Name == name {
			return true
		}
	}
	return false
}

func int32Pointer(i int32) *int32 { return &i }

// ended reports whether a Job finished, and returns an error when it
// failed.
func (j *job) ended() (bool, error) {
	for _, c := range j.Status.Conditions {
		switch {
		case c.Status != "True":
		case c.Type == "Complete":
			return true, nil
		case c.Type == "Failed":
			return true, fmt.Errorf("Job %s failed: %s: %s", j.Metadata.Name, c.Reason, c.Message)
		}
	}
	return false, nil
}

// runJob creates the Job for one job action and waits until it ends.
// The action's timeout is both the deadline of ctx and the Job's
// activeDeadlineSeconds.
func (o *operator) runJob(ctx context.Context, c procCall, spec observatory.Job, at place, report func(string)) (string, error) {
	name := jobName(c.res, c.trigger, c.since, at.index)
	created := false
	err := o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		j, found := t.jobs[name]
		switch {
		case found && j.Metadata.CreationTimestamp != nil && j.Metadata.CreationTimestamp.Before(at.start):
			created = false
			return false, "deleting Job " + name + " of an earlier run", o.deleteJob(name)
		case found:
			done, err := j.ended()
			if err != nil {
				if why, ok := o.jobPodEnd(name); ok {
					err = fmt.Errorf("Job %s failed: %s", name, why)
				}
			}
			return done, "running Job " + name, err
		case created:
			return false, "waiting for Job " + name, nil
		}
		created = true
		return false, "creating Job " + name, o.create(jobsCollection(o.namespace), jobFor(t, name, asNow(t, c), c.trigger, spec, at.limit))
	})
	if err != nil {
		return "", err
	}
	return "Job " + name + " succeeded", nil
}

// deleteJob deletes a Job and its pods. Without a propagation policy,
// the API server leaves a deleted Job's pods running.
func (o *operator) deleteJob(name string) error {
	body, err := json.Marshal(map[string]any{"propagationPolicy": "Background"})
	if err != nil {
		return err
	}
	err = o.client.RequestJSON(http.MethodDelete, jobsCollection(o.namespace)+"/"+name, body, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// messageTail is how many bytes from the end of a failed container's
// termination message go into the action's summary.
const messageTail = 300

// jobPodEnd describes how the container of a failed Job's pod ended,
// such as "exit code 3: checking the dew heater". It returns false
// when no pod reports a terminated container. No watch caches the pod,
// so runJob reads it once, after the Job fails.
//
// The Job's podFailurePolicy ignores a pod that the kubelet ended, which
// has the condition DisruptionTarget, and runs a new pod. So the Job's
// failure comes from the newest pod without that condition, and the
// exit code of an ignored pod says nothing about the action.
func (o *operator) jobPodEnd(name string) (string, bool) {
	var pods struct {
		Items []struct {
			Metadata struct {
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
			Status podStatus `json:"status"`
		} `json:"items"`
	}
	path := "/api/v1/namespaces/" + o.namespace + "/pods?labelSelector=" + url.QueryEscape(labelName+"="+name)
	if err := o.client.RequestJSON(http.MethodGet, path, nil, &pods); err != nil {
		o.logf("reading the pod of Job %s: %v", name, err)
		return "", false
	}
	var end *terminated
	var newest time.Time
	for _, p := range pods.Items {
		if slices.ContainsFunc(p.Status.Conditions, func(c podCondition) bool { return c.Type == "DisruptionTarget" && c.Status == "True" }) {
			continue
		}
		for _, c := range p.Status.ContainerStatuses {
			if c.State.Terminated != nil && (end == nil || p.Metadata.CreationTimestamp.After(newest)) {
				end, newest = c.State.Terminated, p.Metadata.CreationTimestamp
			}
		}
	}
	if end == nil {
		return "", false
	}
	return terminationText(*end), true
}

// terminationText describes how a container ended: its exit code, its
// reason when that is not the plain Error (such as OOMKilled), and the
// last lines of its termination message joined by semicolons.
func terminationText(end terminated) string {
	text := fmt.Sprintf("exit code %d", end.ExitCode)
	if end.Reason != "" && end.Reason != "Error" {
		text += " (" + end.Reason + ")"
	}
	message := strings.TrimSpace(end.Message)
	if len(message) > messageTail {
		message = message[len(message)-messageTail:]
		// The cut can fall inside a line or a multi-byte character, so
		// drop the partial line and any invalid bytes.
		if _, rest, found := strings.Cut(message, "\n"); found {
			message = rest
		}
		message = strings.ToValidUTF8(message, "")
	}
	var lines []string
	for line := range strings.Lines(message) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return text
	}
	return text + ": " + strings.Join(lines, "; ")
}
