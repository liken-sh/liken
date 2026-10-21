package main

// The Disruptions that request-reboot and approve-reboot write beside
// the annotations. A Disruption is the request as `kubectl get
// disruptions` lists it, and the annotation is the request as a
// machine-operator of the release before the Disruptions reads it, so
// each command writes both, and a machine on either release acts on
// the request.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// requesterFallback names the person when the API server does not say
// who the credential belongs to.
const requesterFallback = "liken-cli"

// whoAmI answers the Kubernetes user name of the credential, from a
// SelfSubjectReview, the request behind `kubectl auth whoami`.
func whoAmI(c *apiclient.Client) string {
	body := []byte(`{"apiVersion":"authentication.k8s.io/v1","kind":"SelfSubjectReview"}`)
	var review struct {
		Status struct {
			UserInfo struct {
				Username string `json:"username"`
			} `json:"userInfo"`
		} `json:"status"`
	}
	err := c.RequestJSON(http.MethodPost, "/apis/authentication.k8s.io/v1/selfsubjectreviews", body, &review)
	if err != nil || review.Status.UserInfo.Username == "" {
		return requesterFallback
	}
	return review.Status.UserInfo.Username
}

// fileRebootRequest creates a person's request to reboot the running
// boot. A request for the same boot already exists when the command
// ran before, and that is the same request. A cluster that does not
// serve the kind answers nothing filed, and the annotation alone
// carries the request.
func fileRebootRequest(c *apiclient.Client, m *machine.Machine, bootID string) (string, error) {
	name := disruption.RebootRequestName(m.Metadata.Name, bootID)
	request := disruption.New(name, m, disruption.Spec{
		Machine:   m.Metadata.Name,
		Requester: whoAmI(c),
		Kind:      disruption.KindReboot,
		Reason:    "a reboot that a person asked for, with no change to apply",
		Hash:      bootID,
	})
	body, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	err = c.RequestJSON(http.MethodPost, kubernetes.DisruptionsPath, body, nil)
	switch {
	case err == nil, errors.Is(err, apiclient.ErrConflict):
		return name, nil
	case errors.Is(err, apiclient.ErrNotFound):
		return "", nil
	}
	return "", err
}

// approveRequest sets approvedHash on the request that covers the
// chosen pending entry: a person's request for a reboot of the running
// boot, or machine-operator's own request for every staged document.
// It answers the request's name, or nothing when the machine has no
// such request, such as a machine-operator of the release before the
// Disruptions.
//
// cluster-operator writes each request's status, and every status
// write moves the request's resourceVersion, so a write from an older
// read gets 409. The command then reads the request again and writes
// once more, a few times.
func approveRequest(c *apiclient.Client, m *machine.Machine, choice *machine.PendingDisruption) (string, error) {
	path := kubernetes.DisruptionsPath + "?fieldSelector=" + url.QueryEscape("spec.machine="+m.Metadata.Name)
	requests, err := kubernetes.List[disruption.Disruption](c, path)
	if errors.Is(err, apiclient.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	at := slices.IndexFunc(requests, func(d disruption.Disruption) bool { return coversChoice(&d, choice) })
	if at < 0 {
		return "", nil
	}
	name := requests[at].Metadata.Name
	for range approveAttempts {
		d, err := apiclient.Get[disruption.Disruption](c, kubernetes.DisruptionsPath+"/"+name)
		if err != nil {
			return "", err
		}
		if !coversChoice(d, choice) {
			return "", nil
		}
		d.Spec.ApprovedHash = d.Spec.Hash
		body, err := json.Marshal(d)
		if err != nil {
			return "", err
		}
		err = c.RequestJSON(http.MethodPut, kubernetes.DisruptionsPath+"/"+name, body, nil)
		if !errors.Is(err, apiclient.ErrConflict) {
			return name, err
		}
	}
	return "", fmt.Errorf("the Disruption %s changed on each of %d writes", name, approveAttempts)
}

// approveAttempts bounds the writes of one approval.
const approveAttempts = 5

// coversChoice reports whether a request covers the pending entry a
// person approves.
func coversChoice(d *disruption.Disruption, choice *machine.PendingDisruption) bool {
	if d.Spec.Hash == "" {
		return false
	}
	if choice.Condition == rebootRequestCondition {
		return !d.Own() && d.Spec.Kind == disruption.KindReboot && machine.RebootRequestNames(d.Spec.Hash, choice.Hash)
	}
	return d.Own()
}

// rebootRequestCondition is the condition machine-operator reports a
// person's reboot request on, in status.pending.
const rebootRequestCondition = "RebootRequestHonored"
