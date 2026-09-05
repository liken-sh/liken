package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// testNode is a node whose mounts are recorded, because a test process
// may not mount, and whose events go to a client-go fake.
func testNode(t *testing.T, logs io.Writer) (*node, *recordedMounts) {
	t.Helper()
	calls := &recordedMounts{}
	answering := newNode(
		t.Context(),
		&config{nodeID: "node-1", store: filepath.Join(t.TempDir(), "store")},
		fakeEvents(t, logs),
		slog.New(slog.NewTextHandler(logs, nil)),
	)
	answering.mounts = calls
	return answering, calls
}

// publishRequest is the publish call the kubelet makes for an inline
// volume of the URL.
func publishRequest(t *testing.T, id, url string, extra map[string]string) *csi.NodePublishVolumeRequest {
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

// eventsOf is every Event the node posted, newest last.
func eventsOf(t *testing.T, answering *node) []corev1.Event {
	t.Helper()
	list, err := answering.events.client.CoreV1().Events("").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing the events: %v", err)
	}
	return list.Items
}

func TestNodeGetInfoNamesTheNode(t *testing.T) {
	client := csi.NewNodeClient(startServer(t, io.Discard))
	info, err := client.NodeGetInfo(t.Context(), &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatalf("NodeGetInfo: %v", err)
	}
	if info.GetNodeId() != "node-1" {
		t.Errorf("NodeGetInfo answered %q, want %q", info.GetNodeId(), "node-1")
	}
	if info.GetAccessibleTopology() != nil {
		t.Errorf("NodeGetInfo answered topology %v, want none", info.GetAccessibleTopology())
	}
}

func TestNodeGetCapabilitiesDeclaresTheThree(t *testing.T) {
	client := csi.NewNodeClient(startServer(t, io.Discard))
	answer, err := client.NodeGetCapabilities(t.Context(), &csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("NodeGetCapabilities: %v", err)
	}
	var got []csi.NodeServiceCapability_RPC_Type
	for _, capability := range answer.GetCapabilities() {
		got = append(got, capability.GetRpc().GetType())
	}
	want := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		csi.NodeServiceCapability_RPC_VOLUME_CONDITION,
	}
	if len(got) != len(want) {
		t.Fatalf("NodeGetCapabilities answered %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("NodeGetCapabilities answered %v, want %v", got, want)
		}
	}
}

func TestTheCallsPlan04AddsNameThatPlan(t *testing.T) {
	client := csi.NewNodeClient(startServer(t, io.Discard))
	for _, c := range []struct {
		name    string
		call    func(context.Context, csi.NodeClient) error
		message string
	}{
		{
			name: "NodeStageVolume",
			call: func(ctx context.Context, client csi.NodeClient) error {
				_, err := client.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{})
				return err
			},
			message: "NodeStageVolume: plan 04",
		},
		{
			name: "NodeUnstageVolume",
			call: func(ctx context.Context, client csi.NodeClient) error {
				_, err := client.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{})
				return err
			},
			message: "NodeUnstageVolume: plan 04",
		},
		{
			name: "NodeExpandVolume",
			call: func(ctx context.Context, client csi.NodeClient) error {
				_, err := client.NodeExpandVolume(ctx, &csi.NodeExpandVolumeRequest{})
				return err
			},
			message: "NodeExpandVolume: never; git volumes have no size",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.call(t.Context(), client)
			if got := status.Code(err); got != codes.Unimplemented {
				t.Fatalf("%s answered %v, want %v", c.name, got, codes.Unimplemented)
			}
			if got := status.Convert(err).Message(); got != c.message {
				t.Errorf("%s said %q, want %q", c.name, got, c.message)
			}
		})
	}
}

func TestNodePublishVolumeRefusesACallThatNamesTooLittle(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	for _, c := range []struct {
		name    string
		request *csi.NodePublishVolumeRequest
		says    string
	}{
		{
			name:    "no volume",
			request: &csi.NodePublishVolumeRequest{TargetPath: "/mount"},
			says:    "volume_id: the call names no volume",
		},
		{
			name:    "a volume id that is a path",
			request: &csi.NodePublishVolumeRequest{VolumeId: "../escape", TargetPath: "/mount"},
			says:    "volume_id: a volume id is one path element",
		},
		{
			name:    "no target path",
			request: &csi.NodePublishVolumeRequest{VolumeId: "csi-1"},
			says:    "target_path: the call names no path",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := answering.NodePublishVolume(t.Context(), c.request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("NodePublishVolume answered %v, want %v", got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.says {
				t.Errorf("NodePublishVolume said %q, want %q", got, c.says)
			}
		})
	}
}

func TestNodePublishVolumePostsARefusalOnThePod(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	request := publishRequest(t, "csi-1", "file:///nowhere", map[string]string{"branch": "main"})
	if _, err := answering.NodePublishVolume(t.Context(), request); err == nil {
		t.Fatal("NodePublishVolume answered no error for an unknown attribute")
	}
	posted := eventsOf(t, answering)
	if len(posted) != 1 {
		t.Fatalf("NodePublishVolume posted %d events, want 1", len(posted))
	}
	if posted[0].Reason != reasonRefused || !strings.Contains(posted[0].Message, "branch") {
		t.Errorf("the event says %q: %q", posted[0].Reason, posted[0].Message)
	}
	if posted[0].InvolvedObject.Name != "reader" {
		t.Errorf("the event is on %q, want the pod", posted[0].InvolvedObject.Name)
	}
}

func TestNodePublishVolumeNamesPlan04ForAPersistentVolume(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	request := publishRequest(t, "csi-1", "file:///nowhere", nil)
	delete(request.VolumeContext, ephemeralKey)
	_, err := answering.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Unimplemented {
		t.Fatalf("NodePublishVolume answered %v, want %v", got, codes.Unimplemented)
	}
	if got := status.Convert(err).Message(); got != "NodePublishVolume: a persistent volume: plan 04" {
		t.Errorf("NodePublishVolume said %q", got)
	}
}

func TestNodePublishVolumeRefusesASecretWithNoCredential(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	request := publishRequest(t, "csi-1", "file:///nowhere", nil)
	request.Secrets = map[string]string{"password": "hunter2"}
	_, err := answering.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("NodePublishVolume answered %v, want %v", got, codes.InvalidArgument)
	}
	if !strings.Contains(status.Convert(err).Message(), "nodePublishSecretRef") {
		t.Errorf("NodePublishVolume said %q", status.Convert(err).Message())
	}
}

func TestNodePublishVolumeBindsTheTreeReadOnly(t *testing.T) {
	answering, calls := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one", "docs/b.txt": "two"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})

	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	tree := filepath.Join(answering.store.volumeDir("csi-1"), "tree")
	if got := readTree(t, tree); !sameTree(got, map[string]string{"a.txt": "one", "docs/b.txt": "two"}) {
		t.Errorf("the tree holds %v", got)
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
	if len(eventsOf(t, answering)) != 0 {
		t.Errorf("a publish that worked posted %v", eventsOf(t, answering))
	}
}

func TestNodePublishVolumeAnswersTheSameCallTwice(t *testing.T) {
	answering, calls := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})

	for range 2 {
		if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
	}
	if len(calls.mounts) != 2 {
		t.Errorf("two publishes made %v, want one bind and one remount", calls.mounts)
	}
}

func TestNodePublishVolumeRefusesTheSameVolumeAtAnotherPath(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	elsewhere := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	_, err := answering.NodePublishVolume(t.Context(), elsewhere)
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("NodePublishVolume answered %v, want %v", got, codes.FailedPrecondition)
	}
}

func TestNodePublishVolumeSharesOneRepositoryPerURL(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	for _, id := range []string{"csi-1", "csi-2"} {
		request := publishRequest(t, id, fileURL(source), map[string]string{"pull": "never"})
		if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume %s: %v", id, err)
		}
	}
	repositories, err := os.ReadDir(filepath.Join(answering.store.root, "repos"))
	if err != nil {
		t.Fatalf("reading the repositories: %v", err)
	}
	if len(repositories) != 1 {
		t.Errorf("two volumes of one URL made %d repositories, want 1", len(repositories))
	}
}

func TestNodePublishVolumeTakesTheDepthOfTheFirstVolume(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	commitFiles(t, source, map[string]string{"a.txt": "two"})

	first := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never", "depth": "1"})
	if _, err := answering.NodePublishVolume(t.Context(), first); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	repo := answering.store.repository(fileURL(source))
	if got := strings.TrimSpace(git(t, repo.dir, "rev-list", "--count", refPrefix+"main")); got != "1" {
		t.Errorf("the store holds %s commits, want 1", got)
	}

	// A later volume with a depth of its own reuses what is there.
	second := publishRequest(t, "csi-2", fileURL(source), map[string]string{"pull": "never", "depth": "0"})
	if _, err := answering.NodePublishVolume(t.Context(), second); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if got := strings.TrimSpace(git(t, repo.dir, "rev-list", "--count", refPrefix+"main")); got != "1" {
		t.Errorf("the store holds %s commits, want the shallow copy kept", got)
	}
}

func TestNodePublishVolumeRefusesAForgeItCannotReach(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	request := publishRequest(t, "csi-1",
		fileURL(filepath.Join(t.TempDir(), "gone")), map[string]string{"pull": "never"})
	_, err := answering.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Unavailable {
		t.Fatalf("NodePublishVolume answered %v, want %v", got, codes.Unavailable)
	}
	posted := eventsOf(t, answering)
	if len(posted) != 1 || posted[0].Reason != reasonRefused {
		t.Errorf("NodePublishVolume posted %v, want one refusal", posted)
	}
	if _, err := os.Stat(answering.store.volumeDir("csi-1")); err == nil {
		t.Error("a refused publish left the volume's directory behind")
	}
}

func TestNodePublishVolumeRefusesARepositoryTheNodeNeverFetched(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	request := publishRequest(t, "csi-1", fileURL(filepath.Join(t.TempDir(), "gone")),
		map[string]string{"pull": "never", "offline": "allowStale"})
	_, err := answering.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Unavailable {
		t.Fatalf("NodePublishVolume answered %v, want %v", got, codes.Unavailable)
	}
	if !strings.Contains(status.Convert(err).Message(), "no copy of main") {
		t.Errorf("NodePublishVolume said %q, want the missing copy named", status.Convert(err).Message())
	}
}

func TestNodePublishVolumePublishesAStaleTreeWhenTheVolumeAllowsIt(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)

	first := publishRequest(t, "csi-1", url, map[string]string{"pull": "never"})
	if _, err := answering.NodePublishVolume(t.Context(), first); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatalf("removing the forge: %v", err)
	}

	second := publishRequest(t, "csi-2", url, map[string]string{"pull": "never", "offline": "allowStale"})
	if _, err := answering.NodePublishVolume(t.Context(), second); err != nil {
		t.Fatalf("NodePublishVolume with a stale copy: %v", err)
	}
	tree := filepath.Join(answering.store.volumeDir("csi-2"), "tree")
	if got := readTree(t, tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
		t.Errorf("the stale tree holds %v", got)
	}

	stats, err := answering.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "csi-2", VolumePath: second.TargetPath,
	})
	if err != nil {
		t.Fatalf("NodeGetVolumeStats: %v", err)
	}
	if !stats.GetVolumeCondition().GetAbnormal() {
		t.Error("a stale publish reported a normal condition")
	}
	posted := eventsOf(t, answering)
	if len(posted) != 1 || posted[0].Reason != reasonStale {
		t.Errorf("a stale publish posted %v, want one stale event", posted)
	}
}

func TestNodeUnpublishVolumeTakesTheMountAndTheStoreAway(t *testing.T) {
	answering, calls := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	if _, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
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
	if _, err := os.Stat(answering.store.volumeDir("csi-1")); err == nil {
		t.Error("the volume's directory stayed after the unpublish")
	}
	if _, err := os.Stat(answering.store.repository(fileURL(source)).dir); err != nil {
		t.Errorf("the bare repository went with the volume: %v", err)
	}
}

func TestNodeUnpublishVolumeAnswersAVolumeItNeverPublished(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	target := filepath.Join(t.TempDir(), "mount")
	if _, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "csi-9", TargetPath: target,
	}); err != nil {
		t.Errorf("NodeUnpublishVolume: %v", err)
	}
}

func TestNodeUnpublishVolumeRefusesACallThatNamesTooLittle(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	for _, c := range []struct {
		name    string
		request *csi.NodeUnpublishVolumeRequest
		says    string
	}{
		{
			name:    "no volume",
			request: &csi.NodeUnpublishVolumeRequest{TargetPath: "/mount"},
			says:    "volume_id: the call names no volume",
		},
		{
			name:    "no target path",
			request: &csi.NodeUnpublishVolumeRequest{VolumeId: "csi-1"},
			says:    "target_path: the call names no path",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := answering.NodeUnpublishVolume(t.Context(), c.request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("NodeUnpublishVolume answered %v, want %v", got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.says {
				t.Errorf("NodeUnpublishVolume said %q, want %q", got, c.says)
			}
		})
	}
}

func TestNodeGetVolumeStatsReportsTheTreesSize(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "12345"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	stats, err := answering.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "csi-1", VolumePath: request.TargetPath,
	})
	if err != nil {
		t.Fatalf("NodeGetVolumeStats: %v", err)
	}
	usage := stats.GetUsage()
	if len(usage) != 1 {
		t.Fatalf("NodeGetVolumeStats answered %v, want one usage", usage)
	}
	if usage[0].GetUnit() != csi.VolumeUsage_BYTES {
		t.Errorf("NodeGetVolumeStats answered %v, want bytes", usage[0].GetUnit())
	}
	if usage[0].GetUsed() != 5 {
		t.Errorf("NodeGetVolumeStats answered %d used, want 5", usage[0].GetUsed())
	}
	if usage[0].GetAvailable() != 0 {
		t.Errorf("NodeGetVolumeStats answered %d available, want 0", usage[0].GetAvailable())
	}
	condition := stats.GetVolumeCondition()
	if condition.GetAbnormal() {
		t.Errorf("NodeGetVolumeStats answered abnormal: %q", condition.GetMessage())
	}
	if !strings.HasPrefix(condition.GetMessage(), "main at ") {
		t.Errorf("the condition says %q, want the ref and the commit", condition.GetMessage())
	}
}

func TestNodeGetVolumeStatsRefusesACallItCannotAnswer(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
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
			_, err := answering.NodeGetVolumeStats(t.Context(), c.request)
			if got := status.Code(err); got != c.code {
				t.Errorf("NodeGetVolumeStats answered %v, want %v", got, c.code)
			}
		})
	}
}

func TestNodeGetVolumeStatsReportsATreeItCannotRead(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if err := os.RemoveAll(answering.store.volumeDir("csi-1")); err != nil {
		t.Fatalf("removing the tree: %v", err)
	}
	_, err := answering.NodeGetVolumeStats(t.Context(), &csi.NodeGetVolumeStatsRequest{
		VolumeId: "csi-1", VolumePath: request.TargetPath,
	})
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodeGetVolumeStats answered %v, want %v", got, codes.Internal)
	}
}

func TestNodePublishVolumeReportsAMountItCannotMake(t *testing.T) {
	answering, calls := testNode(t, io.Discard)
	calls.failAt = 1
	calls.mountErr = unix.EPERM
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	_, err := answering.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Fatalf("NodePublishVolume answered %v, want %v", got, codes.Internal)
	}
}

func TestNodePublishVolumeReportsAStoreItCannotWrite(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	file := filepath.Join(t.TempDir(), "file")
	writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
	answering.store = newStore(file)

	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	_, err := answering.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodePublishVolume answered %v, want %v", got, codes.Internal)
	}
}

func TestShortNamesACommitTheWayGitDoes(t *testing.T) {
	if got := short("d633176146e997eff3144573b5770e555b7af624"); got != "d633176" {
		t.Errorf("short answered %q", got)
	}
	if got := short("d63"); got != "d63" {
		t.Errorf("short answered %q", got)
	}
}

// blobRef is a ref that names a blob, so a fetch works and the store
// still holds no commit for it.
func blobRef(t *testing.T, dir string) string {
	t.Helper()
	blob := strings.TrimSpace(git(t, dir, "hash-object", "-w", "a.txt"))
	git(t, dir, "tag", "notacommit", blob)
	return "notacommit"
}

func TestNodePublishVolumeReportsWhatItCannotWrite(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, answering *node, request *csi.NodePublishVolumeRequest)
	}{
		{
			name: "a target path under a file",
			stand: func(t *testing.T, _ *node, request *csi.NodePublishVolumeRequest) {
				file := filepath.Join(t.TempDir(), "file")
				writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
				request.TargetPath = filepath.Join(file, "mount")
			},
		},
		{
			name: "a repository directory it cannot make",
			stand: func(t *testing.T, answering *node, _ *csi.NodePublishVolumeRequest) {
				if err := os.MkdirAll(answering.store.root, 0o755); err != nil {
					t.Fatalf("making the store: %v", err)
				}
				writeFiles(t, answering.store.root, map[string]string{"repos": ""})
			},
		},
		{
			name: "a tree that is already a file",
			stand: func(t *testing.T, answering *node, _ *csi.NodePublishVolumeRequest) {
				directory := answering.store.volumeDir("csi-1")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatalf("making the volume directory: %v", err)
				}
				writeFiles(t, directory, map[string]string{"tree": ""})
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
			c.stand(t, answering, request)

			_, err := answering.NodePublishVolume(t.Context(), request)
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("NodePublishVolume answered %v, want %v", got, codes.Internal)
			}
		})
	}
}

func TestNodePublishVolumeReportsAKeyItCannotWrite(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	request.Secrets = map[string]string{privateKeyKey: "KEY"}

	directory := answering.store.volumeDir("csi-1")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	readOnlyDir(t, directory)

	_, err := answering.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodePublishVolume answered %v, want %v", got, codes.Internal)
	}
}

func TestNodePublishVolumeReportsARefThatNamesNoCommit(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{
		"pull": "never", "ref": blobRef(t, source),
	})
	_, err := answering.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodePublishVolume answered %v, want %v", got, codes.Internal)
	}
}

func TestNodePublishVolumeReportsAMountItCannotTakeAway(t *testing.T) {
	answering, calls := testNode(t, io.Discard)
	calls.unmountErr = unix.EPERM
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	_, err := answering.NodePublishVolume(t.Context(), request)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("NodePublishVolume answered %v, want %v", got, codes.Internal)
	}
}

func TestNodeUnpublishVolumeReportsWhatItCannotTakeAway(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, answering *node, calls *recordedMounts, target string)
	}{
		{
			name: "a mount that stays",
			stand: func(_ *testing.T, _ *node, calls *recordedMounts, _ string) {
				calls.unmountErr = unix.EPERM
			},
		},
		{
			name: "a target path it cannot remove",
			stand: func(t *testing.T, _ *node, _ *recordedMounts, target string) {
				readOnlyDir(t, filepath.Dir(target))
			},
		},
		{
			name: "a volume directory it cannot remove",
			stand: func(t *testing.T, answering *node, _ *recordedMounts, _ string) {
				readOnlyDir(t, filepath.Join(answering.store.root, "volumes"))
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, calls := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
			if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
				t.Fatalf("NodePublishVolume: %v", err)
			}
			c.stand(t, answering, calls, request.TargetPath)

			_, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
				VolumeId: "csi-1", TargetPath: request.TargetPath,
			})
			if got := status.Code(err); got != codes.Internal {
				t.Errorf("NodeUnpublishVolume answered %v, want %v", got, codes.Internal)
			}
		})
	}
}

func TestNodePublishVolumeReplacesATreeADriverThatRestartedLeftBehind(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one", "stale.txt": "gone"})
	url := fileURL(source)
	request := publishRequest(t, "csi-1", url, map[string]string{"pull": "never"})
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	tree := filepath.Join(answering.store.volumeDir("csi-1"), "tree")
	before := inodeOf(t, tree)

	if err := os.Remove(filepath.Join(source, "stale.txt")); err != nil {
		t.Fatalf("removing the file: %v", err)
	}
	commitFiles(t, source, map[string]string{"a.txt": "two"})

	// A driver that restarted holds no volumes, and the kubelet publishes
	// every mount again onto the tree a pod still reads.
	answering.mu.Lock()
	answering.volumes = map[string]*volume{}
	answering.mu.Unlock()
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume again: %v", err)
	}

	if got := readTree(t, tree); !sameTree(got, map[string]string{"a.txt": "two"}) {
		t.Errorf("the tree holds %v, want the new commit alone", got)
	}
	if after := inodeOf(t, tree); after != before {
		t.Errorf("the tree is a new directory (%d, was %d), which a bind mount would not follow", after, before)
	}
}
