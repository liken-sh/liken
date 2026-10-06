package main

// The job action: a container that the operator runs once as a
// batch/v1 Job, for what the vocabulary of target states lacks, such
// as a dew heater's relay or a webhook. The operator watches its Jobs
// as it watches its pods, by its label, and the Job's conditions end
// the action. Nothing polls the Job.
//
// The Job's name comes from the resource, the trigger, the transition
// time of the run, and the place of the action in the run. So an
// operator that restarts during a run finds the Job it created, and
// creates no second one. A run that runs again, such as a failed
// activation after the retry annotation, finds the Job of the earlier
// run under the same name. That Job is older than the run, so the
// operator deletes it and creates a new one.

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
	"strconv"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// roleJob is the role of a Job's labels.
const roleJob = "job"

// jobTTL is how long Kubernetes keeps a Job after it ends, so a person
// can read its logs.
const jobTTL = int32(time.Hour / time.Second)

func jobsCollection(namespace string) string {
	return "/apis/batch/v1/namespaces/" + namespace + "/jobs"
}

// notLabel matches each run of characters that a DNS label cannot
// hold.
var notLabel = regexp.MustCompile(`[^a-z0-9]+`)

// jobName answers the name of the Job of one action of one run, such
// as dome-lab-triggers-0-3f9a1c2b7e. The readable part names the
// resource and the trigger, and the hash names the transition and the
// action, so the name stays a DNS label of at most 63 characters.
func jobName(r resource, trigger string, since time.Time, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n%d", r.key(), trigger, since.Unix(), index)))
	hash := hex.EncodeToString(sum[:])[:10]
	readable := strings.Trim(notLabel.ReplaceAllString(strings.ToLower(r.kind.Name+"-"+r.name()+"-"+trigger), "-"), "-")
	if limit := maxName - len(hash) - 1; len(readable) > limit {
		readable = strings.TrimRight(readable[:limit], "-")
	}
	return readable + "-" + hash
}

// jobEnvironment answers the variables that the operator adds to a
// Job's environment: what ran the Job, and where the resource's INDI
// server listens.
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

// serverOf answers the INDI server of a resource: a device's server,
// or the server that a Telescope or an Observatory runs.
func serverOf(t *tree, r resource) (serverRef, bool) {
	if r.device != nil {
		return t.server(r.device)
	}
	return serverRef{r.kind, r.name()}, true
}

// jobFor answers the Job of one action. The person's variables come
// first, and a variable with the name of one of the operator's own is
// left out, so the operator's value holds.
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
	// The namespace states no Pod Security level, so a cluster's
	// default applies. The container meets the restricted level, the
	// strictest, so the Job's pod starts under any of them.
	security.SeccompProfile = &seccompProfile{Type: "RuntimeDefault"}
	jobLabels := map[string]string{
		labelManagedBy: managedBy, labelPartOf: partOf, labelName: name, labelRole: roleJob,
		labelKind: r.kind.Name, labelResource: r.name(),
	}
	// The pod carries no managed-by label: the operator's pod watch
	// selects by it, and a Job's pod is the Job controller's.
	podLabels := map[string]string{labelPartOf: partOf, labelName: name, labelRole: roleJob}
	return &job{
		APIVersion: "batch/v1", Kind: "Job",
		Metadata: jobMeta{meta: meta{
			Name: name, Namespace: t.namespace, Labels: jobLabels,
			Annotations:     map[string]string{Group + "/trigger": trigger},
			OwnerReferences: []ownerReference{owner(observatory.APIVersion, r.kind, r.name(), r.meta.UID)},
		}},
		Spec: jobSpec{
			// The action runs once. A step's retry annotation runs a
			// failed action again with a new Job.
			BackoffLimit:            int32Pointer(0),
			ActiveDeadlineSeconds:   int64Pointer(int64(limit / time.Second)),
			TTLSecondsAfterFinished: int32Pointer(jobTTL),
			Template: podTemplate{
				Metadata: meta{Labels: podLabels},
				Spec: podSpec{
					RestartPolicy:                "Never",
					EnableServiceLinks:           boolPointer(false),
					AutomountServiceAccountToken: boolPointer(false),
					Containers: []container{{
						Name: "job", Image: spec.Image, Command: spec.Command, Args: spec.Args, Env: env,
						SecurityContext: security,
						// A script that fails usually prints why and exits,
						// so the end of its log is the reason a person needs.
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

// ended answers whether a Job ended, and the error of one that failed.
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

// runJob runs one job action, and waits until its Job ends. ctx holds
// the action's deadline, and the action's timeout bounds the Job too.
func (o *operator) runJob(ctx context.Context, c procCall, spec observatory.Job, at place, report func(string)) (string, error) {
	name := jobName(c.res, c.trigger, c.since, at.index)
	created := false
	err := o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		j, held := t.jobs[name]
		switch {
		case held && j.Metadata.CreationTimestamp != nil && j.Metadata.CreationTimestamp.Before(at.start):
			// The Job of an earlier run of the same transition.
			created = false
			return false, "deleting Job " + name + " of an earlier run", o.deleteJob(name)
		case held:
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

// deleteJob deletes a Job and its pods. The API deletes a Job's pods
// only with a propagation policy: with none, it leaves them running.
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

// messageTail is how many bytes of a failed container's termination
// message an action's summary keeps: the last lines, which name what
// the script did when it failed.
const messageTail = 300

// jobPodEnd answers how the container of a failed Job's pod ended,
// such as "exit code 3: checking the dew heater", and false when no
// pod reports a terminated container. The pod carries no managed-by
// label, so no watch of the operator holds it. The Job's Failed
// condition is the event, and this reads the pod once, when it
// arrives.
func (o *operator) jobPodEnd(name string) (string, bool) {
	var pods struct {
		Items []pod `json:"items"`
	}
	path := "/api/v1/namespaces/" + o.namespace + "/pods?labelSelector=" + url.QueryEscape(labelName+"="+name)
	if err := o.client.RequestJSON(http.MethodGet, path, nil, &pods); err != nil {
		o.logf("reading the pod of Job %s: %v", name, err)
		return "", false
	}
	for _, p := range pods.Items {
		for _, c := range p.Status.ContainerStatuses {
			if end := c.State.Terminated; end != nil {
				return terminationText(*end), true
			}
		}
	}
	return "", false
}

// terminationText says how a container ended: its exit code, a reason
// other than the plain Error, such as OOMKilled, and the last lines of
// its message, one line after another.
func terminationText(end terminated) string {
	text := fmt.Sprintf("exit code %d", end.ExitCode)
	if end.Reason != "" && end.Reason != "Error" {
		text += " (" + end.Reason + ")"
	}
	message := strings.TrimSpace(end.Message)
	if len(message) > messageTail {
		message = message[len(message)-messageTail:]
		// The cut can fall inside a line or a character, so the text
		// starts at the next whole line.
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
