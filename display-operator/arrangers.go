package main

// The arranger of a claim is the client that places that claim's
// surfaces inside the claim's region. This file builds the arranger
// entries of the Display status. arrangerevents.go posts the Events for
// a claim that gains or loses its arranger.
//
// The layout module reports the claim socket that the arranger
// connected on, and the placement pass resolves the socket to a claim
// and the pods that hold it. The status lists those pods and does not
// name the arranger's container, because the compositor reads pid 0
// for a client in another pod's pid namespace.
//
// A commit with the same arrangement name leaves the status unchanged,
// so a client that commits on each key press causes no write to the
// API server.

import (
	"slices"
	"strings"
)

// DisplayArranger is the client that places one claim's surfaces inside
// the claim's region.
type DisplayArranger struct {
	// The claim whose socket the arranger connected on, as
	// namespace/name.
	Claim string `json:"claim"`
	// The region that shows the claim's surfaces. It is empty while no
	// region shows them.
	Region string `json:"region,omitempty"`
	// The pods that hold the claim. The arranger runs in one of them.
	Pods []string `json:"pods,omitempty"`
	// The arranger's pid in the compositor's pid namespace. It is
	// absent for a client in another pod, which is every workload pod.
	PID int `json:"pid,omitempty"`
	// The name of the arrangement it committed last. It is absent
	// before the first commit.
	Arrangement string `json:"arrangement,omitempty"`
	// Each app_id that two or more of the claim's surfaces have. An
	// arrangement entry applies to every surface with the app_id it
	// names, so surfaces that share an app_id cannot be placed
	// separately.
	DuplicateAppIDs []string `json:"duplicateAppIDs,omitempty"`
}

// screenArranger is an arranger that the layout module reported, with
// the claim and the pods that its socket resolved to.
type screenArranger struct {
	layoutArranger
	claim string
	pods  []string
}

// arrangerStatus returns the status entry of each arranger on one
// screen, sorted by claim. It reads each entry's region and duplicate
// app_ids from the surfaces in the same status.
func arrangerStatus(arrangers []screenArranger, surfaces []DisplaySurface) []DisplayArranger {
	var entries []DisplayArranger
	for _, arranger := range arrangers {
		entries = append(entries, DisplayArranger{
			Claim:           arranger.claim,
			Region:          claimRegion(arranger.claim, surfaces),
			Pods:            arranger.pods,
			PID:             arranger.PID,
			Arrangement:     arranger.Arrangement,
			DuplicateAppIDs: duplicateAppIDs(arranger.claim, surfaces),
		})
	}
	slices.SortFunc(entries, func(a, b DisplayArranger) int { return strings.Compare(a.Claim, b.Claim) })
	return entries
}

// claimRegion returns the region that shows a claim's surfaces. A
// region shows all the surfaces of one claim, so the first surface with
// a region is enough.
func claimRegion(claim string, surfaces []DisplaySurface) string {
	for _, surface := range surfaces {
		if surface.Claim == claim && surface.Region != "" {
			return surface.Region
		}
	}
	return ""
}

// duplicateAppIDs returns each app_id that more than one surface of the
// claim has, sorted. Surfaces with no app_id are skipped, because no
// arrangement entry can match them.
func duplicateAppIDs(claim string, surfaces []DisplaySurface) []string {
	counts := map[string]int{}
	for _, surface := range surfaces {
		if surface.Claim == claim && surface.AppID != "" {
			counts[surface.AppID]++
		}
	}
	var duplicates []string
	for appID, count := range counts {
		if count > 1 {
			duplicates = append(duplicates, appID)
		}
	}
	slices.Sort(duplicates)
	return duplicates
}

func arrangersOn(screen *screenSurfaces) []screenArranger {
	if screen == nil {
		return nil
	}
	return screen.arrangers
}

// resolveArrangers adds each arranger and each departure that the
// layout module reported to the screen of its socket, keyed by the
// claim that holds the socket. A socket that no prepared claim holds
// belongs to an unprepared claim, whose region no longer exists, so the
// pass drops its arranger and its departure. announceArrangers then
// treats that claim's loss as a disconnect.
func resolveArrangers(state layoutState, sockets map[string]preparedSocket, connectors map[string]string,
	holders *holderReader, screens map[string]*screenSurfaces) []error {
	var failures []error
	// claimOf returns a nil screen when the socket does not resolve to
	// a claim on a screen.
	claimOf := func(name string) (*screenSurfaces, claimHolders) {
		socket, held := sockets[name]
		if !held || connectors[socket.device] == "" {
			return nil, claimHolders{}
		}
		connector := connectors[socket.device]
		if screens[connector] == nil {
			screens[connector] = &screenSurfaces{}
		}
		holder, err := holders.holders(socket.claim)
		if err != nil {
			failures = append(failures, err)
			screens[connector].unresolved = true
			return nil, claimHolders{}
		}
		if holder.key == "" {
			return nil, claimHolders{}
		}
		return screens[connector], holder
	}
	for _, reported := range state.Arrangers {
		if screen, holder := claimOf(reported.Socket); screen != nil {
			screen.arrangers = append(screen.arrangers,
				screenArranger{layoutArranger: reported, claim: holder.key, pods: holder.pods})
		}
	}
	for name, reason := range state.Departures {
		if screen, holder := claimOf(name); screen != nil {
			if screen.departures == nil {
				screen.departures = map[string]string{}
			}
			screen.departures[holder.key] = reason
		}
	}
	return failures
}

// departuresOn returns why each claim on a screen last lost its
// arranger, keyed by claim.
func departuresOn(screen *screenSurfaces) map[string]string {
	if screen == nil {
		return nil
	}
	return screen.departures
}
