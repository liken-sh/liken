package main

// The world of the promotion loop's tests: the operator running against
// the fake API server, and builders for the Targets, claims, Jobs, and
// pods that the loop reads.

import (
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

// startOperator runs the operator, with the fake API server holding its
// own pod, until the test ends.
func startOperator(t *testing.T, api *fakeAPI) *operator {
	t.Helper()
	api.put(podsPath, operatorPod("operator"))
	return runOperator(t, api)
}

// operatorPod is the operator's own pod, with one container of a name.
func operatorPod(container string) map[string]any {
	return object(operatorNS, operatorPodName, nil, map[string]any{
		"containers": []any{map[string]any{"name": container, "image": operatorImage}},
	})
}

// runOperator runs the operator until the test ends.
func runOperator(t *testing.T, api *fakeAPI) *operator {
	client := apiclient.New(apiservertest.Host, api.server.Client(), "").WithWaitContext(t.Context())
	recorder := events.New(t.Context(), client, managedBy, events.Options{Instance: operatorPodName, Log: io.Discard})
	o := newOperator(client, recorder, newMetrics("dev"), io.Discard)
	o.pod, o.namespace = operatorPodName, operatorNS
	go o.run(t.Context(), api.watcher(t))
	return o
}

// toMap answers a Go value as the fake API server holds an object.
func toMap(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// m42 is the drill's target: Luminance 300 s x 3 into the claim
// archive, with its frames counted in the outbox claim frames.
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

// withPending is a target whose runner counted frames in the outbox
// claim frames.
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

func readTarget(api *fakeAPI) astrophotography.Target {
	return decode[astrophotography.Target](api, targetsPath, observatoryNS, "m42")
}

// finish ends a Job, the way the Job controller does after its pod
// exits, with a pod that wrote the report as its termination message.
// A nil report ends the Job as Failed, with no report.
func finish(t *testing.T, api *fakeAPI, job string, report *promote.Report) {
	t.Helper()
	held := api.get(jobsPath, observatoryNS, job)
	if held == nil {
		t.Fatalf("no Job %s", job)
	}
	status := map[string]any{"conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
	terminated := map[string]any{"exitCode": 1}
	if report == nil {
		status = map[string]any{"conditions": []any{map[string]any{"type": "Failed", "status": "True", "message": "Job has reached the specified backoff limit"}}}
	} else {
		encoded, _ := json.Marshal(report)
		terminated = map[string]any{"exitCode": 0, "message": string(encoded)}
	}
	held["status"] = status
	api.put(jobsPath, held)
	pod := object(observatoryNS, job+"-x7k2p", map[string]any{labelManagedBy: managedBy, labelPromotionJob: job}, nil)
	pod["status"] = map[string]any{"containerStatuses": []any{map[string]any{
		"name": promoteContainer, "state": map[string]any{"terminated": terminated},
	}}}
	api.put(podsPath, pod)
}
