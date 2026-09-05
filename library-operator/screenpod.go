package main

// The screen pod is what a delegated Player becomes: one pod per
// Player, in the Player's namespace and owned by it, so deleting the Player
// tears it down. It holds the media browser and a Corrosion agent of its own.
//
// The agent's state is a claim of the screen's own, sized by the
// namespace Catalog, so a screen that restarts syncs a delta rather than
// pulling the whole catalog. A screen in a namespace with no single Catalog
// has no size to read, so its agent keeps an emptyDir and rebuilds from its
// peers on every start.
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
	"strconv"
	"strings"
	"time"
)

// The container's name reaches a person through kubectl logs, so it
const browserContainer = "browser"

// The name label value a screen pod carries. It is neither a
// worker Job's value nor the catalog pod's, so one list answers one
// kind of pod.
const screenLabelValue = "library-media-browser"

// The label that names the Player a screen pod draws for, beside the
// name label above. A person lists one Player's pod by this pair.
const playerLabelKey = "library.liken.sh/player"

// Where the operator mounts the Libraries of the namespace, one
// directory per Library under this root. The browser reads a title's poster
// from the mount its library root names.
const librariesMountPath = "/libraries"

// The browser keeps scaled posters on the node's local disk. The extra
// 128 MiB above the disk cache's 512 MiB cap gives atomic writes room for
// temporary files before their rename.
const (
	posterCacheVolumeName = "poster-cache"
	posterCacheMountPath  = "/var/cache/media-browser"
	posterCacheSizeLimit  = "640Mi"
)

// The pod-local name of the display claim. The pod holds
// media-operator's ResourceClaim under this name, and the browser container's
// resource claims refer to the same name.
const displayClaimName = "devices"

// The seconds the browser waits for a window before it exits 7 and the
// kubelet restarts it. The container reads the variable; a run outside a pod
// sets none and waits forever.
const (
	windowGraceVariable = "WINDOW_GRACE_SECONDS"
	// The zone the browser's clock and its day's draw read, the standard
	// name and not a LIBRARY_ one, because glibc reads it.
	timeZoneVariable   = "TZ"
	windowGraceSeconds = "15"
)

// The variables that carry status.idle into the browser. They are the
// names media-operator's own client reads, and the media-screen crate
// both clients link reads them in its wiring.rs, so the two clients of
// one contract are wired the same way. They name the broker, the
// Player's own object name that every focus mark holds, the retained
// status, the level, the commands topic that carries the re-present,
// the panel topic the client states the panel desire on, and the two
// newline-joined lists of the unit's controllers. The level variable is
// absent for a unit with no sinks.
const (
	mediaBusAddressVariable         = "MEDIA_BUS_ADDRESS"
	mediaPlayerNameVariable         = "MEDIA_PLAYER_NAME"
	mediaStatusTopicVariable        = "MEDIA_PLAYER_STATUS_TOPIC"
	mediaVolumeTopicVariable        = "MEDIA_PLAYER_VOLUME_TOPIC"
	mediaCommandsTopicVariable      = "MEDIA_PLAYER_COMMANDS_TOPIC"
	mediaPanelTopicVariable         = "MEDIA_PLAYER_PANEL_TOPIC"
	mediaRemoteEventsTopicsVariable = "MEDIA_REMOTE_EVENTS_TOPICS"
	mediaRemoteFocusTopicsVariable  = "MEDIA_REMOTE_FOCUS_TOPICS"
)

// The two windows the browser runs itself, in seconds. They are always
// set beside the bus block, because the crate holds the timers and an
// absent variable is not a policy a client can read. Zero on the fade
// means the screen never fades on its own, and zero on the off window
// leaves the panel lit.
const (
	idleFadeAfterSecondsVariable = "IDLE_FADE_AFTER_SECONDS"
	idleOffAfterSecondsVariable  = "IDLE_OFF_AFTER_SECONDS"
)

// The topic the browser publishes a play request on. It is this
// operator's own variable and not media-operator's, because this
// operator names the topic and reads it.
const libraryPlayTopicVariable = "LIBRARY_PLAY_TOPIC"

// ScreenPodName is the pod one Player becomes. The name is derived
// rather than generated, so every pass names the same pod and the operator
// needs no record of what it created.
func screenPodName(player string) string {
	return player + "-media-browser"
}

// ScreenLabels is what one Player's screen pod carries: the name
// label a list of this operator's screens selects on, the Player it
// draws for, and the member label that makes its agent a peer of the
// namespace's catalog cluster.
func screenLabels(player string) map[string]string {
	return withMemberLabel(map[string]string{
		scannerLabelKey: screenLabelValue,
		playerLabelKey:  player,
	})
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
func buildScreenPod(player *Player, libraries []Library, catalog *NamespaceCatalog, browserImage, corrosionImage, topicBase, timeZone string) *Pod {
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
	// The agent's state is the screen's own claim, which the pass
	// creates before this pod. A namespace with no single Catalog states no
	// size, so the agent takes an emptyDir and pays a full sync per start.
	volumes = append(volumes,
		screenCatalogVolume(player, catalog),
		Volume{
			Name:     posterCacheVolumeName,
			EmptyDir: &EmptyDirVolumeSource{SizeLimit: posterCacheSizeLimit},
		},
	)

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
			// The catalog agent is the same native sidecar every other
			// pod runs, so the kubelet passes its startupProbe before it
			// starts the browser, and the browser's first read never races an
			// API that is not listening.
			InitContainers: []Container{
				catalogSidecar(corrosionImage),
			},
			Containers: []Container{
				browserSidecar(player, shown, browserImage, topicBase, timeZone),
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

// The volume the catalog agent's state is on: the screen's own claim
// where the namespace holds one Catalog, and an emptyDir where it does not.
func screenCatalogVolume(player *Player, catalog *NamespaceCatalog) Volume {
	if catalog == nil {
		return Volume{Name: catalogVolumeName, EmptyDir: &EmptyDirVolumeSource{}}
	}
	return Volume{Name: catalogVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
		ClaimName: screenClaimName(player.Metadata.Name),
	}}
}

// BrowserSidecar builds the container that draws the wall. It learns
// the catalog, the update stream, and every library root from its arguments
// alone, because it holds no API credential to look one up with. Each library
// claim is mounted read-only, so the browser cannot write to a media volume
// whatever it does.
func browserSidecar(player *Player, libraries []Library, image, topicBase, timeZone string) Container {
	args := []string{
		"--catalog", path.Join(catalogStatePath, catalogStateFile),
		"--updates", defaultCatalogAPI,
		"--cache-dir", posterCacheMountPath,
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
		{Name: posterCacheVolumeName, MountPath: posterCacheMountPath},
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

	idle := player.idle()
	environment := []EnvVar{
		{Name: windowGraceVariable, Value: windowGraceSeconds},
	}
	// The clock reads TZ against the image's tz database. Set it only
	// when the household stated a zone, so an unset zone leaves the pod
	// on UTC, the way media-operator's own pods do. The template hash
	// rolls the pod when the zone changes.
	if timeZone != "" {
		environment = append(environment, EnvVar{Name: timeZoneVariable, Value: timeZone})
	}
	// A Player whose status names a bus gets the wiring, and its browser
	// takes the room's remotes. A Player under an older media-operator
	// gets none of it, and its browser opens no connection and takes the
	// keyboard alone. The template hash rolls the pod when the block
	// appears.
	if bus := idle.Bus; bus != nil {
		environment = append(environment,
			EnvVar{Name: mediaBusAddressVariable, Value: bus.Address},
			// The Player's own object name, which is what every focus
			// mark holds. A client that reads no name matches no mark
			// and answers no press.
			EnvVar{Name: mediaPlayerNameVariable, Value: player.Metadata.Name},
			EnvVar{Name: mediaStatusTopicVariable, Value: bus.StatusTopic},
		)
		// The level topic is the speaker gate as well as the address,
		// so a unit with no sinks carries no variable rather than an
		// empty one.
		if bus.VolumeTopic != "" {
			environment = append(environment,
				EnvVar{Name: mediaVolumeTopicVariable, Value: bus.VolumeTopic})
		}
		environment = append(environment,
			EnvVar{Name: mediaCommandsTopicVariable, Value: bus.CommandsTopic},
			EnvVar{Name: mediaPanelTopicVariable, Value: bus.PanelTopic},
		)
		environment = append(environment, remoteTopics(bus.Remotes)...)
		environment = append(environment,
			EnvVar{Name: idleFadeAfterSecondsVariable,
				Value: strconv.FormatInt(idle.FadeAfterSeconds, 10)},
			EnvVar{Name: idleOffAfterSecondsVariable,
				Value: strconv.FormatInt(idle.OffAfterSeconds, 10)},
			// The play topic travels with the rest, because it is the
			// same connection. A browser with no broker publishes no
			// request, so the topic alone would name nothing.
			EnvVar{Name: libraryPlayTopicVariable, Value: playRequestTopic(
				topicBase, player.Metadata.Namespace, player.Metadata.Name)},
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

// remoteTopics is the unit's controllers as the two newline-joined
// lists the crate reads. They pair by position: a line's number is the
// controller's place in spec.remotes, which is the index a focus moment
// carries, so a controller with no focus topic contributes an empty
// line rather than shifting the pairing. A unit with no controllers
// carries neither variable.
func remoteTopics(remotes []PlayerIdleRemote) []EnvVar {
	if len(remotes) == 0 {
		return nil
	}
	events := make([]string, 0, len(remotes))
	focuses := make([]string, 0, len(remotes))
	for _, remote := range remotes {
		events = append(events, remote.Events)
		focuses = append(focuses, remote.Focus)
	}
	return []EnvVar{
		{Name: mediaRemoteEventsTopicsVariable, Value: strings.Join(events, "\n")},
		{Name: mediaRemoteFocusTopicsVariable, Value: strings.Join(focuses, "\n")},
	}
}

// ReconcileScreens brings one namespace's screen pods into line. A
// Player that names this operator as its idle controller gets a claim and a
// pod, and a Player that names another, or none, loses the pod that stands
// for it.
//
// The operator sends two other deletes here. media-operator deletes
// the pod itself when the claim under it must be replaced, and the next pass
// creates it again. A screen the scheduler has refused for longer than the
// grace loses its pod and its catalog claim, which is the recovery in
// screenclaim.go.
//
// A failure on one Player is reported and the pass carries on, because
// one broken screen must not hold up another room's.
func (o *operator) reconcileScreens(ctx context.Context, namespace string, catalog *NamespaceCatalog, players []Player, libraries []Library, screens []Pod, now time.Time) {
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
	standing := map[string]*Pod{}
	for index := range screens {
		if screens[index].Metadata.Namespace == namespace {
			standing[screens[index].Metadata.Name] = &screens[index]
		}
	}

	for index := range players {
		player := &players[index]
		if player.Metadata.Namespace != namespace {
			continue
		}
		name := player.Metadata.Name
		if !player.delegated() {
			if standing[screenPodName(name)] == nil {
				continue
			}
			if err := DeletePod(ctx, o.client, namespace, screenPodName(name)); err != nil {
				fmt.Fprintf(os.Stderr, "stopping the screen of %s/%s: %v\n", namespace, name, err)
			}
			continue
		}
		// The recovery runs before the claim and the pod, because a
		// pass that has just deleted both creates them on the next one.
		recovered, err := o.recoverUnschedulableScreen(ctx, player, standing[screenPodName(name)], now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "recovering the screen of %s/%s: %v\n", namespace, name, err)
			continue
		}
		if recovered {
			continue
		}
		// The claim stands before the pod, because a pod that named a
		// claim nothing had created would sit Pending until the next pass.
		if catalog != nil {
			if err := o.standScreenClaim(ctx, player, catalog); err != nil {
				fmt.Fprintf(os.Stderr, "standing the catalog claim of the screen of %s/%s: %v\n",
					namespace, name, err)
				continue
			}
		}
		desired := buildScreenPod(player, inNamespace, catalog, o.browserImage, o.corrosionImage, o.topicBase, o.timeZone)
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
