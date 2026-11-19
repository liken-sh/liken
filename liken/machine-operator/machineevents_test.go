package main

// Tests of how the machine's readers wake the reconcile loop. A test
// gives the loop channels in place of the readers and sends the events
// itself, because it cannot make the kernel send a uevent.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"golang.org/x/sys/unix"
)

// plugInSoundCard writes an audio controller into the fake sysfs: a pci
// device bound to a driver, with one ALSA control node under it.
func plugInSoundCard(t *testing.T, sysRoot string) {
	t.Helper()
	plugInSoundCardAt(t, sysRoot, "0000:00:1f.3", 0)
}

func plugInSoundCardAt(t *testing.T, sysRoot, address string, index int) {
	t.Helper()
	card := filepath.Join(sysRoot, "bus", "pci", "devices", address)
	node := filepath.Join(card, "sound", fmt.Sprintf("card%d", index), fmt.Sprintf("controlC%d", index))
	for _, dir := range []string{node, filepath.Join(sysRoot, "bus", "pci", "drivers", "snd_hda_intel"), filepath.Join(sysRoot, "class", "sound")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating the sound card directories: %v", err)
		}
	}
	files := map[string]string{
		filepath.Join(card, "modalias"): "pci:v00008086d000054C8",
		filepath.Join(node, "dev"):      fmt.Sprintf("116:%d", index),
		filepath.Join(node, "uevent"):   fmt.Sprintf("DEVNAME=snd/controlC%d", index),
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
			t.Fatalf("writing the sound card files: %v", err)
		}
	}
	links := map[string]string{
		filepath.Join(card, "driver"):    filepath.Join(sysRoot, "bus", "pci", "drivers", "snd_hda_intel"),
		filepath.Join(node, "subsystem"): filepath.Join(sysRoot, "class", "sound"),
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("linking the sound card: %v", err)
		}
	}
}

// sliceDevices returns the device names in node-1's ResourceSlice, or
// nil when there is no slice.
func sliceDevices(t *testing.T, l *loop) []string {
	t.Helper()
	slice := &kubernetes.ResourceSlice{}
	err := l.objects.client.RequestJSON(http.MethodGet, kubernetes.ResourceSlicesPath+"/node-1-liken.sh", nil, slice)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("reading the resource slice: %v", err)
	}
	var names []string
	for _, d := range slice.Spec.Devices {
		names = append(names, d.Name)
	}
	return names
}

// startLoop runs the loop until the test ends, and returns a channel
// that receives the error from run.
func startLoop(t *testing.T, l *loop) <-chan error {
	t.Helper()
	current, err := l.objects.machine("node-1")
	if err != nil {
		t.Fatalf("reading the Machine: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done, finished := make(chan error, 1), make(chan struct{})
	// The cleanup waits for the loop to return, so no pass still runs
	// when isolatePass's cleanup restores the host paths.
	t.Cleanup(func() { cancel(); <-finished })
	go func() { done <- l.run(ctx, current); close(finished) }()
	return done
}

// A device that arrives after the first pass appears in the slice one
// second after its uevent.
func TestAUeventWakesAPassThatPublishesTheNewDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		watch := &fakeFactsWatch{wake: make(chan struct{}, 1)}
		l, _ := testLoop(t, newPassAPI(), watch)
		uevents := make(chan struct{}, 1)
		l.uevents = uevents
		startLoop(t, l)
		synctest.Wait()
		before := sliceDevices(t, l)

		plugInSoundCard(t, draSysfsRoot)
		uevents <- struct{}{}
		time.Sleep(time.Second)
		synctest.Wait()

		if after := sliceDevices(t, l); len(before) != 0 || !slices.Equal(after, []string{"pci-0000-00-1f-3"}) {
			t.Errorf("the slice held %q before the uevent and %q after, want nothing and the sound card", before, after)
		}
	})
}

func TestABurstOfUeventsSettlesIntoOnePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		watch := &fakeFactsWatch{wake: make(chan struct{}, 1)}
		l, _ := testLoop(t, newPassAPI(), watch)
		uevents := make(chan struct{}, 1)
		l.uevents = uevents
		startLoop(t, l)
		synctest.Wait()

		for range 12 {
			uevents <- struct{}{}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(time.Minute)
		synctest.Wait()

		if passes := watch.syncs.Load(); passes != 2 {
			t.Errorf("the loop ran %d passes for one burst after the first, want 2", passes)
		}
	})
}

// When another process rewrites /etc/hosts, the pass that the inotify
// event wakes writes the declared file back.
func TestAHostsEventWakesAPassThatWritesTheFileBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		l, _ := testLoop(t, newPassAPI(), &fakeFactsWatch{wake: make(chan struct{}, 1)})
		hosts := make(chan struct{}, 1)
		l.hosts = hosts
		startLoop(t, l)
		synctest.Wait()
		written, err := os.ReadFile(hostsPath)
		if err != nil {
			t.Fatalf("reading the hosts file: %v", err)
		}

		if err := os.WriteFile(hostsPath, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
			t.Fatalf("writing the hosts file: %v", err)
		}
		hosts <- struct{}{}
		time.Sleep(time.Second)
		synctest.Wait()

		if got, err := os.ReadFile(hostsPath); err != nil || string(got) != string(written) {
			t.Errorf("hosts reads %q after the event, want the pass's %q", got, written)
		}
	})
}

// main exits the process on the error that run returns.
func TestALoopEndsWhenAReaderStops(t *testing.T) {
	cases := []struct {
		name   string
		reader func(*loop, <-chan struct{})
		want   string
	}{
		{"the uevent listener", func(l *loop, c <-chan struct{}) { l.uevents = c }, "the uevent listener: the reader stopped"},
		{"the hosts watch", func(l *loop, c <-chan struct{}) { l.hosts = c }, "the hosts watch: the reader stopped"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				isolatePass(t)
				writeAppliedSysctls(t)
				l, _ := testLoop(t, newPassAPI(), &fakeFactsWatch{wake: make(chan struct{}, 1)})
				reader := make(chan struct{})
				tc.reader(l, reader)
				done := startLoop(t, l)
				synctest.Wait()

				close(reader)
				synctest.Wait()

				select {
				case err := <-done:
					if err == nil || err.Error() != tc.want {
						t.Errorf("run returned %v, want %q", err, tc.want)
					}
				default:
					t.Error("the loop kept running after its reader stopped")
				}
			})
		})
	}
}

func TestALoopEndsWhenTheFactsWatchDies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		dying := &fakeFactsWatch{wake: make(chan struct{})}
		l, _ := testLoop(t, newPassAPI(), dying)
		done := startLoop(t, l)
		synctest.Wait()

		close(dying.wake)
		synctest.Wait()

		select {
		case err := <-done:
			if !errors.Is(err, errFactsStopped) {
				t.Errorf("run returned %v, want %v", err, errFactsStopped)
			}
		default:
			t.Error("the loop kept running after the facts watch died")
		}
	})
}

// init publishes the facts before the operator starts, so a facts
// watch that cannot open means the machine is broken.
func TestALoopEndsWhenTheFactsWatchCannotOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		api := &countingMachineReads{next: newPassAPI()}
		l, _ := testLoop(t, api)
		l.watchFactsTree = func(context.Context) (*factsWatch, error) { return nil, os.ErrPermission }

		err := <-startLoop(t, l)

		if !errors.Is(err, os.ErrPermission) || api.reads.Load() != 1 {
			t.Errorf("run returned %v after %d passes, want %v and none", err, api.reads.Load()-1, os.ErrPermission)
		}
	})
}

func TestARelayEndsQuietlyWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- relay(ctx, make(chan struct{}), make(chan struct{}, 1), nil) }()

		cancel()

		if err := <-done; err != nil {
			t.Errorf("relay returned %v, want nil", err)
		}
	})
}

// The uevent listener opens as any user, and isolatePass points
// hostsPath at a directory that exists, so both real readers open in a
// test.
func TestTheSeamsOpenTheRealReaders(t *testing.T) {
	isolatePass(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if _, err := watchHostsFile(ctx); err != nil {
		t.Errorf("opening the hosts watch: %v", err)
	}
	if _, err := listenForUevents(ctx); err != nil {
		t.Errorf("opening the uevent listener: %v", err)
	}
}

// The operator's own slice write comes back through the watch at the
// version that the write returned, and wakes nothing. Otherwise a write
// that the API server stores in a different form would wake one pass
// after another.
func TestTheSliceWatchWakesOnlyForAnotherWritersChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		fake := newPassAPI()
		client, watcher := passClients(t, fake)
		wakes := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := watchThisMachine(ctx, watcher, client, "node-1", "lab", watch.Signal(wakes), func(string) {})
		awaitCopies(t, r)
		<-wakes
		owner := kubernetes.OwnerReference{APIVersion: "v1", Kind: "Node", Name: "node-1", UID: "uid-node-1"}
		woke := func() bool {
			synctest.Wait()
			select {
			case <-wakes:
				return true
			default:
				return false
			}
		}

		if err := r.sliceWriter().Write(client, "node-1", nil, owner, []kubernetes.SliceDevice{{Name: "usb-1-2"}}); err != nil {
			t.Fatalf("writing the first resource slice: %v", err)
		}
		ownCreate := woke()
		current, err := r.resourceSlice("node-1")
		if err != nil || current == nil {
			t.Fatalf("reading the slice: %v", err)
		}
		if err := r.sliceWriter().Write(client, "node-1", current, owner, []kubernetes.SliceDevice{{Name: "usb-1-3"}}); err != nil {
			t.Fatalf("writing the second resource slice: %v", err)
		}
		ownUpdate := woke()
		foreign, _ := json.Marshal(fakeapi.Object("resource.k8s.io/v1", "ResourceSlice", "", "node-1-liken.sh", nil))
		if err := client.RequestJSON(http.MethodPut, kubernetes.ResourceSlicesPath+"/node-1-liken.sh", foreign, nil); err != nil {
			t.Fatalf("writing the foreign resource slice: %v", err)
		}
		foreignUpdate := woke()
		if err := client.RequestJSON(http.MethodDelete, kubernetes.ResourceSlicesPath+"/node-1-liken.sh", nil, nil); err != nil {
			t.Fatalf("deleting the resource slice: %v", err)
		}
		deleted := woke()

		if ownCreate || ownUpdate || !foreignUpdate || !deleted {
			t.Errorf("woke on the own create %v, the own update %v, another writer's update %v, the delete %v; want false, false, true, true",
				ownCreate, ownUpdate, foreignUpdate, deleted)
		}
	})
}

// refusingSlices responds to every write of a ResourceSlice with a 503
// and counts the writes.
type refusingSlices struct {
	next   http.Handler
	writes atomic.Int64
}

func (h *refusingSlices) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && strings.HasPrefix(r.URL.Path, kubernetes.ResourceSlicesPath) {
		h.writes.Add(1)
		http.Error(w, "etcd is gone", http.StatusServiceUnavailable)
		return
	}
	h.next.ServeHTTP(w, r)
}

// A failed slice write is retried on the retry timer, with no other
// wake.
func TestAFailedSliceWriteIsWrittenAgainOnTheRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		plugInSoundCard(t, draSysfsRoot)
		api := &refusingSlices{next: newPassAPI()}
		l, _ := testLoop(t, api, &fakeFactsWatch{wake: make(chan struct{}, 1)})
		startLoop(t, l)
		synctest.Wait()
		first := api.writes.Load()

		time.Sleep(1500 * time.Millisecond)
		synctest.Wait()

		if first != 1 || api.writes.Load() != 2 {
			t.Errorf("the slice was written %d times by the first pass and %d after the retry, want 1 and 2", first, api.writes.Load())
		}
	})
}

func TestAPassRunsWhenTheFactsWatchCannotSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		api := &countingMachineReads{next: newPassAPI()}
		l, _ := testLoop(t, api)
		l.watchFactsTree = func(context.Context) (*factsWatch, error) {
			return &factsWatch{wake: make(chan struct{}), sync: func() error { return os.ErrPermission }}, nil
		}
		startLoop(t, l)
		synctest.Wait()

		if passes := api.reads.Load() - 1; passes != 1 {
			t.Errorf("the loop ran %d passes, want 1", passes)
		}
	})
}

// A facts watch that is closed before the first pass ends the loop,
// because the pass would read a tree that nothing watches.
func TestALoopEndsBeforeItsFirstPassOnADeadFactsWatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		api := &countingMachineReads{next: newPassAPI()}
		dead := &fakeFactsWatch{wake: make(chan struct{})}
		close(dead.wake)
		l, _ := testLoop(t, api, dead)

		err := <-startLoop(t, l)

		if !errors.Is(err, errFactsStopped) || api.reads.Load() != 1 {
			t.Errorf("run returned %v after %d passes, want %v and none", err, api.reads.Load()-1, errFactsStopped)
		}
	})
}

func TestAFightOverAFileMakesAPassEveryFiveSecondsAtMost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		watch := &fakeFactsWatch{wake: make(chan struct{}, 1)}
		l, _ := testLoop(t, newPassAPI(), watch)
		hosts := make(chan struct{}, 1)
		l.hosts = hosts
		startLoop(t, l)
		synctest.Wait()

		for range 200 {
			select {
			case hosts <- struct{}{}:
			default:
			}
			time.Sleep(100 * time.Millisecond)
		}
		synctest.Wait()

		if passes := watch.syncs.Load() - 1; passes > 4 {
			t.Errorf("twenty seconds of writes made %d passes, want at most 4", passes)
		}
	})
}

// A watch's cached copy is ready again as soon as the API server
// accepts the watch, before the events missed during the failure
// arrive. So the next pass reads every kind from the API server, and
// the pass after it reads the cache again.
func TestAPassAfterARecoveryReadsTheAPIServerOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		fake := newPassAPI()
		client, watcher := passClients(t, fake)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := watchThisMachine(ctx, watcher, client, "node-1", "lab", func() {}, func(string) {})
		awaitCopies(t, r)
		reads := func(pass *reader) []string {
			fake.Forget()
			for _, read := range []func() error{
				func() error { _, err := pass.machine("node-1"); return err },
				func() error { _, err := pass.node("node-1"); return err },
				func() error { _, err := pass.cluster("lab"); return err },
				func() error { _, err := pass.registryCredentials(); return err },
				func() error { _, err := pass.operatorPods("node-1"); return err },
				func() error { _, err := pass.resourceSlice("node-1"); return err },
			} {
				if err := read(); err != nil && !errors.Is(err, apiclient.ErrNotFound) {
					t.Fatalf("reading before the pass: %v", err)
				}
			}
			return fake.Requests()
		}
		_, _ = r.freshMachine("node-1")

		r.recovered.Store(true)
		afterRecovery := reads(r.throughAPIOnce())
		next := reads(r.throughAPIOnce())

		if len(afterRecovery) != 6 || len(next) != 0 {
			t.Errorf("the pass after the recovery sent %q, and the next one %q; want six reads and none", afterRecovery, next)
		}
	})
}

// A missing directory means a pod template older than the /host/etc
// mount.
func TestOnlyAMissingDirectoryLetsTheOperatorRunWithoutTheHostsWatch(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		fatal bool
	}{
		{"opened", nil, false},
		{"no directory", fmt.Errorf("watching /host/etc: %w", fs.ErrNotExist), false},
		{"no inotify instances left", fmt.Errorf("inotify_init1: %w", unix.EMFILE), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hostsWatchFailure(c.err) != nil; got != c.fatal {
				t.Errorf("hostsWatchFailure(%v) is fatal = %v, want %v", c.err, got, c.fatal)
			}
		})
	}
}

// The Machine watch wakes the loop for another writer's change, such as
// cluster-operator's grant of a reboot turn, and not for this operator's
// own status write. Otherwise each status write would cost a second
// pass that finds nothing to do.
func TestTheMachineWatchWakesOnlyForAnotherWritersChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		fake := newPassAPI()
		client, watcher := passClients(t, fake)
		wakes := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := watchThisMachine(ctx, watcher, client, "node-1", "lab", watch.Signal(wakes), func(string) {})
		awaitCopies(t, r)
		<-wakes
		woke := func() bool {
			synctest.Wait()
			select {
			case <-wakes:
				return true
			default:
				return false
			}
		}

		current, err := r.machine("node-1")
		if err != nil {
			t.Fatalf("reading the Machine: %v", err)
		}
		status := current.Status
		status.Phase = "Ready"
		if err := r.publishStatus(current, &status); err != nil {
			t.Fatalf("publishing the Machine status: %v", err)
		}
		ownWrite := woke()
		current, err = r.machine("node-1")
		if err != nil {
			t.Fatalf("reading the Machine again: %v", err)
		}
		granted := *current
		granted.Status.Phase = "Draining"
		body, _ := json.Marshal(&granted)
		if err := client.RequestJSON(http.MethodPut, kubernetes.MachinesPath+"/node-1/status", body, nil); err != nil {
			t.Fatalf("writing the Machine status as another writer: %v", err)
		}
		anotherWrite := woke()

		if ownWrite || !anotherWrite {
			t.Errorf("woke on the own write %v and on another writer's %v; want false, true", ownWrite, anotherWrite)
		}
	})
}
