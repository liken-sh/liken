package main

// The fake API server also plays the Job controller: a Job becomes
// Complete as soon as the operator creates it, unless a test holds Jobs
// running or makes them fail.

import "time"

const jobsPath = "/apis/batch/v1/namespaces/" + testNamespace + "/jobs"

type fakeJobController struct {
	// hold keeps each new Job running until a test completes it.
	hold bool
	// failure is the Failed condition to give each new Job, when it is
	// set.
	failure *jobCondition
	// terminated is the container state of each failed Job's pod, when
	// it is set. Without it, a failed Job has no pod.
	terminated map[string]any
	// evicted gives each failed Job, before the pod that failed it, a
	// pod that the kubelet evicted.
	evicted bool
}

// started returns the status of a Job when the operator creates it.
func (c *fakeJobController) started() map[string]any {
	switch {
	case c.hold:
		return map[string]any{"active": 1}
	case c.failure != nil:
		return failedJob(*c.failure)
	}
	return completeJob()
}

// completeJob returns the status of a Job whose pod succeeded, as the
// Job controller in Kubernetes 1.31 and later writes it.
func completeJob() map[string]any {
	return map[string]any{"succeeded": 1, "conditions": []any{
		map[string]any{"type": "SuccessCriteriaMet", "status": "True"},
		map[string]any{"type": "Complete", "status": "True"},
	}}
}

func failedJob(c jobCondition) map[string]any {
	return map[string]any{"failed": 1, "conditions": []any{
		map[string]any{"type": "FailureTarget", "status": "True", "reason": c.Reason, "message": c.Message},
		map[string]any{"type": "Failed", "status": "True", "reason": c.Reason, "message": c.Message},
	}}
}

// holdJobs keeps each Job that the operator creates from now on
// running.
func (a *fakeAPI) holdJobs() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.jobs.hold = true
}

// failJobs makes each Job that the operator creates from now on fail
// immediately, or succeed again when reason is empty.
func (a *fakeAPI) failJobs(reason, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.jobs.failure = nil
	if reason != "" {
		a.jobs.failure = &jobCondition{Type: "Failed", Status: "True", Reason: reason, Message: message}
	}
}

// completeJob makes a held Job Complete, and stops holding new Jobs.
func (a *fakeAPI) completeJob(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.jobs.hold = false
	held, ok := a.objects[jobsPath][name]
	if !ok {
		return
	}
	next := clone(held)
	next["status"] = completeJob()
	a.store(jobsPath, name, next, "MODIFIED")
}

// terminateJobPods gives each Job that fails from now on a pod whose
// container ended with an exit code and a termination message, as the
// kubelet reports it.
func (a *fakeAPI) terminateJobPods(exitCode int, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.jobs.terminated = map[string]any{"exitCode": exitCode, "reason": "Error", "message": message}
}

// evictJobPods gives each Job that fails from now on an earlier pod that
// the kubelet evicted, which the Job's podFailurePolicy ignored. Its
// container ended with exit code 137, as SIGKILL ends one.
func (a *fakeAPI) evictJobPods() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.jobs.evicted = true
}

// failedPod stores the pod of a failed Job, with the labels of the
// Job's pod template and the batch.kubernetes.io/job-name label that
// the Job controller adds. The caller holds a.mu.
func (a *fakeAPI) failedPod(object map[string]any) {
	if a.jobs.failure == nil || a.jobs.terminated == nil {
		return
	}
	name := object["metadata"].(map[string]any)["name"].(string)
	template := object["spec"].(map[string]any)["template"].(map[string]any)
	labels := clone(template["metadata"].(map[string]any)["labels"].(map[string]any))
	labels["batch.kubernetes.io/job-name"] = name
	now := time.Now().UTC()
	if a.jobs.evicted {
		a.store(podsCollection, name+"-a4c9m", map[string]any{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]any{"name": name + "-a4c9m", "namespace": testNamespace, "labels": clone(labels),
				"creationTimestamp": now.Add(-time.Minute).Format(time.RFC3339)},
			"status": map[string]any{"phase": "Failed", "reason": "Evicted",
				"conditions": []any{map[string]any{"type": "DisruptionTarget", "status": "True"}},
				"containerStatuses": []any{
					map[string]any{"name": "job", "state": map[string]any{"terminated": map[string]any{"exitCode": 137, "reason": "Error"}}},
				}},
		}, "ADDED")
	}
	a.store(podsCollection, name+"-x7k2p", map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name + "-x7k2p", "namespace": testNamespace, "labels": labels,
			"creationTimestamp": now.Format(time.RFC3339)},
		"status": map[string]any{"phase": "Failed", "containerStatuses": []any{
			map[string]any{"name": "job", "state": map[string]any{"terminated": a.jobs.terminated}},
		}},
	}, "ADDED")
}
