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
