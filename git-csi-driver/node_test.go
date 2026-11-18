package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	kevents "github.com/liken-sh/liken/kubernetes/events"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testNode returns a node for tests. It records mounts instead of
// making them, because a test process may not mount. It posts Events to
// a fake API server and reads claims through a client-go fake.
//
// The quiesce and the sweep are short, so a test that writes in a tree
// advances the synctest clock by one sweep before the driver scans it.
func testNode(t testing.TB, logs io.Writer) (*node, *recordedMounts) {
	t.Helper()
	calls := &recordedMounts{}
	n := newNode(
		t.Context(),
		&config{
			nodeID:    "node-1",
			store:     filepath.Join(t.TempDir(), "store"),
			demandMin: defaultDemandMin,
		},
		fakeEvents(t, logs),
		newMetrics(),
		slog.New(slog.NewTextHandler(logs, nil)),
	)
	n.mounts = calls
	// The watch loops run on the test's context. Go cancels that context
	// before cleanups run, but does not wait for the loops to end. A loop
	// that still runs git in the store makes the removal of the store's
	// directory fail. Cleanups run in reverse order, so this one runs
	// before that removal and waits for every watch to end.
	t.Cleanup(func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		for _, w := range n.watchers {
			n.unwatch(w.volume)
		}
	})
	// Fail the inotify setup, so the sweep alone detects changes, as on a
	// node whose kernel refuses the watch. A goroutine that reads an
	// inotify file is not durably blocked, so the synctest clock would
	// not advance while it runs. watch_inotify_test.go tests the real
	// inotify watch outside a synctest bubble.
	n.inotify = func(int) (int, error) { return 0, errors.New("no inotify in a test") }
	n.quiesce = 20 * time.Millisecond
	n.sweep = 100 * time.Millisecond
	n.arming.retry = 20 * time.Millisecond
	// Register the volume count metric as newServer does, so a test can
	// read it the way a scrape does.
	n.metrics.registerNodeFacts(n.volumesByRepo)
	return n, calls
}

// publishRequest returns the NodePublishVolume request that the kubelet
// makes for an inline volume of the URL.
func publishRequest(t testing.TB, id, url string, extra map[string]string) *csi.NodePublishVolumeRequest {
	t.Helper()
	context := map[string]string{"url": url}
	for key, value := range extra {
		context[key] = value
	}
	request := inlineRequest(context)
	request.VolumeId = id
	request.TargetPath = filepath.Join(t.TempDir(), "mount")
	return request
}

// eventsOf returns every Event the node posted, newest last, after the
// recorder has written its queue. It must run in a synctest bubble.
func eventsOf(t *testing.T, n *node) []kevents.Event {
	t.Helper()
	return postedEvents(t, n.events)
}

func TestNodeGetInfoNamesTheNode(t *testing.T) {
	client := csi.NewNodeClient(startServer(t, io.Discard))
	info, err := client.NodeGetInfo(t.Context(), &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatalf("NodeGetInfo: %v", err)
	}
	if info.GetNodeId() != "node-1" {
		t.Errorf("NodeGetInfo returned %q, want %q", info.GetNodeId(), "node-1")
	}
	if info.GetAccessibleTopology() != nil {
		t.Errorf("NodeGetInfo returned topology %v, want none", info.GetAccessibleTopology())
	}
}

func TestNodeGetCapabilitiesDeclaresTheThree(t *testing.T) {
	client := csi.NewNodeClient(startServer(t, io.Discard))
	response, err := client.NodeGetCapabilities(t.Context(), &csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("NodeGetCapabilities: %v", err)
	}
	var got []csi.NodeServiceCapability_RPC_Type
	for _, capability := range response.GetCapabilities() {
		got = append(got, capability.GetRpc().GetType())
	}
	want := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	}
	if len(got) != len(want) {
		t.Fatalf("NodeGetCapabilities returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("NodeGetCapabilities returned %v, want %v", got, want)
		}
	}
}

func TestACallTheDriverDoesNotImplementReturnsUnimplemented(t *testing.T) {
	client := csi.NewNodeClient(startServer(t, io.Discard))
	_, err := client.NodeExpandVolume(t.Context(), &csi.NodeExpandVolumeRequest{})
	if got := status.Code(err); got != codes.Unimplemented {
		t.Fatalf("NodeExpandVolume returned %v, want %v", got, codes.Unimplemented)
	}
	want := "NodeExpandVolume: a git volume has no size to expand"
	if got := status.Convert(err).Message(); got != want {
		t.Errorf("NodeExpandVolume returned the message %q, want %q", got, want)
	}
}

func TestNodePublishVolumeRefusesACallThatNamesTooLittle(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	for _, c := range []struct {
		name        string
		request     *csi.NodePublishVolumeRequest
		wantMessage string
	}{
		{
			name:        "no volume",
			request:     &csi.NodePublishVolumeRequest{TargetPath: "/mount"},
			wantMessage: "volume_id is empty",
		},
		{
			name:        "a volume id that is a path",
			request:     &csi.NodePublishVolumeRequest{VolumeId: "../escape", TargetPath: "/mount"},
			wantMessage: "volume_id: a volume id is one path element",
		},
		{
			name:        "no target path",
			request:     &csi.NodePublishVolumeRequest{VolumeId: "csi-1"},
			wantMessage: "target_path is empty",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := n.NodePublishVolume(t.Context(), c.request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("NodePublishVolume returned %v, want %v", got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.wantMessage {
				t.Errorf("NodePublishVolume returned the message %q, want %q", got, c.wantMessage)
			}
		})
	}
}

func TestNodePublishVolumePostsARefusalOnThePod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		request := publishRequest(t, "csi-1", "file:///nowhere", map[string]string{"branch": "main"})
		if _, err := n.NodePublishVolume(t.Context(), request); err == nil {
			t.Fatal("NodePublishVolume returned no error for an unknown attribute")
		}
		posted := eventsOf(t, n)
		if len(posted) != 1 {
			t.Fatalf("NodePublishVolume posted %d events, want 1", len(posted))
		}
		if posted[0].Reason != reasonRefused || !strings.Contains(posted[0].Message, "branch") {
			t.Errorf("the event is %q with the message %q, want reason %q and a message that mentions the branch", posted[0].Reason, posted[0].Message, reasonRefused)
		}
		if posted[0].InvolvedObject.Name != "reader" {
			t.Errorf("the event is on %q, want the pod", posted[0].InvolvedObject.Name)
		}
	})
}

// The kubelet retries a refused mount with a backoff that grows to
// about two minutes, so a pod whose volume the driver refuses for an
// hour is refused about 37 times. The Event recorder merges the repeats
// into the first Event and counts them, so the pod has one Event.
func TestAnHourOfRefusalsIsOneEventWithACount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		request := publishRequest(t, "csi-1", "file:///nowhere", map[string]string{"branch": "main"})
		for range 37 {
			if _, err := n.NodePublishVolume(t.Context(), request); err == nil {
				t.Fatal("NodePublishVolume returned no error for an unknown attribute")
			}
			time.Sleep(97 * time.Second)
		}

		posted := eventsOf(t, n)
		if len(posted) != 1 || posted[0].Reason != reasonRefused || posted[0].Count != 37 {
			t.Errorf("an hour of refusals posted %+v, want one %s Event with a count of 37",
				posted, reasonRefused)
		}
	})
}

func TestNodePublishVolumeRefusesAVolumeItNeverStaged(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	request := publishRequest(t, "csi-1", "file:///nowhere", nil)
	delete(request.VolumeContext, ephemeralKey)
	_, err := n.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("NodePublishVolume returned %v, want %v", got, codes.FailedPrecondition)
	}
	if got := status.Convert(err).Message(); got != "volume_id: csi-1 is not staged on this node" {
		t.Errorf("NodePublishVolume returned the message %q, want it to say csi-1 is not staged", got)
	}
}

func TestNodePublishVolumeRefusesASecretWithNoCredential(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	request := publishRequest(t, "csi-1", "file:///nowhere", nil)
	request.Secrets = map[string]string{"password": "hunter2"}
	_, err := n.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("NodePublishVolume returned %v, want %v", got, codes.InvalidArgument)
	}
	if !strings.Contains(status.Convert(err).Message(), "nodePublishSecretRef") {
		t.Errorf("NodePublishVolume returned the message %q, want it to mention nodePublishSecretRef", status.Convert(err).Message())
	}
}

func TestNodePublishVolumeBindsTheTreeReadOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, calls := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one", "docs/b.txt": "two"})
		request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})

		if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}

		tree := filepath.Join(n.store.volumeDir("csi-1"), "tree")
		if got := readTree(t, tree); !sameTree(got, map[string]string{"a.txt": "one", "docs/b.txt": "two"}) {
			t.Errorf("the tree contains %v, want a.txt and docs/b.txt", got)
		}
		if len(calls.mounts) != 2 {
			t.Fatalf("NodePublishVolume made %v, want a bind and a remount", calls.mounts)
		}
		if calls.mounts[0].source != tree || calls.mounts[0].target != request.TargetPath {
			t.Errorf("the bind is %+v, want %s onto %s", calls.mounts[0], tree, request.TargetPath)
		}
		if calls.mounts[1].flags&unix.MS_RDONLY == 0 {
			t.Errorf("the remount is %+v, want it read-only", calls.mounts[1])
		}
		if _, err := os.Stat(request.TargetPath); err != nil {
			t.Errorf("the target path is not there: %v", err)
		}
		if len(eventsOf(t, n)) != 0 {
			t.Errorf("a publish with no problem posted %v, want no events", eventsOf(t, n))
		}
	})
}

func TestNodePublishVolumeSucceedsWhenTheSameCallRepeats(t *testing.T) {
	n, calls := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})

	for range 2 {
		if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
	}
	if len(calls.mounts) != 2 {
		t.Errorf("two publishes made %v, want one bind and one remount", calls.mounts)
	}
}

func TestNodePublishVolumeRefusesTheSameVolumeAtAnotherPath(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	elsewhere := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	_, err := n.NodePublishVolume(t.Context(), elsewhere)
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("NodePublishVolume returned %v, want %v", got, codes.FailedPrecondition)
	}
}

func TestNodePublishVolumeSharesOneRepositoryPerURL(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	for _, id := range []string{"csi-1", "csi-2"} {
		request := publishRequest(t, id, fileURL(source), map[string]string{"pull": "never"})
		if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume %s: %v", id, err)
		}
	}
	repositories, err := os.ReadDir(filepath.Join(n.store.root, "repos"))
	if err != nil {
		t.Fatalf("reading the repositories: %v", err)
	}
	if len(repositories) != 1 {
		t.Errorf("two volumes of one URL made %d repositories, want 1", len(repositories))
	}
}

func TestNodePublishVolumeUsesTheDepthOfTheFirstVolume(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	commitFiles(t, source, map[string]string{"a.txt": "two"})

	first := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never", "depth": "1"})
	if _, err := n.NodePublishVolume(t.Context(), first); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	repo := n.store.repository(fileURL(source))
	if got := strings.TrimSpace(git(t, repo.dir, "rev-list", "--count", refPrefix+"main")); got != "1" {
		t.Errorf("the store has %s commits, want 1", got)
	}

	second := publishRequest(t, "csi-2", fileURL(source), map[string]string{"pull": "never", "depth": "0"})
	if _, err := n.NodePublishVolume(t.Context(), second); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if got := strings.TrimSpace(git(t, repo.dir, "rev-list", "--count", refPrefix+"main")); got != "1" {
		t.Errorf("the store has %s commits, want the shallow copy kept", got)
	}
}

func TestNodePublishVolumeRefusesAForgeItCannotReach(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		request := publishRequest(t, "csi-1",
			fileURL(filepath.Join(t.TempDir(), "gone")), map[string]string{"pull": "never"})
		_, err := n.NodePublishVolume(t.Context(), request)
		if got := status.Code(err); got != codes.Unavailable {
			t.Fatalf("NodePublishVolume returned %v, want %v", got, codes.Unavailable)
		}
		posted := eventsOf(t, n)
		if len(posted) != 1 || posted[0].Reason != reasonRefused {
			t.Errorf("NodePublishVolume posted %v, want one refusal", posted)
		}
		if _, err := os.Stat(n.store.volumeDir("csi-1")); err == nil {
			t.Error("a refused publish left the volume's directory behind")
		}
	})
}

func TestNodePublishVolumeRefusesARepositoryTheNodeNeverFetched(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	request := publishRequest(t, "csi-1", fileURL(filepath.Join(t.TempDir(), "gone")),
		map[string]string{"pull": "never", "offline": "allowStale"})
	_, err := n.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Unavailable {
		t.Fatalf("NodePublishVolume returned %v, want %v", got, codes.Unavailable)
	}
	if !strings.Contains(status.Convert(err).Message(), "no copy of main") {
		t.Errorf("NodePublishVolume returned the message %q, want it to say the node has no copy of main", status.Convert(err).Message())
	}
}

func TestNodePublishVolumePublishesAStaleTreeWhenTheVolumeAllowsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		url := fileURL(source)

		first := publishRequest(t, "csi-1", url, map[string]string{"pull": "never"})
		if _, err := n.NodePublishVolume(t.Context(), first); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
		if err := os.RemoveAll(source); err != nil {
			t.Fatalf("removing the forge: %v", err)
		}

		second := publishRequest(t, "csi-2", url, map[string]string{"pull": "never", "offline": "allowStale"})
		if _, err := n.NodePublishVolume(t.Context(), second); err != nil {
			t.Fatalf("NodePublishVolume with a stale copy: %v", err)
		}
		tree := filepath.Join(n.store.volumeDir("csi-2"), "tree")
		if got := readTree(t, tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
			t.Errorf("the stale tree contains %v, want a.txt alone", got)
		}

		abnormal, found := abnormalOf(t, n.metrics, "home", "csi-2")
		if !found || abnormal != 1 {
			t.Errorf("git_csi_volume_abnormal reads %v (found: %v) after a stale publish, want 1",
				abnormal, found)
		}
		posted := eventsOf(t, n)
		if len(posted) != 1 || posted[0].Reason != reasonStale {
			t.Errorf("a stale publish posted %v, want one stale event", posted)
		}
	})
}

func TestNodeUnpublishVolumeRemovesTheMountAndTheVolumeDirectory(t *testing.T) {
	n, calls := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	if _, err := n.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "csi-1", TargetPath: request.TargetPath,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if len(calls.unmounts) == 0 || calls.unmounts[len(calls.unmounts)-1] != request.TargetPath {
		t.Errorf("NodeUnpublishVolume unmounted %v, want %s", calls.unmounts, request.TargetPath)
	}
	if _, err := os.Stat(request.TargetPath); err == nil {
		t.Error("the target path stayed after the unpublish")
	}
	if _, err := os.Stat(n.store.volumeDir("csi-1")); err == nil {
		t.Error("the volume's directory stayed after the unpublish")
	}
	if _, err := os.Stat(n.store.repository(fileURL(source)).dir); err != nil {
		t.Errorf("the bare repository went with the volume: %v", err)
	}
}

func TestNodeUnpublishVolumeSucceedsForAVolumeItNeverPublished(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	target := filepath.Join(t.TempDir(), "mount")
	if _, err := n.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "csi-9", TargetPath: target,
	}); err != nil {
		t.Errorf("NodeUnpublishVolume: %v", err)
	}
}

func TestNodeUnpublishVolumeRefusesACallThatNamesTooLittle(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	for _, c := range []struct {
		name        string
		request     *csi.NodeUnpublishVolumeRequest
		wantMessage string
	}{
		{
			name:        "no volume",
			request:     &csi.NodeUnpublishVolumeRequest{TargetPath: "/mount"},
			wantMessage: "volume_id is empty",
		},
		{
			name:        "no target path",
			request:     &csi.NodeUnpublishVolumeRequest{VolumeId: "csi-1"},
			wantMessage: "target_path is empty",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := n.NodeUnpublishVolume(t.Context(), c.request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("NodeUnpublishVolume returned %v, want %v", got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.wantMessage {
				t.Errorf("NodeUnpublishVolume returned the message %q, want %q", got, c.wantMessage)
			}
		})
	}
}

func TestNodeGetVolumeStatsReportsTheTreesSize(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "12345"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	stats, err := n.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "csi-1", VolumePath: request.TargetPath,
	})
	if err != nil {
		t.Fatalf("NodeGetVolumeStats: %v", err)
	}
	usage := stats.GetUsage()
	if len(usage) != 1 {
		t.Fatalf("NodeGetVolumeStats returned %v, want one usage", usage)
	}
	if usage[0].GetUnit() != csi.VolumeUsage_BYTES {
		t.Errorf("NodeGetVolumeStats returned %v, want bytes", usage[0].GetUnit())
	}
	if usage[0].GetUsed() != 5 {
		t.Errorf("NodeGetVolumeStats returned %d used, want 5", usage[0].GetUsed())
	}
	if usage[0].GetAvailable() != 0 {
		t.Errorf("NodeGetVolumeStats returned %d available, want 0", usage[0].GetAvailable())
	}
}

func TestNodeGetVolumeStatsReturnsUnimplemented(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	for _, c := range []struct {
		name    string
		request *csi.NodeGetVolumeStatsRequest
		code    codes.Code
	}{
		{
			name:    "no volume",
			request: &csi.NodeGetVolumeStatsRequest{VolumePath: "/mount"},
			code:    codes.InvalidArgument,
		},
		{
			name:    "no volume path",
			request: &csi.NodeGetVolumeStatsRequest{VolumeId: "csi-1"},
			code:    codes.InvalidArgument,
		},
		{
			name:    "a volume this node never published",
			request: &csi.NodeGetVolumeStatsRequest{VolumeId: "csi-9", VolumePath: "/mount"},
			code:    codes.NotFound,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := n.NodeGetVolumeStats(t.Context(), c.request)
			if got := status.Code(err); got != c.code {
				t.Errorf("NodeGetVolumeStats returned %v, want %v", got, c.code)
			}
		})
	}
}

func TestNodeGetVolumeStatsReportsATreeItCannotRead(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if err := os.RemoveAll(n.store.volumeDir("csi-1")); err != nil {
		t.Fatalf("removing the tree: %v", err)
	}
	_, err := n.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "csi-1", VolumePath: request.TargetPath,
	})
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodeGetVolumeStats returned %v, want %v", got, codes.Internal)
	}
}

func TestNodePublishVolumeReportsAMountItCannotMake(t *testing.T) {
	n, calls := testNode(t, io.Discard)
	calls.failAt = 1
	calls.mountErr = unix.EPERM
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	_, err := n.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Fatalf("NodePublishVolume returned %v, want %v", got, codes.Internal)
	}
}

func TestNodePublishVolumeReportsAStoreItCannotWrite(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	file := filepath.Join(t.TempDir(), "file")
	writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
	n.store = newStore(file)

	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	_, err := n.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodePublishVolume returned %v, want %v", got, codes.Internal)
	}
}

func TestShortNamesACommitTheWayGitDoes(t *testing.T) {
	if got := short("d633176146e997eff3144573b5770e555b7af624"); got != "d633176" {
		t.Errorf("short returned %q, want d633176", got)
	}
	if got := short("d63"); got != "d63" {
		t.Errorf("short returned %q, want d63", got)
	}
}

// blobRef creates a tag that points at a blob and returns its name. A
// fetch of it succeeds, but the store gets no commit for the ref.
func blobRef(t *testing.T, dir string) string {
	t.Helper()
	blob := strings.TrimSpace(git(t, dir, "hash-object", "-w", "a.txt"))
	git(t, dir, "tag", "notacommit", blob)
	return "notacommit"
}

func TestNodePublishVolumeReportsWhatItCannotWrite(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, n *node, request *csi.NodePublishVolumeRequest)
	}{
		{
			name: "a target path under a file",
			setup: func(t *testing.T, _ *node, request *csi.NodePublishVolumeRequest) {
				file := filepath.Join(t.TempDir(), "file")
				writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
				request.TargetPath = filepath.Join(file, "mount")
			},
		},
		{
			name: "a repository directory it cannot make",
			setup: func(t *testing.T, n *node, _ *csi.NodePublishVolumeRequest) {
				if err := os.MkdirAll(n.store.root, 0o755); err != nil {
					t.Fatalf("making the store: %v", err)
				}
				writeFiles(t, n.store.root, map[string]string{"repos": ""})
			},
		},
		{
			name: "a tree that is already a file",
			setup: func(t *testing.T, n *node, _ *csi.NodePublishVolumeRequest) {
				directory := n.store.volumeDir("csi-1")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatalf("making the volume directory: %v", err)
				}
				writeFiles(t, directory, map[string]string{"tree": ""})
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
			c.setup(t, n, request)

			_, err := n.NodePublishVolume(t.Context(), request)
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("NodePublishVolume returned %v, want %v", got, codes.Internal)
			}
		})
	}
}

func TestNodePublishVolumeReportsAKeyItCannotWrite(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	request.Secrets = map[string]string{privateKeyKey: "KEY"}

	directory := n.store.volumeDir("csi-1")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	readOnlyDir(t, directory)

	_, err := n.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodePublishVolume returned %v, want %v", got, codes.Internal)
	}
}

func TestNodePublishVolumeReportsARefThatNamesNoCommit(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{
		"pull": "never", "ref": blobRef(t, source),
	})
	_, err := n.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodePublishVolume returned %v, want %v", got, codes.Internal)
	}
}

func TestNodePublishVolumeReportsAMountItCannotRemove(t *testing.T) {
	n, calls := testNode(t, io.Discard)
	calls.unmountErr = unix.EPERM
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	_, err := n.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodePublishVolume returned %v, want %v", got, codes.Internal)
	}
}

func TestNodeUnpublishVolumeReportsAMountItCannotRemove(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, n *node, calls *recordedMounts, target string)
	}{
		{
			name: "a mount that stays",
			setup: func(_ *testing.T, _ *node, calls *recordedMounts, _ string) {
				calls.unmountErr = unix.EPERM
			},
		},
		{
			name: "a target path it cannot remove",
			setup: func(t *testing.T, _ *node, _ *recordedMounts, target string) {
				readOnlyDir(t, filepath.Dir(target))
			},
		},
		{
			name: "a volume directory it cannot remove",
			setup: func(t *testing.T, n *node, _ *recordedMounts, _ string) {
				readOnlyDir(t, filepath.Join(n.store.root, "volumes"))
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, calls := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
			if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
				t.Fatalf("NodePublishVolume: %v", err)
			}
			c.setup(t, n, calls, request.TargetPath)

			_, err := n.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
				VolumeId: "csi-1", TargetPath: request.TargetPath,
			})
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("NodeUnpublishVolume returned %v, want %v", got, codes.Internal)
			}
		})
	}
}

func TestNodePublishVolumeReplacesATreeADriverThatRestartedLeftBehind(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one", "stale.txt": "gone"})
	url := fileURL(source)
	request := publishRequest(t, "csi-1", url, map[string]string{"pull": "never"})
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	tree := filepath.Join(n.store.volumeDir("csi-1"), "tree")
	before := inodeOf(t, tree)

	if err := os.Remove(filepath.Join(source, "stale.txt")); err != nil {
		t.Fatalf("removing the file: %v", err)
	}
	commitFiles(t, source, map[string]string{"a.txt": "two"})

	// A restarted driver has no volumes in memory. The kubelet publishes
	// every mount again, onto the tree that a pod still reads.
	n.mu.Lock()
	n.volumes = map[string]*volume{}
	n.mu.Unlock()
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume again: %v", err)
	}

	if got := readTree(t, tree); !sameTree(got, map[string]string{"a.txt": "two"}) {
		t.Errorf("the tree contains %v, want the new commit alone", got)
	}
	if after := inodeOf(t, tree); after != before {
		t.Errorf("the tree is a new directory (%d, was %d), which a bind mount would not follow", after, before)
	}
}
