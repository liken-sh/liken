package main

// The fake API server plays the Job controller too: a Job ends as soon
// as the operator creates it, Complete, unless a test holds the Jobs
// running or makes them fail.

const jobsPath = "/apis/batch/v1/namespaces/" + testNamespace + "/jobs"

type fakeJobController struct {
	// hold keeps each new Job running until a test ends it.
	hold bool
	// failure is the Failed condition of each new Job, when it is set.
	failure *jobCondition
	// terminated is the state of the container of each failed Job's
	// pod, when it is set. Without it, a failed Job has no pod.
	terminated map[string]any
}

// started answers the status of a Job that the operator creates now.
func (c *fakeJobController) started() map[string]any {
	switch {
	case c.hold:
		return map[string]any{"active": 1}
	case c.failure != nil:
		return failedJob(*c.failure)
	}
	return completeJob()
}

// completeJob is the status of a Job whose pod succeeded, as the Job
// controller of Kubernetes 1.31 and later writes it.
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
// at once, or, with no reason, succeed again.
func (a *fakeAPI) failJobs(reason, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.jobs.failure = nil
	if reason != "" {
		a.jobs.failure = &jobCondition{Type: "Failed", Status: "True", Reason: reason, Message: message}
	}
}

// completeJob ends a held Job, Complete.
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

// failedPod stores the pod of a Job that failed, with the labels of the
// Job's template and the label that the Job controller adds. The
// caller holds a.mu.
func (a *fakeAPI) failedPod(object map[string]any) {
	if a.jobs.failure == nil || a.jobs.terminated == nil {
		return
	}
	name := object["metadata"].(map[string]any)["name"].(string)
	template := object["spec"].(map[string]any)["template"].(map[string]any)
	labels := clone(template["metadata"].(map[string]any)["labels"].(map[string]any))
	labels["batch.kubernetes.io/job-name"] = name
	a.store(podsCollection, name+"-x7k2p", map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name + "-x7k2p", "namespace": testNamespace, "labels": labels},
		"status": map[string]any{"phase": "Failed", "containerStatuses": []any{
			map[string]any{"name": "job", "state": map[string]any{"terminated": a.jobs.terminated}},
		}},
	}, "ADDED")
}
