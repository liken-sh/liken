package main

// The Events a person reads when a claim gains or loses its arranger.
// The pass posts them after its status write lands, from the
// difference between the arrangers it read and the arrangers it wrote
// (screenreport.go).

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// announceArrangers posts the Events of one status write that landed:
// one for each claim that gained an arranger, and one for each claim
// that lost one.
//
// The module reports why each arranger left. A client that released
// its arranger gave the region up on purpose, which is Normal. A
// client that disconnected while a pod that holds the claim still
// runs and is not being deleted is how a crash looks, and is a
// Warning. A disconnect with the pods gone or going is a workload that
// ended, which is Normal. A departure the pass could not resolve to
// its claim reads as a disconnect.
//
// A pass on a new connection to the module posts no loss. The
// compositor or this operator restarted, every arranger went with the
// old compositor, and the CompositorServing condition or the restart's
// own Event already reports it.
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
				"the arranger of claim %s gave up the region, so the claim's surfaces return to fill %s",
				lost.Claim, regionWords(lost.Region)))
			continue
		}
		if running := p.runningHolder(lost.Pods); running != "" {
			recorder.Warning(object, ArrangerLostReason, fmt.Sprintf(
				"the arranger of claim %s disconnected while pod %s still runs, so the claim's surfaces return to fill %s",
				lost.Claim, running, regionWords(lost.Region)))
			continue
		}
		recorder.Normal(object, ArrangerReleasedReason, fmt.Sprintf(
			"the arranger of claim %s left with its pods, so the claim's surfaces return to fill %s",
			lost.Claim, regionWords(lost.Region)))
	}
}

func sameClaim(claim string) func(DisplayArranger) bool {
	return func(held DisplayArranger) bool { return held.Claim == claim }
}

func grantedMessage(arranger DisplayArranger) string {
	message := "a client of claim " + arranger.Claim
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

// runningHolder answers the first of the pods that still runs and is
// not being deleted, or "" when none does. A pod that is gone does
// not run. A pod the API server cannot answer for counts as not
// running, and the log says why.
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
