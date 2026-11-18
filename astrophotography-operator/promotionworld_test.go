package main

// The setup for the promotion loop's tests: the operator running
// against the fake API server, and builders for the Targets, claims,
// Jobs, and pods that the loop reads.

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/promote"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
)

const (
	observatoryNS = "observatory"
	claimsPath    = "/api/v1/persistentvolumeclaims"
	trainsPath    = "/apis/observatory.liken.sh/v1alpha1/opticaltrains"
	operatorImage = "ghcr.io/liken-sh/astrophotography-operator:2026.10.12-001"
)

// startOperator puts the operator's own pod in the fake API server and
// runs the operator until the test ends.
func startOperator(t *testing.T, api *fakeAPI) *operator {
	t.Helper()
	api.put(podsPath, operatorPod("operator"))
	return runOperator(t, api)
}

// operatorPod returns the operator's own pod, with one container that
// has the given name.
func operatorPod(container string) map[string]any {
	return object(operatorNS, operatorPodName, nil, map[string]any{
		"containers": []any{map[string]any{"name": container, "image": operatorImage}},
	})
}

func runOperator(t *testing.T, api *fakeAPI) *operator {
	return runOperatorUntil(t.Context(), t, api)
}

// runOperatorUntil runs the operator until ctx ends, so a test can
// restart the operator against the same API server.
func runOperatorUntil(ctx context.Context, t *testing.T, api *fakeAPI) *operator {
	client := apiclient.New(apiservertest.Host, api.server.Client(), "").WithWaitContext(ctx)
	recorder := events.New(t.Context(), client, managedBy, events.Options{Instance: operatorPodName, Log: io.Discard})
	o := newOperator(client, recorder, newMetrics("dev"), io.Discard)
	o.pod, o.namespace = operatorPodName, operatorNS
	go o.run(ctx, api.watcher(t))
	return o
}

// toMap converts a Go value to the generic map form that the fake API
// server stores.
func toMap(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding the value as JSON: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decoding the JSON into a map: %v", err)
	}
	return out
}

// m42 returns a Target that images 3 Luminance frames of 300 s and
// archives them to the claim named archive.
func m42() astrophotography.Target {
	t := astrophotography.Target{}
	t.Metadata.Name, t.Metadata.Namespace, t.Metadata.UID = "m42", observatoryNS, "uid-m42"
	t.Spec = astrophotography.TargetSpec{
		Object:    "M42",
		Sequences: []astrophotography.Sequence{{Filter: "Luminance", Exposure: "300s", Count: 3}},
		Archive: astrophotography.Archive{ClaimName: "archive", Path: "astro", Promotion: astrophotography.PromotionContinuous,
			Format: "{object}_{n}"},
	}
	return t
}

// withPending returns t with status counts for the outbox claim named
// frames: pending frames, and frames that a promotion Job left there.
func withPending(t astrophotography.Target, pending, left int32) astrophotography.Target {
	t.Status.Outboxes = []astrophotography.OutboxFrames{{ClaimName: "frames", Pending: pending, Left: left}}
	t.Status.Sequences = []astrophotography.SequenceProgress{{Sequence: t.Spec.Sequences[0], Pending: pending}}
	return t
}

func putTarget(t *testing.T, api *fakeAPI, target astrophotography.Target) {
	api.put(targetsPath, toMap(t, target))
}

func putClaim(api *fakeAPI, name string, finalizers ...any) {
	claim := object(observatoryNS, name, nil, nil)
	if len(finalizers) > 0 {
		claim["metadata"].(map[string]any)["finalizers"] = finalizers
	}
	api.put(claimsPath, claim)
}

// targetEvents returns the type and reason of each Event about the
// Target m42, in order.
func targetEvents(api *fakeAPI) []string {
	var out []string
	for _, e := range api.recorded.About("Target", "m42") {
		out = append(out, e.Type+" "+e.Reason)
	}
	return out
}

func readTarget(api *fakeAPI) astrophotography.Target {
	return decode[astrophotography.Target](api, targetsPath, observatoryNS, "m42")
}

// finish ends a Job as the Job controller does when its pod exits. The
// pod's termination message is the JSON of report. A nil report ends
// the Job as Failed and the pod writes no report.
func finish(t *testing.T, api *fakeAPI, job string, report *promote.Report) {
	t.Helper()
	stored := api.get(jobsPath, observatoryNS, job)
	if stored == nil {
		t.Fatalf("the fake API server has no Job %s", job)
	}
	status := map[string]any{"conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
	terminated := map[string]any{"exitCode": 1}
	if report == nil {
		status = map[string]any{"conditions": []any{map[string]any{"type": "Failed", "status": "True", "message": "Job has reached the specified backoff limit"}}}
	} else {
		encoded, _ := json.Marshal(report)
		terminated = map[string]any{"exitCode": 0, "message": string(encoded)}
	}
	stored["status"] = status
	api.put(jobsPath, stored)
	pod := object(observatoryNS, job+"-x7k2p", map[string]any{labelManagedBy: managedBy, labelPromotionJob: job}, nil)
	pod["status"] = map[string]any{"containerStatuses": []any{map[string]any{
		"name": promoteContainer, "state": map[string]any{"terminated": terminated},
	}}}
	api.put(podsPath, pod)
}

// complete ends a Job as Complete with no pod, as when the pod whose
// termination message had the report is deleted.
func complete(t *testing.T, api *fakeAPI, job string) {
	t.Helper()
	stored := api.get(jobsPath, observatoryNS, job)
	if stored == nil {
		t.Fatalf("the fake API server has no Job %s", job)
	}
	stored["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
	api.put(jobsPath, stored)
}
