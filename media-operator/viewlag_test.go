package main

// These tests run a pass against a view that is behind the API server.
// A watch delivers a change shortly after the write, so a pass right
// after a create or a delete can read a view without it. The pass
// decides from the view and reads the API server again before it acts,
// so a stale view causes no second create, no second delete, and no log
// line about an action that did not happen.

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

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

// The view shows the playback pod as Failed, and the API server shows it
// with a deletionTimestamp. The pass does not delete it again, and the
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

// The same holds for a Remote's reader pod.
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

// A reader pod that an older release created has no component label,
// so the pods watch does not select it. The pass adds the label to the
// running pod, and the next pass finds the pod in the view and sends no
// request.
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

func TestTheReaderPodCarriesTheComponentLabel(t *testing.T) {
	pod := buildRemotePod(houseRemote("gamepad"), buildRemoteClaim(houseRemote("gamepad")),
		"registry.example/sidecar:test", "bus.media.svc:1883", defaultTopicBase)

	mustMatch(t, pod.Metadata.Labels[playbackLabelKey], remoteLabelValue)
	mustMatch(t, strings.Contains(ownPodsSelector, remoteLabelValue), true)
}

// A Play applied in the same file as its Player can reach the view
// first, because each resource has its own watch. A run with no pod
// reads a Player that the view lacks from the API server, so the Play
// starts.
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

// The same holds for the Remote that the Player names.
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

// A pass can read a Play from the view before the view has the
// operator's own last status write. Its write then fails with a
// conflict, it reads the Play again, and it finds the status already
// written, so it does not log or count the phase change again.
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

// The pods watch does not select an idle pod that an older release
// created without the component label. When another controller draws
// the idle screen, the Player needs no idle pod, so the pass reads the
// pod from the API server and deletes it. The next pass reads the API
// server once more and finds the pod gone, and later passes send no
// request.
func TestAnUnlabeledPodThePassWantsGoneIsDeleted(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()
	claim := buildIdleClaim(player, media.idleDisplayClass)
	pod := plainIdlePod(player, claim, testBusAddress, testTopicBase, "America/New_York")
	pod.Metadata.Labels = nil
	seedPersistent(t, cluster, claim, pod)
	player.Spec.Idle = &IdlePolicy{Controller: "library.liken.sh/media-browser"}

	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))
	_, exists := cluster.pods["theater-idle"]
	mustMatch(t, exists, false)

	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))
	cluster.requests = nil
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))
	mustMatchAll(t, cluster.requests, nil)
}

// A Player edit can reach the view after the pod that the edit changed.
// The pod does not match the old Player in the view, so the pass reads
// the Player from the API server, finds that the pod matches the edit,
// and replaces nothing.
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

// A report that the old pod ended labels no pod, even while the view
// still has the old pod under the same name. The merge patch selects
// the pod by name alone, so a patch based on the view would label the
// new pod and end playback that is still running.
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
