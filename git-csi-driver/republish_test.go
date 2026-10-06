package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	kevents "github.com/liken-sh/liken/kubernetes/events"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The two Secrets the tests below publish with. A token has no effect on
// a file URL, so a test that needs no forge can still carry one.
var (
	secretA = map[string]string{tokenKey: "token-a"}
	secretB = map[string]string{tokenKey: "token-b"}
)

// gitInvocations puts a git on PATH that writes each invocation to a
// log and then runs the real git, and answers a function that counts
// the invocations since this call.
func gitInvocations(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("finding git: %v", err)
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %s\nexec %s \"$@\"\n", quote(calls), quote(real))
	writeFiles(t, dir, map[string]string{"git": script})
	if err := os.Chmod(filepath.Join(dir, "git"), 0o755); err != nil {
		t.Fatalf("making the git wrapper runnable: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		content, _ := os.ReadFile(calls)
		return strings.Count(string(content), "\n")
	}
}

// publishedOnce stages, where the kind has a stage, and publishes one
// volume of the kind with the two Secrets, and answers the publish call
// and what the node holds. No loop of the volume runs git after this:
// the read-only kinds pull never, and the writeable volume's watch is
// stopped.
func publishedOnce(
	t testing.TB, answering *node, kind volumeKind, url string, stageSecret, publishSecret map[string]string,
) (*csi.NodePublishVolumeRequest, *volume) {
	t.Helper()
	var request *csi.NodePublishVolumeRequest
	switch kind {
	case inlineVolume:
		request = publishRequest(t, "csi-1", url, map[string]string{"pull": "never"})
	case readOnlyClaim:
		staged := readOnlyStage(t, "franchises", url, map[string]string{"pull": "never"})
		staged.Secrets = stageSecret
		if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
			t.Fatalf("NodeStageVolume: %v", err)
		}
		request = readOnlyPublish(t, staged, "reader-a")
	case writeableVolume:
		staged := stageRequest(t, "config", url, nil)
		staged.Secrets = stageSecret
		if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
			t.Fatalf("NodeStageVolume: %v", err)
		}
		request = persistentPublish(t, staged)
	}
	request.Secrets = publishSecret
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	answering.mu.Lock()
	held := answering.volumes[request.VolumeId]
	answering.mu.Unlock()
	if kind == writeableVolume {
		unwatched(t, answering, held)
	}
	return request, held
}

// tokenOf is the token the volume holds, and the empty string when it
// holds no credential.
func tokenOf(held *volume) string {
	if holding := held.credential(); holding != nil {
		return holding.token
	}
	return ""
}

func TestARepeatPublishTakesTheFreshCredentialAndDoesNothingElse(t *testing.T) {
	for _, kind := range []volumeKind{inlineVolume, readOnlyClaim, writeableVolume} {
		for _, c := range []struct {
			name   string
			repeat map[string]string
			holds  string
		}{
			{name: "the same Secret", repeat: secretA, holds: "token-a"},
			{name: "a rotated Secret", repeat: secretB, holds: "token-b"},
			{name: "no Secret", repeat: nil, holds: "token-a"},
		} {
			t.Run(kindNames[kind]+", "+c.name, func(t *testing.T) {
				answering, calls := testNode(t, io.Discard)
				source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
				request, held := publishedOnce(t, answering, kind, fileURL(source), secretA, secretA)
				binds, unbinds := len(calls.mounts), len(calls.unmounts)
				invocations := gitInvocations(t)

				request.Secrets = c.repeat
				if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
					t.Fatalf("a repeated NodePublishVolume: %v", err)
				}
				if got := tokenOf(held); got != c.holds {
					t.Errorf("the volume holds %q, want %q", got, c.holds)
				}
				if got := invocations(); got != 0 {
					t.Errorf("a repeated publish ran git %d times, want none", got)
				}
				if len(calls.mounts) != binds || len(calls.unmounts) != unbinds {
					t.Errorf("a repeated publish made %d mounts and %d unmounts, want none",
						len(calls.mounts)-binds, len(calls.unmounts)-unbinds)
				}
			})
		}
	}
}

func TestAFirstPublishRefusesASecretThatDiffersFromTheStage(t *testing.T) {
	for _, kind := range []volumeKind{readOnlyClaim, writeableVolume} {
		for _, c := range []struct {
			name    string
			stage   map[string]string
			publish map[string]string
			refused bool
			holds   string
			warned  int
		}{
			{name: "the same Secret", stage: secretA, publish: secretA, holds: "token-a"},
			{name: "different Secrets", stage: secretA, publish: secretB, refused: true, holds: "token-a"},
			{name: "a Secret at the publish alone", stage: nil, publish: secretA, holds: "token-a"},
			{name: "a Secret at the stage alone", stage: secretA, publish: nil, holds: "token-a", warned: 1},
		} {
			t.Run(kindNames[kind]+", "+c.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					answering, _ := testNode(t, io.Discard)
					source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
					url := fileURL(source)
					var staged *csi.NodeStageVolumeRequest
					var request *csi.NodePublishVolumeRequest
					if kind == readOnlyClaim {
						staged = readOnlyStage(t, "franchises", url, map[string]string{"pull": "never"})
					} else {
						staged = stageRequest(t, "config", url, nil)
					}
					staged.Secrets = c.stage
					if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
						t.Fatalf("NodeStageVolume: %v", err)
					}
					if kind == readOnlyClaim {
						request = readOnlyPublish(t, staged, "reader-a")
					} else {
						request = persistentPublish(t, staged)
					}
					request.Secrets = c.publish

					_, err := answering.NodePublishVolume(t.Context(), request)
					if refused := status.Code(err) == codes.InvalidArgument; refused != c.refused {
						t.Fatalf("NodePublishVolume answered %v, want refused: %v", err, c.refused)
					}
					if c.refused && !strings.Contains(status.Convert(err).Message(), "both nodeStageSecretRef and nodePublishSecretRef") {
						t.Errorf("the refusal says %q, want it to name both references", status.Convert(err).Message())
					}
					answering.mu.Lock()
					held := answering.staged[staged.VolumeId]
					answering.mu.Unlock()
					if got := tokenOf(held); got != c.holds {
						t.Errorf("the volume holds %q, want %q", got, c.holds)
					}
					if got := len(eventsWithReason(t, answering, reasonNoPublishSecret)); got != c.warned {
						t.Errorf("the node posted %d %s Events, want %d", got, reasonNoPublishSecret, c.warned)
					}
				})
			})
		}
	}
}

// loggedPublish sends the publish through the interceptor that writes
// the call log, and answers the lines it wrote.
func loggedPublish(t *testing.T, answering *node, request *csi.NodePublishVolumeRequest) string {
	t.Helper()
	logs := &logbook{}
	logging := logCalls(slog.New(slog.NewTextHandler(logs, nil)))
	_, err := logging(t.Context(), request,
		&grpc.UnaryServerInfo{FullMethod: "/csi.v1.Node/NodePublishVolume"},
		func(ctx context.Context, request any) (any, error) {
			return answering.NodePublishVolume(ctx, request.(*csi.NodePublishVolumeRequest))
		})
	if err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	return logs.String()
}

func TestARepeatPublishLogsOnlyWhatItChanged(t *testing.T) {
	for _, kind := range []volumeKind{inlineVolume, readOnlyClaim, writeableVolume} {
		for _, c := range []struct {
			name    string
			publish map[string]string
			repeat  map[string]string
			logged  []string
		}{
			{name: "the same Secret", publish: secretA, repeat: secretA, logged: nil},
			// A PersistentVolume with a stage Secret alone said so at its
			// first publish, and says nothing on every sync after it.
			{name: "no publish Secret", publish: nil, repeat: nil, logged: nil},
			{name: "a rotated Secret", publish: secretA, repeat: secretB,
				logged: []string{`msg="the credential changed"`, "NodePublishVolume"}},
		} {
			t.Run(kindNames[kind]+", "+c.name, func(t *testing.T) {
				logs := &logbook{}
				answering, _ := testNode(t, logs)
				source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
				request, _ := publishedOnce(t, answering, kind, fileURL(source), secretA, c.publish)
				before := len(logs.String())

				request.Secrets = c.repeat
				calls := loggedPublish(t, answering, request)
				written := logs.String()[before:] + calls
				if c.logged == nil && written != "" {
					t.Errorf("a repeated publish that changed nothing logged %q, want nothing", written)
				}
				for _, line := range c.logged {
					if !strings.Contains(written, line) {
						t.Errorf("the log is %q, want %q in it", written, line)
					}
				}
			})
		}
	}
}

func TestARepeatPublishThatReturnsACredentialIsLogged(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	request.Secrets = secretA
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	logs := &logbook{}
	again, _ := testNode(t, logs)
	again.store = answering.store
	again.mounted = func(string) bool { return true }
	again.resume(t.Context())
	before := len(logs.String())

	calls := loggedPublish(t, again, request)
	written := logs.String()[before:] + calls
	for _, line := range []string{`msg="the credential returned"`, "NodePublishVolume"} {
		if !strings.Contains(written, line) {
			t.Errorf("the log is %q, want %q in it", written, line)
		}
	}
}

func TestAFirstPublishIsLogged(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if got := loggedPublish(t, answering, request); !strings.Contains(got, "NodePublishVolume") {
		t.Errorf("a first publish logged %q, want its call line", got)
	}
}

func TestTheCallLogKeepsEveryErrorAndEveryUnmarkedCall(t *testing.T) {
	refused := status.Error(codes.InvalidArgument, "refused")
	for _, c := range []struct {
		name   string
		quiet  bool
		answer error
		lines  int
	}{
		{name: "a quiet call that worked", quiet: true, answer: nil, lines: 0},
		{name: "a quiet call that failed", quiet: true, answer: refused, lines: 1},
		{name: "a call that worked", quiet: false, answer: nil, lines: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := &logbook{}
			logging := logCalls(slog.New(slog.NewTextHandler(logs, nil)))
			_, err := logging(t.Context(), nil, &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Node/NodePublishVolume"},
				func(ctx context.Context, _ any) (any, error) {
					if c.quiet {
						quietCall(ctx)
					}
					return nil, c.answer
				})
			if !errors.Is(err, c.answer) {
				t.Errorf("the interceptor answered %v, want %v", err, c.answer)
			}
			if got := strings.Count(logs.String(), "\n"); got != c.lines {
				t.Errorf("the interceptor wrote %d lines, want %d: %q", got, c.lines, logs)
			}
		})
	}
}

func TestAQuietMarkOutsideTheCallLogIsDropped(t *testing.T) {
	quietCall(t.Context())
}

// restartedQuiet is a second driver on the same store whose mount
// table holds every target, and whose sweep and fetch timers are an
// hour, so only a republish can make it fetch or push within a test.
func restartedQuiet(t *testing.T, answering *node) *node {
	t.Helper()
	again, _ := testNode(t, io.Discard)
	again.store = answering.store
	again.events = answering.events
	again.arms.client = answering.arms.client
	again.sweep = time.Hour
	again.mounted = func(string) bool { return true }
	again.resume(t.Context())
	return again
}

// waitForFile waits until the bubble is blocked, and fails unless the
// tree holds the file.
func waitForFile(t *testing.T, tree, name string) {
	t.Helper()
	synctest.Wait()
	if _, err := os.Stat(filepath.Join(tree, name)); err != nil {
		t.Fatalf("%s holds no %s: %v", tree, name, err)
	}
}

func TestAResumedVolumeFetchesAtTheRepublishThatReturnsItsCredential(t *testing.T) {
	for _, c := range []struct {
		name  string
		kind  volumeKind
		pulls map[string]string
	}{
		{name: "an inline volume", kind: inlineVolume, pulls: map[string]string{"pull": "1h"}},
		{name: "a read-only claim", kind: readOnlyClaim, pulls: map[string]string{"pull": "on-demand"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				answering, _ := testNode(t, io.Discard)
				source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
				var request *csi.NodePublishVolumeRequest
				if c.kind == inlineVolume {
					request = publishRequest(t, "csi-1", fileURL(source), c.pulls)
					request.Secrets = secretA
					if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
						t.Fatalf("NodePublishVolume: %v", err)
					}
				} else {
					staged := readOnlyStage(t, "franchises", fileURL(source), c.pulls)
					staged.Secrets = secretA
					if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
						t.Fatalf("NodeStageVolume: %v", err)
					}
					request = readOnlyPublish(t, staged, "reader-a")
					request.Secrets = secretA
					if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
						t.Fatalf("NodePublishVolume: %v", err)
					}
				}
				commitFiles(t, source, map[string]string{"b.txt": "two"})

				again := restartedQuiet(t, answering)
				again.mu.Lock()
				resumed := again.volumes[request.VolumeId]
				again.mu.Unlock()
				if abnormal, message := resumed.report(); !abnormal || !strings.Contains(message, "no credential") {
					t.Errorf("the resumed volume reports %q, want the credential it waits for", message)
				}

				if _, err := again.NodePublishVolume(t.Context(), request); err != nil {
					t.Fatalf("the republish: %v", err)
				}
				waitForFile(t, resumed.tree, "b.txt")
				waitForCondition(t, resumed, "main at")
				if got := tokenOf(resumed); got != "token-a" {
					t.Errorf("the resumed volume holds %q, want the republished token", got)
				}
			})
		})
	}
}

func TestAResumedWriteableVolumePushesAtTheRepublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, _ := testNode(t, io.Discard)
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		boundVolume(t, answering, "config", "config-eager")
		armingClass(t, answering, "config-eager", nil)
		staged := stageRequest(t, "config", fileURL(remote), nil)
		staged.Secrets = secretA
		if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
			t.Fatalf("NodeStageVolume: %v", err)
		}
		request := persistentPublish(t, staged)
		request.Secrets = secretA
		if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
		answering.mu.Lock()
		held := answering.volumes["config"]
		answering.mu.Unlock()
		waitForArmed(t, held, true)
		unwatched(t, answering, held)
		writeFiles(t, held.tree, map[string]string{"one.yaml": "1"})
		answering.commit(t.Context(), held, held.policyNow())
		committed := held.work.refCommit(t.Context(), "HEAD")

		again := restartedQuiet(t, answering)
		again.mu.Lock()
		resumed := again.volumes["config"]
		again.mu.Unlock()
		if got := git(t, remote, "rev-parse", "main"); strings.TrimSpace(got) == committed {
			t.Fatal("the remote holds the commit before the republish")
		}

		if _, err := again.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("the republish: %v", err)
		}
		waitForPushed(t, resumed)
		if got := strings.TrimSpace(git(t, remote, "rev-parse", "main")); got != committed {
			t.Errorf("the remote holds %s after the republish, want %s", got, committed)
		}
		waitForCondition(t, resumed, "main at")
	})
}

// timesPosted counts each post of the Events, the repeats the recorder
// folded into one Event included.
func timesPosted(posted []kevents.Event) int32 {
	var total int32
	for _, one := range posted {
		total += one.Count
	}
	return total
}

func TestAResumedVolumeWithNoPublishSecretSaysWhatItNeeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, _ := testNode(t, io.Discard)
		boundVolume(t, answering, "franchises", "")
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		staged := readOnlyStage(t, "franchises", fileURL(source), map[string]string{"pull": "never"})
		staged.Secrets = secretA
		if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
			t.Fatalf("NodeStageVolume: %v", err)
		}
		request := publishedTo(t, answering, staged, "reader-a")
		before := timesPosted(eventsWithReason(t, answering, reasonNoPublishSecret))

		again := restartedQuiet(t, answering)
		again.mu.Lock()
		resumed := again.volumes["franchises"]
		again.mu.Unlock()
		// A fetch that failed without the credential leaves its error, and
		// the report still says what the volume waits for.
		resumed.reportFetchFailed("git fetch: Permission denied (publickey)")
		for range 3 {
			if _, err := again.NodePublishVolume(t.Context(), request); err != nil {
				t.Fatalf("the republish: %v", err)
			}
		}
		abnormal, message := resumed.report()
		if !abnormal || !strings.Contains(message, "nodePublishSecretRef") {
			t.Errorf("the resumed volume reports %q, want it to name nodePublishSecretRef", message)
		}
		// A resumed claim knows its claim and none of its pods, so one Event
		// goes to the claim, however many republishes arrive. It repeats
		// the Event the first node posted, so the recorder counts it on
		// that Event.
		if got := timesPosted(eventsWithReason(t, again, reasonNoPublishSecret)) - before; got != 1 {
			t.Errorf("three republishes posted %s %d times, want once", reasonNoPublishSecret, got)
		}
		if !recordOf(t, again, "franchises").Credentials {
			t.Error("the record no longer says the volume needs a credential")
		}
	})
}

func TestAPrivateRepositoryFetchesAgainAtTheRepublishAfterARestart(t *testing.T) {
	// sshd starts outside the bubble. The goroutine that reads its log
	// waits on a pipe, which is not durably blocked, so inside the
	// bubble it would stop the clock.
	port, holder := sshdOrSkip(t, t.TempDir())
	synctest.Test(t, func(t *testing.T) {
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		url := fmt.Sprintf("ssh://127.0.0.1:%d%s", port, source)
		secrets := map[string]string{privateKeyKey: holder.privateKey, knownHostsKey: holder.knownHosts}

		answering, _ := testNode(t, io.Discard)
		request := publishRequest(t, "csi-1", url, map[string]string{"pull": "1h"})
		request.Secrets = secrets
		if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
		commitFiles(t, source, map[string]string{"b.txt": "two"})

		again := restartedQuiet(t, answering)
		again.mu.Lock()
		resumed := again.volumes["csi-1"]
		again.mu.Unlock()
		if _, err := again.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("the republish: %v", err)
		}
		waitForFile(t, resumed.tree, "b.txt")
	})
}

// BenchmarkARepeatPublish measures the call the kubelet makes for every
// volume on every pod sync once requiresRepublish is set.
func BenchmarkARepeatPublish(b *testing.B) {
	for _, kind := range []volumeKind{inlineVolume, readOnlyClaim, writeableVolume} {
		b.Run(kindNames[kind], func(b *testing.B) {
			answering, _ := testNode(b, io.Discard)
			source := repositoryWithACommit(b, map[string]string{"a.txt": "one"})
			request, _ := publishedOnce(b, answering, kind, fileURL(source), secretA, secretA)
			for b.Loop() {
				if _, err := answering.NodePublishVolume(b.Context(), request); err != nil {
					b.Fatalf("a repeated NodePublishVolume: %v", err)
				}
			}
		})
	}
}

func TestAResumedVolumeThatPullsNeverIsNormalAfterItsRepublish(t *testing.T) {
	for _, c := range []struct {
		name    string
		secrets map[string]string
	}{
		{name: "a public repository", secrets: nil},
		{name: "a private repository", secrets: secretA},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
			request.Secrets = c.secrets
			if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
				t.Fatalf("NodePublishVolume: %v", err)
			}

			again := restartedQuiet(t, answering)
			again.mu.Lock()
			resumed := again.volumes["csi-1"]
			again.mu.Unlock()
			if _, err := again.NodePublishVolume(t.Context(), request); err != nil {
				t.Fatalf("the republish: %v", err)
			}
			if abnormal, message := resumed.report(); abnormal {
				t.Errorf("the resumed volume reports %q after its republish, want it normal", message)
			}
		})
	}
}

func TestAnArrivedCredentialPassesOverAVolumeTheLoopDropped(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	loop := followerOf(answering, "file:///nowhere")
	loop.arrived = make(chan struct{}, 1)
	loop.wanted = map[string]*volume{}
	dropped := &volume{id: "csi-1"}

	loop.credentialArrived(dropped)
	if len(loop.wanted) != 0 || len(loop.arrived) != 0 {
		t.Errorf("a volume off the loop left %d wanted and %d wakes, want none",
			len(loop.wanted), len(loop.arrived))
	}
}

func TestAnArrivedCredentialWakesEachLoopOnce(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	held := &volume{id: "csi-1"}
	loop := followerOf(answering, "file:///nowhere")
	loop.arrived = make(chan struct{}, 1)
	loop.wanted = map[string]*volume{}
	loop.volumes[held.id] = held
	seeing := &watcher{arrived: make(chan struct{}, 1)}

	for range 2 {
		loop.credentialArrived(held)
		seeing.credentialArrived()
	}
	if len(loop.arrived) != 1 || len(seeing.arrived) != 1 {
		t.Errorf("two arrivals left %d wakes on the loop and %d on the watch, want one each",
			len(loop.arrived), len(seeing.arrived))
	}
}
