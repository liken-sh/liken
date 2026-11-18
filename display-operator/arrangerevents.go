package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// announceArrangers compares the arrangers before and after a status
// write that succeeded, and posts an Event for each claim that gained
// an arranger and for each claim that lost one.
//
// departures holds the reason that the compositor module reported for
// each arranger that left. A client that released its arranger posts a
// Normal Event. A client that disconnected while a pod of the claim
// still runs and is not being deleted posts a Warning, because that is
// what a crash looks like. A disconnect after the pods are gone or
// being deleted means the workload ended, which is Normal. A claim
// with no reason in departures is treated as a disconnect.
//
// The first pass on a new connection to the module posts no Event for
// a lost arranger. The compositor or this operator restarted, so every
// arranger left with the old connection, and the CompositorServing
// condition or the restart's own Event already reports that.
func (p *placementPass) announceArrangers(display *Display, before, after []DisplayArranger,
	departures map[string]string) {
	recorder := p.displays.recorder
	if recorder == nil {
		return
	}
	object := displayReference(display)
	for _, gained := range after {
		if !slices.ContainsFunc(before, sameClaim(gained.Claim)) {
			recorder.Normal(object, ArrangerGrantedReason, grantedMessage(gained))
		}
	}
	if p.reconnected {
		return
	}
	for _, lost := range before {
		if slices.ContainsFunc(after, sameClaim(lost.Claim)) {
			continue
		}
		if departures[lost.Claim] == arrangerReleased {
			recorder.Normal(object, ArrangerReleasedReason, fmt.Sprintf(
				"The arranger of claim %s released the region, so the claim's surfaces fill %s again",
				lost.Claim, regionWords(lost.Region)))
			continue
		}
		if running := p.runningHolder(lost.Pods); running != "" {
			recorder.Warning(object, ArrangerLostReason, fmt.Sprintf(
				"The arranger of claim %s disconnected while pod %s still runs, so the claim's surfaces fill %s again",
				lost.Claim, running, regionWords(lost.Region)))
			continue
		}
		recorder.Normal(object, ArrangerReleasedReason, fmt.Sprintf(
			"The arranger of claim %s ended with its pods, so the claim's surfaces fill %s again",
			lost.Claim, regionWords(lost.Region)))
	}
}

func sameClaim(claim string) func(DisplayArranger) bool {
	return func(arranger DisplayArranger) bool { return arranger.Claim == claim }
}

func grantedMessage(arranger DisplayArranger) string {
	message := "A client of claim " + arranger.Claim
	if len(arranger.Pods) > 0 {
		message += " in " + strings.Join(arranger.Pods, ", ")
	}
	if arranger.PID != 0 {
		message += fmt.Sprintf(" (pid %d)", arranger.PID)
	}
	return message + " arranges the claim's surfaces in " + regionWords(arranger.Region)
}

func regionWords(region string) string {
	if region == "" {
		return "its region"
	}
	return "region " + region
}

// runningHolder returns the first pod in pods, as namespace/name, that
// exists and is not being deleted, or "" when there is none. A pod
// that fails to read counts as not running, and the error is logged.
func (p *placementPass) runningHolder(pods []string) string {
	for _, key := range pods {
		namespace, name, _ := strings.Cut(key, "/")
		pod, err := p.stores.pod(p.client, namespace, name)
		if errors.Is(err, apiclient.ErrNotFound) {
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading pod %s for the Event of a lost arranger: %v\n", key, err)
			continue
		}
		if pod != nil && pod.Metadata.DeletionTimestamp == nil {
			return key
		}
	}
	return ""
}
