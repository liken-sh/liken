package main

// These tests cover a view that is behind the API server. A watch takes
// a change a moment after the API server writes it, so the pass that
// follows a create or a delete can read the view before the change has
// arrived. The pass decides from the view, and before it acts it reads
// the API server again, so a view that is behind never makes a second
// create, a second delete, or a line about an act that did not happen.

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// A playback pod the last pass created, which the view does not hold
// yet, is read from the API server and kept. The pass creates nothing
// and writes no line.
func TestAPlaybackPodTheViewHasNotSeenIsNotCreatedAgain(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.unseen["movie-playback"] = true
	media, log := loggingOperator(t, cluster)

	media.pass()

	mustMatch(t, countMethod(cluster.requests, "POST"), 0)
	mustMatch(t, countPathRequests(cluster.requests, "GET "+podsPath("house")+"/movie-playback"), 1)
	mustMatchAll(t, linesAbout(log, "play house/movie"), nil)
	mustMatch(t, len(media.recreateBackoff), 0)
}

// A playback pod the view still shows as Failed, which the API server
// already holds on its way out, is not replaced a second time, and the
// recreate backoff does not advance.
func TestAFailedPodTheAPIServerAlreadyReleasedIsNotReplacedAgain(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.pods["movie-playback"].Metadata.DeletionTimestamp = "2026-09-09T10:30:00Z"
	stale := runningCluster(housePlayer())
	stale.pods["movie-playback"].Status.Phase = podFailed
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.view = stale.view()

	media.pass()

	mustMatch(t, countMethod(cluster.requests, "DELETE"), 0)
	mustMatch(t, len(media.recreateBackoff), 0)
}

// A reader pod the last pass created, which the view does not hold yet,
// is read from the API server and kept, with no second create and no
// line.
func TestAStandingPodTheViewHasNotSeenIsNotCreatedAgain(t *testing.T) {
	cluster, media := settledHouse(t, make(chan struct{}, 8))
	cluster.unseen[remotePodName("sofa")] = true
	var log logBuffer
	media.log = &log
	cluster.requests = nil

	media.pass()

	mustMatch(t, countMethod(cluster.requests, "POST"), 0)
	mustMatch(t, countMethod(cluster.requests, "DELETE"), 0)
	mustMatchAll(t, linesAbout(&log, "remote house/sofa"), nil)
}

// A reader pod an older release created carries the template this pass
// builds and no component label, so the pods watch does not select it. The
// pass labels it in place and keeps it running, and the next pass finds
// it in the view.
func TestAReaderPodWithoutTheComponentLabelIsLabeledInPlace(t *testing.T) {
	cluster, media := settledHouse(t, make(chan struct{}, 8))
	delete(cluster.pods[remotePodName("sofa")].Metadata.Labels, playbackLabelKey)
	cluster.requests = nil

	media.pass()

	mustMatch(t, cluster.pods[remotePodName("sofa")].Metadata.Labels[playbackLabelKey], remoteLabelValue)
	mustMatch(t, countMethod(cluster.requests, "DELETE"), 0)
	mustMatch(t, countPathRequests(cluster.requests, "PATCH "+podsPath("house")+"/"+remotePodName("sofa")), 1)

	cluster.requests = nil
	media.pass()

	mustMatchAll(t, cluster.requests, nil)
}

// The reader pod names its component, so the operator's pod watch
// selects it beside the playback and idle pods.
func TestTheReaderPodCarriesTheComponentLabel(t *testing.T) {
	pod := buildRemotePod(houseRemote("gamepad"), buildRemoteClaim(houseRemote("gamepad")),
		"registry.example/sidecar:test", "bus.media.svc:1883", defaultTopicBase)

	mustMatch(t, pod.Metadata.Labels[playbackLabelKey], remoteLabelValue)
	mustMatch(t, strings.Contains(ownPodsSelector, remoteLabelValue), true)
}

// A Play applied in one file with its Player can reach the view first,
// because each collection has a watch of its own. A run with no pod
// reads its Player from the API server, so the Play starts instead of
// waiting on a Player the API server already holds.
func TestAPlayWhosePlayerTheViewHasNotSeenStarts(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	cluster.unseen["theater"] = true
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	_, created := cluster.pods["movie-playback"]
	mustMatch(t, created, true)
	mustMatch(t, cluster.plays["movie"].Status.Phase, phasePending)
	mustMatch(t, cluster.plays["movie"].Status.Message, "")
}

// The same holds for a Remote the Player names: the run reads it from
// the API server, so the Play does not fail for a Remote that exists.
func TestAPlayWhoseRemoteTheViewHasNotSeenStarts(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	cluster.unseen["sofa"] = true
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	_, created := cluster.pods["movie-playback"]
	mustMatch(t, created, true)
	mustMatch(t, cluster.plays["movie"].Status.Phase, phasePending)
}

// A pass that reads a Play one write behind, before this operator's own
// status write has reached the view, meets a conflict, reads the Play
// again, and finds the status already written. It neither logs the
// phase change a second time nor counts a second start.
func TestAPassOneWriteBehindLogsAndCountsNoPhaseChange(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.plays["movie"].Metadata.Finalizers = []string{playFinalizer}
	cluster.plays["movie"].Metadata.ResourceVersion = "10"
	stale := runningCluster(housePlayer())
	stale.plays["movie"].Metadata.Finalizers = []string{playFinalizer}
	stale.plays["movie"].Status.Phase = phasePending
	media, log := loggingOperator(t, cluster)
	media.view = stale.view()
	media.metrics = newMediaMetrics("test")

	media.pass()

	mustMatchAll(t, linesAbout(log, "play house/movie"), nil)
	mustMatch(t, testutil.ToFloat64(media.metrics.playbackStarts), 0.0)
	mustMatch(t, cluster.plays["movie"].Status.Phase, phaseRunning)
}

// An idle pod an older release created without the component label is
// one the pods watch does not select. When the pass wants no idle pod,
// because a delegate draws the idle screen, it reads the pod from the
// API server and deletes it. The next pass reads it once more and finds
// it gone, and every pass after that reads the view alone.
func TestAnUnlabeledPodThePassWantsGoneIsDeleted(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := standingIdlePlayer()
	claim := buildIdleClaim(player, media.idleDisplayClass)
	pod := plainIdlePod(player, claim, testBusAddress, testTopicBase, "America/New_York")
	pod.Metadata.Labels = nil
	seedStanding(t, cluster, claim, pod)
	player.Spec.Idle = &IdlePolicy{Controller: "library.liken.sh/media-browser"}

	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))
	_, stands := cluster.pods["theater-idle"]
	mustMatch(t, stands, false)

	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))
	cluster.requests = nil
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))
	mustMatchAll(t, cluster.requests, nil)
}

// A Player edit can reach the view after the pod the edit reshaped. A
// pass that reads the old Player from the view finds the pod diverged,
// reads the Player again from the API server, finds the pod already
// built from the edit, and replaces nothing.
func TestAPodBuiltFromAPlayerEditTheViewHasNotSeenIsKept(t *testing.T) {
	cluster := runningCluster(brightPlayer())
	stale := runningCluster(housePlayer())
	stale.pods = cluster.pods
	stale.claims = cluster.claims
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.view = stale.view()

	media.pass()

	mustMatch(t, countMethod(cluster.requests, "DELETE"), 0)
	mustMatch(t, countMethod(cluster.requests, "POST"), 0)
	mustMatch(t, countPathRequests(cluster.requests, "GET "+playerPath("house", "theater")), 1)
}

// An ending that names the pod the pass replaced labels nothing, even
// while the view still holds that pod under the name. The merge patch
// names the pod by its name alone, so a patch on the view's evidence
// would label the new pod and fade a film that plays.
func TestAnEndingOfAReplacedPodLabelsNothingWhileTheViewLags(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.pods["movie-playback"].Metadata.UID = "new-pod"
	stale := runningCluster(housePlayer())
	stale.pods["movie-playback"].Metadata.UID = "old-pod"
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.view = stale.view()
	media.reports.readPodsFrom(media.view)

	media.reports.fold("house", "movie", playReport{Item: 1, Position: "1:58:03", Ended: true, Pod: "old-pod"})
	media.pass()

	mustMatch(t, podPatches(cluster, "movie-playback"), 0)
	_, labeled := cluster.pods["movie-playback"].Metadata.Labels[endingLabelKey]
	mustMatch(t, labeled, false)
}
