package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	k8stesting "k8s.io/client-go/testing"
)

// demandPull annotates the PersistentVolume, which is the one action
// that demands a pull.
func demandPull(t *testing.T, answering *node, name, at string) {
	t.Helper()
	volumes := cluster(t, answering).CoreV1().PersistentVolumes()
	held, err := volumes.Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the PersistentVolume: %v", err)
	}
	if held.Annotations == nil {
		held.Annotations = map[string]string{}
	}
	held.Annotations[demandAnnotation] = at
	if _, err := volumes.Update(t.Context(), held, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("annotating the PersistentVolume: %v", err)
	}
}

// waitForCommit waits until the volume's tree stands on the commit,
// and fails at the deadline.
func waitForCommit(t *testing.T, held *volume, want string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if commit, _ := held.condition(); commit == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	commit, trouble := held.condition()
	t.Fatalf("the volume is on %s (%q) within 30s, want %s", commit, trouble, want)
}

// demandedVolume is one read-only claim of the URL with the pull it
// names, published to one pod, with the PersistentVolume a demand is
// written on.
func demandedVolume(t *testing.T, answering *node, id, url, pull string) *volume {
	t.Helper()
	claimedVolume(t, answering, id)
	staged, held := stagedReadOnly(t, answering, id, url, map[string]string{"pull": pull})
	publishedTo(t, answering, staged, "reader")
	return held
}

// claimedVolume writes the PersistentVolume that carries the handle,
// bound to a claim of the same name.
func claimedVolume(t *testing.T, answering *node, id string) {
	t.Helper()
	held := csiVolume(id, driverName)
	held.Spec.ClaimRef = &corev1.ObjectReference{Namespace: "home", Name: id}
	client := cluster(t, answering)
	if _, err := client.CoreV1().PersistentVolumes().
		Create(t.Context(), held, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the PersistentVolume: %v", err)
	}
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: id},
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("home").
		Create(t.Context(), claim, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the claim: %v", err)
	}
}

// watchDemands starts the one watch the node holds, for the test's
// own run.
func watchDemands(t *testing.T, answering *node) {
	t.Helper()
	go answering.demands.follow(t.Context())
}

func TestAnAnnotationOnThePersistentVolumePullsTheTree(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")
	watchDemands(t, answering)

	want := commitFiles(t, source, map[string]string{"a.txt": "two"})
	demandPull(t, answering, "franchises", "2026-09-06T14:31:07Z")

	waitForCommit(t, held, want)
	if got := readTree(t, held.tree); !sameTree(got, map[string]string{"a.txt": "two"}) {
		t.Errorf("the tree holds %v, want the commit the demand pulled", got)
	}
}

func TestAVolumeThatPullsNeverTakesNoDemand(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	pinned := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	moved := repositoryWithACommit(t, map[string]string{"b.txt": "one"})
	held := demandedVolume(t, answering, "pinned", fileURL(pinned), "never")
	moving := demandedVolume(t, answering, "moving", fileURL(moved), "on-demand")
	standing, _ := held.condition()
	watchDemands(t, answering)

	commitFiles(t, pinned, map[string]string{"a.txt": "two"})
	want := commitFiles(t, moved, map[string]string{"b.txt": "two"})
	demandPull(t, answering, "pinned", "2026-09-06T14:31:07Z")
	demandPull(t, answering, "moving", "2026-09-06T14:31:07Z")

	// The volume that pulled is the evidence that the demand on the
	// pinned volume was read and did nothing.
	waitForCommit(t, moving, want)
	if commit, _ := held.condition(); commit != standing {
		t.Errorf("the pinned volume moved to %s, want %s", commit, standing)
	}
}

func TestADemandOnAPinnedVolumeMovesNoVolumeOfTheSameRepository(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	pinned := demandedVolume(t, answering, "pinned", url, "never")
	moving := demandedVolume(t, answering, "moving", url, "on-demand")
	standing, _ := moving.condition()
	watchDemands(t, answering)

	want := commitFiles(t, source, map[string]string{"a.txt": "two"})
	demandPull(t, answering, "pinned", "2026-09-06T14:31:07Z")

	// The watch reads the demand on the pinned volume on every pass,
	// and the volume that shares the repository has to keep the commit
	// it staged through all of them.
	deadline := time.Now().Add(20 * answering.demands.resync)
	for time.Now().Before(deadline) {
		if commit, _ := moving.condition(); commit != standing {
			t.Fatalf("a demand on the pinned volume moved %s to %s", moving.id, commit)
		}
		time.Sleep(10 * time.Millisecond)
	}

	demandPull(t, answering, "moving", "2026-09-06T14:31:08Z")
	waitForCommit(t, moving, want)
	if commit, _ := pinned.condition(); commit != standing {
		t.Errorf("the pinned volume moved to %s, want %s", commit, standing)
	}
}

func TestADemandOnAVolumeWhoseLoopIsGoneDoesNothing(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.demand(&volume{
		id: "franchises",
		attributes: &attributes{
			url:  "file:///gone",
			pull: pullPolicy{mode: pullOnDemand},
		},
	})
}

func TestADemandOnAWriteableVolumeDoesNothingAndSaysSoOnce(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	source := bareRemote(t, map[string]string{"a.txt": "one"})
	boundVolume(t, answering, "config", "")
	held, _ := stagedWriteable(t, answering, "config", fileURL(source))
	standing, _ := held.condition()
	watchDemands(t, answering)

	demandPull(t, answering, "config", "2026-09-06T14:31:07Z")

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), "the demand did nothing") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The watch reads the volume again on every resync, and one demand
	// is acted on once.
	time.Sleep(10 * answering.demands.resync)
	if got := strings.Count(logs.String(), "the demand did nothing"); got != 1 {
		t.Errorf("the log says the demand did nothing %d times, want 1 (%q)", got, logs)
	}
	if commit, _ := held.condition(); commit != standing {
		t.Errorf("the writeable volume moved to %s, want %s", commit, standing)
	}
}

func TestABurstOfDemandsCostsOnePullPerInterval(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.demandMin = time.Second
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")
	watchDemands(t, answering)

	first := commitFiles(t, source, map[string]string{"a.txt": "two"})
	demandPull(t, answering, "franchises", "2026-09-06T14:31:07Z")
	waitForCommit(t, held, first)

	second := commitFiles(t, source, map[string]string{"a.txt": "three"})
	for demand := range 20 {
		demandPull(t, answering, "franchises",
			time.Unix(int64(demand), 0).UTC().Format(time.RFC3339))
	}

	waitForCommit(t, held, second)
	counted, found := demandedOf(t, answering.readings, "home", "franchises")
	if !found || counted != 2 {
		t.Errorf("21 demands counted %v pulls (found: %v), want 2", counted, found)
	}
}

// demandedOf is what git_csi_demanded_pulls_total reads for the
// volume, and false when the volume is on no counter.
func demandedOf(t *testing.T, readings *metrics, namespace, id string) (float64, bool) {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "git_csi_demanded_pulls_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["namespace"] == namespace && labels["volume"] == id {
				return metric.GetCounter().GetValue(), true
			}
		}
	}
	return 0, false
}

func TestTheReportCarriesTheLastDemandAndTheLastPull(t *testing.T) {
	demanded := time.Unix(1757000000, 0).UTC()
	held := &volume{attributes: &attributes{ref: "main"}, commit: "d633176146e997"}

	if _, message := held.report(); message != "main at d633176" {
		t.Errorf("a volume nothing demanded reports %q, want the commit alone", message)
	}
	held.reportDemanded(demanded)
	held.reportPulled(demanded.Add(time.Second))

	_, message := held.report()
	want := "main at d633176, demanded 2025-09-04T15:33:20Z, pulled 2025-09-04T15:33:21Z"
	if message != want {
		t.Errorf("the report says %q, want %q", message, want)
	}
}

func TestARestartPullsEveryVolumeThatDoesNotPullNever(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")

	want := commitFiles(t, source, map[string]string{"a.txt": "two"})
	again := restarted(t, answering, true)
	again.mu.Lock()
	resumed := again.staged["franchises"]
	again.mu.Unlock()

	waitForCommit(t, resumed, want)
}

// csiVolume is a PersistentVolume of the driver it names, with the
// handle as its name.
func csiVolume(handle, driver string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: handle},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       driver,
					VolumeHandle: handle,
				},
			},
		},
	}
}

// annotated is the PersistentVolume with the demand written on it.
func annotated(held *corev1.PersistentVolume, at string) *corev1.PersistentVolume {
	held.Annotations = map[string]string{demandAnnotation: at}
	return held
}

func TestADemandTheNodeCannotActOnDoesNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		held *corev1.PersistentVolume
	}{
		{
			name: "a PersistentVolume of another driver",
			held: annotated(csiVolume("franchises", "other.example.com"), "2026-09-06T14:31:07Z"),
		},
		{
			name: "a PersistentVolume of no CSI driver",
			held: annotated(&corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "franchises"},
			}, "2026-09-06T14:31:07Z"),
		},
		{
			name: "a PersistentVolume nothing demanded",
			held: csiVolume("franchises", driverName),
		},
		{
			name: "a handle this node does not hold",
			held: annotated(csiVolume("other", driverName), "2026-09-06T14:31:07Z"),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")
			standing, _ := held.condition()

			commitFiles(t, source, map[string]string{"a.txt": "two"})
			answering.demands.read(t.Context(), c.held)

			if commit, _ := held.condition(); commit != standing {
				t.Errorf("the volume moved to %s, want %s", commit, standing)
			}
		})
	}
}

func TestTheDemandPassRestsWhenTheClusterRefusesTheVolumes(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, answering *node)
		says  string
	}{
		{
			name: "a list it refuses",
			stand: func(t *testing.T, answering *node) {
				cluster(t, answering).PrependReactor("list", "persistentvolumes",
					func(k8stesting.Action) (bool, runtime.Object, error) {
						return true, nil, errors.New("the api server said no")
					})
			},
			says: "the volumes were not listed",
		},
		{
			name: "a watch it refuses",
			stand: func(t *testing.T, answering *node) {
				cluster(t, answering).PrependWatchReactor("persistentvolumes",
					func(k8stesting.Action) (bool, watch.Interface, error) {
						return true, nil, errors.New("the api server said no")
					})
			},
			says: "the volumes are not watched",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := &logbook{}
			answering, _ := testNode(t, logs)
			c.stand(t, answering)

			answering.demands.pass(t.Context())
			if !strings.Contains(logs.String(), c.says) {
				t.Errorf("the log is %q, want %q in it", logs, c.says)
			}
		})
	}
}

func TestTheDemandPassEndsWhenTheWatchEnds(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.demands.resync = 30 * time.Second
	ended := watch.NewFake()
	cluster(t, answering).PrependWatchReactor("persistentvolumes",
		func(k8stesting.Action) (bool, watch.Interface, error) {
			return true, ended, nil
		})

	over := make(chan struct{})
	go func() {
		defer close(over)
		answering.demands.pass(t.Context())
	}()
	// An object of another kind reaches the channel, and the pass reads
	// past it.
	ended.Add(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "reader"}})
	ended.Stop()
	select {
	case <-over:
	case <-time.After(30 * time.Second):
		t.Fatal("the pass did not end when the watch did")
	}
}

func TestTheDemandPassStartsAgainOnTheResync(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.demands.resync = 10 * time.Millisecond
	cluster(t, answering).PrependWatchReactor("persistentvolumes",
		func(k8stesting.Action) (bool, watch.Interface, error) {
			return true, watch.NewFake(), nil
		})

	over := make(chan struct{})
	go func() {
		defer close(over)
		answering.demands.pass(t.Context())
	}()
	select {
	case <-over:
	case <-time.After(30 * time.Second):
		t.Fatal("the pass did not start again on the resync")
	}
}

func TestTheDemandWatchEndsWithTheDriver(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.demands.resync = 30 * time.Second
	cluster(t, answering).PrependWatchReactor("persistentvolumes",
		func(k8stesting.Action) (bool, watch.Interface, error) {
			return true, watch.NewFake(), nil
		})

	ctx, stop := context.WithCancel(t.Context())
	over := make(chan struct{})
	go func() {
		defer close(over)
		answering.demands.follow(ctx)
	}()
	stop()
	select {
	case <-over:
	case <-time.After(30 * time.Second):
		t.Fatal("the watch did not end with the driver")
	}
}

func TestADriverOutsideAClusterWatchesNothing(t *testing.T) {
	outside := &demanding{}
	outside.follow(t.Context())
}
