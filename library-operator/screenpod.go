package main

// The screen pod is what a delegated Player becomes: one pod per
// Player, in the Player's namespace and owned by it, so deleting the Player
// tears it down. It holds the media browser and a Corrosion agent of its own.
//
// The agent's state is an emptyDir and not a durable claim, unlike the
// scanner pod's. A screen holds a copy of the namespace's catalog, not the
// only copy: the agent joins the cluster through the catalog Service and
// rebuilds from its peers on every start.
//
// The browser reads the catalog from the agent's file and the update
// stream from its loopback API, and it draws poster art from every Library's
// storage claim in the namespace, each mounted read-only. It draws on the
// screen media-operator claimed for the Player: the pod holds that claim, and
// the browser container takes the requests media-operator named in it.

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
)

// The container's name reaches a person through kubectl logs, so it
const browserContainer = "browser"

// The name label a screen pod carries. It is neither the scanner's
// value nor the cleanup pod's, so one list answers one kind of pod.
const screenLabelValue = "library-media-browser"

// The label that names the Player a screen pod draws for, beside the
// name label above. A person lists one Player's pod by this pair.
const playerLabelKey = "library.liken.sh/player"

// Where the operator mounts the Libraries of the namespace, one
// directory per Library under this root. The browser reads a title's poster
// from the mount its library root names.
const librariesMountPath = "/libraries"

// The pod-local name of the display claim. The pod holds
// media-operator's ResourceClaim under this name, and the browser container's
// resource claims refer to the same name.
const displayClaimName = "devices"

// The seconds the browser waits for a window before it exits 7 and the
// kubelet restarts it. The container reads the variable; a run outside a pod
// sets none and waits forever.
const (
	windowGraceVariable = "WINDOW_GRACE_SECONDS"
	windowGraceSeconds  = "15"
)

// The three variables that carry status.idle.bus into the browser.
// They are the names media-operator's own client reads, so the two
// clients of one contract are wired the same way.
const (
	mediaBusAddressVariable    = "MEDIA_BUS_ADDRESS"
	mediaCommandsTopicVariable = "MEDIA_PLAYER_COMMANDS_TOPIC"
	mediaScreenTopicVariable   = "MEDIA_PLAYER_SCREEN_TOPIC"
)

// ScreenPodName is the pod one Player becomes. The name is derived
// rather than generated, so every pass names the same pod and the operator
// needs no record of what it created.
func screenPodName(player string) string {
	return player + "-media-browser"
}

// ScreenLabels is the label pair that names one Player's screen pod.
// The pod carries them and a list of this operator's screens selects on the
// first, so the two cannot drift apart.
func screenLabels(player string) map[string]string {
	return map[string]string{
		scannerLabelKey: screenLabelValue,
		playerLabelKey:  player,
	}
}

// PlayerOwner ties the pod's life to the Player's. Controller is true
// because exactly one thing manages this pod, and the UID is what the garbage
// collector matches: a Player deleted and recreated under the same name is a
// different owner, and the old pod goes.
func playerOwner(player *Player) OwnerReference {
	return OwnerReference{
		APIVersion: playerAPIVersion,
		Kind:       "Player",
		Name:       player.Metadata.Name,
		UID:        player.Metadata.UID,
		Controller: true,
	}
}

// BuildScreenPod writes the pod one delegated Player becomes. It is a
// function of the Player, the namespace's Libraries, and the operator's own
// settings alone, so two passes over an unchanged namespace build the same
// pod, which is what makes the template hash mean anything. The Libraries are
// read in name order for the same reason.
func buildScreenPod(player *Player, libraries []Library, browserImage, corrosionImage string) *Pod {
	grace := int64(scannerGracePeriod)
	// The browser holds no Kubernetes credential. It reads the catalog
	// from the agent beside it, and nothing in the pod speaks to the API
	// server.
	noToken := false
	shown := slices.Clone(libraries)
	slices.SortFunc(shown, func(one, other Library) int {
		return strings.Compare(one.Metadata.Name, other.Metadata.Name)
	})

	volumes := []Volume{}
	for index := range shown {
		library := &shown[index]
		volumes = append(volumes, Volume{
			Name: libraryVolumeName + "-" + library.Metadata.Name,
			PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
				ClaimName: library.Spec.Storage.Claim,
				ReadOnly:  true,
			},
		})
	}
	// The agent rebuilds its catalog from its peers on every start, so
	// its state is an emptyDir and the pod provisions no claim for it.
	volumes = append(volumes, Volume{Name: catalogVolumeName, EmptyDir: &EmptyDirVolumeSource{}})

	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            screenPodName(player.Metadata.Name),
			Namespace:       player.Metadata.Namespace,
			Labels:          screenLabels(player.Metadata.Name),
			OwnerReferences: []OwnerReference{playerOwner(player)},
		},
		Spec: PodSpec{
			// A screen is a standing service, so the kubelet restarts a
			// container that exits rather than letting the pod end. That is
			// also what puts the browser back after the window watchdog exits
			// it.
			RestartPolicy:                 "Always",
			TerminationGracePeriodSeconds: &grace,
			AutomountServiceAccountToken:  &noToken,
			// The catalog agent is the same native sidecar the scanner
			// pod runs, so the kubelet passes its startupProbe before it
			// starts the browser, and the browser's first read never races an
			// API that is not listening.
			InitContainers: []Container{
				catalogSidecar(corrosionImage),
			},
			Containers: []Container{
				browserSidecar(player, shown, browserImage),
			},
			Volumes: volumes,
			// The display claim media-operator stood for this Player.
			// The pod holds it, and the browser container takes the requests
			// inside it.
			ResourceClaims: []PodResourceClaim{
				{Name: displayClaimName, ResourceClaimName: player.idle().Claim},
			},
		},
	}
}

// BrowserSidecar builds the container that draws the wall. It learns
// the catalog, the update stream, and every library root from its arguments
// alone, because it holds no API credential to look one up with. Each library
// claim is mounted read-only, so the browser cannot write to a media volume
// whatever it does.
func browserSidecar(player *Player, libraries []Library, image string) Container {
	args := []string{
		"--catalog", path.Join(catalogStatePath, catalogStateFile),
		"--updates", defaultCatalogAPI,
	}
	// The browser reads the agent's database file straight off the
	// shared volume, so the catalog volume is mounted here as well as in
	// the agent. Without this mount the path --catalog names does not
	// exist in the browser's filesystem, the open fails, and the wall
	// draws an empty library list. The mount is not read-only, because
	// SQLite in WAL mode opens a read-only connection through the -shm
	// file beside the database, and that file must be writable.
	mounts := []VolumeMount{
		{Name: catalogVolumeName, MountPath: catalogStatePath},
	}
	for index := range libraries {
		library := &libraries[index]
		mountPath := path.Join(librariesMountPath, library.Metadata.Name)
		mounts = append(mounts, VolumeMount{
			Name:      libraryVolumeName + "-" + library.Metadata.Name,
			MountPath: mountPath,
			ReadOnly:  true,
		})
		// The browser keys a title's library by namespace and name, the
		// same key the catalog rows carry, and reads that library's files
		// under the Library's own root inside the mount.
		args = append(args, "--library-root", fmt.Sprintf("%s/%s=%s",
			library.Metadata.Namespace, library.Metadata.Name,
			path.Join(mountPath, library.Spec.Storage.Root)))
	}

	claims := []ResourceClaim{}
	for _, request := range player.idle().Requests {
		claims = append(claims, ResourceClaim{Name: displayClaimName, Request: request})
	}

	environment := []EnvVar{
		{Name: windowGraceVariable, Value: windowGraceSeconds},
	}
	// A Player whose status names a bus gets the three variables, and
	// its browser takes the room's remotes. A Player under an older
	// media-operator gets none of them, and its browser opens no
	// connection and takes the keyboard alone. The template hash rolls
	// the pod when the block appears.
	if bus := player.idle().Bus; bus != nil {
		environment = append(environment,
			EnvVar{Name: mediaBusAddressVariable, Value: bus.Address},
			EnvVar{Name: mediaCommandsTopicVariable, Value: bus.CommandsTopic},
			EnvVar{Name: mediaScreenTopicVariable, Value: bus.ScreenTopic},
		)
	}

	return Container{
		Name:            browserContainer,
		Image:           image,
		Args:            args,
		Env:             environment,
		VolumeMounts:    mounts,
		Resources:       ResourceRequirements{Claims: claims},
		SecurityContext: unprivileged(),
	}
}

// ReconcileScreens brings one namespace's screen pods into line. A
// Player that names this operator as its idle controller gets a pod, and a
// Player that names another, or none, loses the pod that stands for it. That
// delete is the only one this operator sends for a screen: media-operator
// deletes the pod itself when the claim under it must be replaced, and the
// next pass creates it again.
//
// A failure on one Player is reported and the pass carries on, because
// one broken screen must not hold up another room's.
func (o *operator) reconcileScreens(ctx context.Context, namespace string, players []Player, libraries []Library, screens []Pod) {
	inNamespace := []Library{}
	for index := range libraries {
		if libraries[index].Metadata.Namespace == namespace {
			inNamespace = append(inNamespace, libraries[index])
		}
	}
	// The screen pods that stand in this namespace now, by name. A
	// Player this operator does not serve costs no request unless one
	// of them is its pod, so a cluster full of undelegated units sends
	// no delete on every pass.
	standing := map[string]bool{}
	for index := range screens {
		if screens[index].Metadata.Namespace == namespace {
			standing[screens[index].Metadata.Name] = true
		}
	}

	for index := range players {
		player := &players[index]
		if player.Metadata.Namespace != namespace {
			continue
		}
		name := player.Metadata.Name
		if !player.delegated() {
			if !standing[screenPodName(name)] {
				continue
			}
			if err := DeletePod(ctx, o.client, namespace, screenPodName(name)); err != nil {
				fmt.Fprintf(os.Stderr, "stopping the screen of %s/%s: %v\n", namespace, name, err)
			}
			continue
		}
		desired := buildScreenPod(player, inNamespace, o.browserImage, o.corrosionImage)
		if _, err := o.standPod(ctx, desired); err != nil {
			fmt.Fprintf(os.Stderr, "standing the screen of %s/%s: %v\n", namespace, name, err)
		}
	}
}

// ScreenNamespaces is every namespace that holds a Player, in name
// order. The pass reconciles the screens one namespace at a time, because a
// screen pod mounts the Libraries of its own namespace and no other.
func screenNamespaces(players []Player) []string {
	namespaces := map[string]bool{}
	for index := range players {
		namespaces[players[index].Metadata.Namespace] = true
	}
	return slices.Sorted(maps.Keys(namespaces))
}
