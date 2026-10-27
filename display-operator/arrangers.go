package main

// Who arranges each claim's region, as the Display's status shows it,
// and the Events a person reads when a claim gains or loses its
// arranger.
//
// The arranger is a client of the claim: the module reports the
// claim's socket, and the pass resolves the socket to the claim and
// the pods that hold it, the same way it resolves a surface. The
// compositor cannot name the container, because a client in another
// pod reports pid 0 across the pid namespaces, so the status names the
// pods that hold the claim. When one pod holds it, that pod is the
// arranger's.
//
// The status changes when a claim gains or loses its arranger, and
// when the name of its arrangement changes. A commit that keeps the
// name changes nothing, so the arrangements that follow key presses
// cost the API server no write unless they change the name. The
// Events of a gain and a loss are in arrangerevents.go.

import (
	"slices"
	"strings"
)

// DisplayArranger is the client that arranges one claim's surfaces
// inside its region.
type DisplayArranger struct {
	// The claim whose socket the arranger arrived on, as
	// namespace/name.
	Claim string `json:"claim"`
	// The region that shows the claim's surfaces, and empty while no
	// region shows them.
	Region string `json:"region,omitempty"`
	// The pods that hold the claim. The arranger is a process of one
	// of them.
	Pods []string `json:"pods,omitempty"`
	// The arranger's pid in the compositor's pid namespace. It is
	// absent for a client in another pod, which is every workload.
	PID int `json:"pid,omitempty"`
	// The name of the arrangement it committed last, and absent
	// before its first commit.
	Arrangement string `json:"arrangement,omitempty"`
	// Each app_id that two or more of the claim's surfaces carry. An
	// arrangement entry moves every surface whose app_id it names, so
	// these surfaces always move together.
	DuplicateAppIDs []string `json:"duplicateAppIDs,omitempty"`
}

// screenArranger is the module's report of one arranger, with the
// claim and pods its socket resolved to.
type screenArranger struct {
	layoutArranger
	claim string
	pods  []string
}

// arrangerStatus states each arranger of one screen, ordered by claim,
// with the region and the duplicate app_ids read from the surfaces
// the same status reports.
func arrangerStatus(arrangers []screenArranger, surfaces []DisplaySurface) []DisplayArranger {
	var stated []DisplayArranger
	for _, arranger := range arrangers {
		stated = append(stated, DisplayArranger{
			Claim:           arranger.claim,
			Region:          claimRegion(arranger.claim, surfaces),
			Pods:            arranger.pods,
			PID:             arranger.PID,
			Arrangement:     arranger.Arrangement,
			DuplicateAppIDs: duplicateAppIDs(arranger.claim, surfaces),
		})
	}
	slices.SortFunc(stated, func(a, b DisplayArranger) int { return strings.Compare(a.Claim, b.Claim) })
	return stated
}

// claimRegion answers the region that shows a claim's surfaces. A
// region shows every surface of the one claim it takes, so the first
// surface with a region answers for all of them.
func claimRegion(claim string, surfaces []DisplaySurface) string {
	for _, surface := range surfaces {
		if surface.Claim == claim && surface.Region != "" {
			return surface.Region
		}
	}
	return ""
}

// duplicateAppIDs answers each app_id that more than one surface of a
// claim carries, sorted. A surface with no app_id matches no entry, so
// it is not counted.
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

// The arrangers of one screen, and none for a screen with nothing on
// it.
func arrangersOn(screen *screenSurfaces) []screenArranger {
	if screen == nil {
		return nil
	}
	return screen.arrangers
}

// resolveArrangers puts each arranger the module reported on the
// screen of its claim's socket, with the claim and the pods that hold
// it, and each departure on the screen of its claim. An arranger on a
// socket no prepared claim holds belongs to a claim that was given
// back, and its region is gone, so no screen shows it. A departure
// from such a socket resolves to no claim, and its Event reads the
// pods alone.
func resolveArrangers(state layoutState, sockets map[string]preparedSocket, connectors map[string]string,
	holders *holderReader, screens map[string]*screenSurfaces) []error {
	var failures []error
	// claimOf answers the screen and the claim of one socket, or nil
	// for a socket that resolves to no claim on a screen.
	claimOf := func(name string) (*screenSurfaces, claimHolders) {
		socket, held := sockets[name]
		if !held || connectors[socket.device] == "" {
			return nil, claimHolders{}
		}
		connector := connectors[socket.device]
		if screens[connector] == nil {
			screens[connector] = &screenSurfaces{}
		}
		by, err := holders.holders(socket.claim)
		if err != nil {
			failures = append(failures, err)
			screens[connector].unresolved = true
			return nil, claimHolders{}
		}
		if by.key == "" {
			return nil, claimHolders{}
		}
		return screens[connector], by
	}
	for _, reported := range state.Arrangers {
		if screen, by := claimOf(reported.Socket); screen != nil {
			screen.arrangers = append(screen.arrangers,
				screenArranger{layoutArranger: reported, claim: by.key, pods: by.pods})
		}
	}
	for name, reason := range state.Departures {
		if screen, by := claimOf(name); screen != nil {
			if screen.departures == nil {
				screen.departures = map[string]string{}
			}
			screen.departures[by.key] = reason
		}
	}
	return failures
}

// The reason each claim of one screen last lost its arranger, by
// claim, and none for a screen with nothing on it.
func departuresOn(screen *screenSurfaces) map[string]string {
	if screen == nil {
		return nil
	}
	return screen.departures
}
