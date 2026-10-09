package main

// These tests run the reconcile loop in a synctest bubble with no
// ticker, and hand it the machine's readers as channels the test
// sends on, because a test cannot make the kernel send a uevent.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/liken-sh/liken/liken/machine"
)

// plugInSoundCard writes an audio controller into the fake sysfs: a
// driven pci device with one ALSA control node beneath it.
func plugInSoundCard(t *testing.T, sysRoot string) {
	t.Helper()
	plugInSoundCardAt(t, sysRoot, "0000:00:1f.3", 0)
}

// plugInSoundCardAt writes one audio controller at a pci address, with
// the control node of card number index.
func plugInSoundCardAt(t *testing.T, sysRoot, address string, index int) {
	t.Helper()
	card := filepath.Join(sysRoot, "bus", "pci", "devices", address)
	node := filepath.Join(card, "sound", fmt.Sprintf("card%d", index), fmt.Sprintf("controlC%d", index))
	for _, dir := range []string{node, filepath.Join(sysRoot, "bus", "pci", "drivers", "snd_hda_intel"), filepath.Join(sysRoot, "class", "sound")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(card, "modalias"): "pci:v00008086d000054C8",
		filepath.Join(node, "dev"):      fmt.Sprintf("116:%d", index),
		filepath.Join(node, "uevent"):   fmt.Sprintf("DEVNAME=snd/controlC%d", index),
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		filepath.Join(card, "driver"):    filepath.Join(sysRoot, "bus", "pci", "drivers", "snd_hda_intel"),
		filepath.Join(node, "subsystem"): filepath.Join(sysRoot, "class", "sound"),
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
}

// sliceDevices reads the names in node-1's ResourceSlice. A machine
// with nothing to publish has no slice.
func sliceDevices(t *testing.T, l *loop) []string {
	t.Helper()
	slice := &kubernetes.ResourceSlice{}
	err := l.objects.client.RequestJSON(http.MethodGet, kubernetes.ResourceSlicesPath+"/node-1-liken.sh", nil, slice)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range slice.Spec.Devices {
		names = append(names, d.Name)
	}
	return names
}

// startLoop runs the loop until the test ends, and answers a channel
// that carries what run returned.
func startLoop(t *testing.T, l *loop) <-chan error {
	t.Helper()
	current, err := l.objects.machine("node-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- l.run(ctx, current) }()
	return done
}

// A device that arrives after the first pass reaches the slice on the
// uevent the kernel sends for it, one quiet second later, with no
// ticker.
func TestAUeventWakesAPassThatPublishesTheNewDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
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

// A device that enumerates sends a burst of uevents, and the burst
// makes one pass, not one pass for each event.
func TestABurstOfUeventsSettlesIntoOnePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
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

// Another process that rewrites /etc/hosts sends an inotify event, and
// the pass it wakes writes the declared file back.
func TestAHostsEventWakesAPassThatWritesTheFileBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		l, _ := testLoop(t, newPassAPI(), &fakeFactsWatch{wake: make(chan struct{}, 1)})
		hosts := make(chan struct{}, 1)
		l.hosts = hosts
		startLoop(t, l)
		synctest.Wait()
		written, err := os.ReadFile(hostsPath)
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(hostsPath, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		hosts <- struct{}{}
		time.Sleep(time.Second)
		synctest.Wait()

		if got, err := os.ReadFile(hostsPath); err != nil || string(got) != string(written) {
			t.Errorf("hosts reads %q after the event, want the pass's %q", got, written)
		}
	})
}

// A reader that stops ends the loop with an error that names it, and
// main ends the process on that error.
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
				seedSysctls(t)
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
						t.Errorf("run answered %v, want %q", err, tc.want)
					}
				default:
					t.Error("the loop kept running after its reader stopped")
				}
			})
		})
	}
}

// A facts watch that dies ends the loop, the same as a reader.
func TestALoopEndsWhenTheFactsWatchDies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		dying := &fakeFactsWatch{wake: make(chan struct{})}
		l, _ := testLoop(t, newPassAPI(), dying)
		done := startLoop(t, l)
		synctest.Wait()

		close(dying.wake)
		synctest.Wait()

		select {
		case err := <-done:
			if !errors.Is(err, errFactsStopped) {
				t.Errorf("run answered %v, want %v", err, errFactsStopped)
			}
		default:
			t.Error("the loop kept running after the facts watch died")
		}
	})
}

// A facts watch that cannot open ends the loop before its first pass.
// init publishes the facts before the operator can start, so the
// error means something is wrong with the machine.
func TestALoopEndsWhenTheFactsWatchCannotOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		api := &countingMachineReads{next: newPassAPI()}
		l, _ := testLoop(t, api)
		l.watchFactsTree = func(context.Context) (*factsWatch, error) { return nil, os.ErrPermission }

		err := <-startLoop(t, l)

		if !errors.Is(err, os.ErrPermission) || api.reads.Load() != 1 {
			t.Errorf("run answered %v after %d passes, want %v and none", err, api.reads.Load()-1, os.ErrPermission)
		}
	})
}

// The relay answers nil when its context ends, so a loop that stops
// for any other reason reads no error from it.
func TestARelayEndsQuietlyWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- relay(ctx, make(chan struct{}), make(chan struct{}, 1), nil) }()

		cancel()

		if err := <-done; err != nil {
			t.Errorf("relay answered %v, want nil", err)
		}
	})
}

// The seams open the real readers. The uevent listener opens as any
// user, and the hosts watch opens on the directory of hostsPath.
func TestTheSeamsOpenTheRealReaders(t *testing.T) {
	isolatePass(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if _, err := watchHostsFile(ctx); err != nil {
		t.Errorf("watchHostsFile: %v", err)
	}
	if _, err := listenForUevents(ctx); err != nil {
		t.Errorf("listenForUevents: %v", err)
	}
}

// A pass that only the ticker woke reads neither sysfs nor the hosts
// file. A device that arrives with no uevent, and a hosts file that
// changes with no inotify event, both wait for their event, and the
// uevent's pass then publishes the device.
func TestATickReadsNeitherSysfsNorTheHostsFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		l, _ := testLoop(t, newPassAPI(), &fakeFactsWatch{wake: make(chan struct{}, 1)})
		ticks := make(chan time.Time)
		uevents := make(chan struct{}, 1)
		l.ticks, l.uevents = ticks, uevents
		startLoop(t, l)
		synctest.Wait()

		plugInSoundCard(t, draSysfsRoot)
		if err := os.WriteFile(hostsPath, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ticks <- time.Now()
		synctest.Wait()
		afterTick := sliceDevices(t, l)
		hosts, err := os.ReadFile(hostsPath)
		if err != nil {
			t.Fatal(err)
		}
		uevents <- struct{}{}
		time.Sleep(time.Second)
		synctest.Wait()

		if len(afterTick) != 0 || string(hosts) != "127.0.0.1 localhost\n" {
			t.Errorf("the tick's pass published %q and left hosts %q, want nothing read", afterTick, hosts)
		}
		if got := sliceDevices(t, l); !slices.Equal(got, []string{"pci-0000-00-1f-3"}) {
			t.Errorf("the uevent's pass published %q, want the sound card", got)
		}
	})
}

// A tick reuses a read only when its last attempt succeeded. A read
// that failed runs again on the tick, so the tick's pass records the
// failure again and the retry timer stays set.
func TestLocalReadsReuseOnlyASuccessOnATick(t *testing.T) {
	cases := []struct {
		name     string
		tickOnly bool
		lastErr  error
		reads    int
	}{
		{"a tick after a success", true, nil, 1},
		{"a tick after a failure", true, os.ErrPermission, 2},
		{"another wake after a success", false, nil, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := &localReads{}
			reads := 0
			apply := func(err error) func() ([]machine.HostEntry, error) {
				return func() ([]machine.HostEntry, error) { reads++; return nil, err }
			}
			_, _ = l.hostEntries(apply(tc.lastErr))
			l.walked(tc.lastErr == nil)
			l.tickOnly = tc.tickOnly

			_, _ = l.hostEntries(apply(nil))

			if reads != tc.reads || l.walk() != (tc.reads == 2) {
				t.Errorf("read the hosts file %d times and walk = %v, want %d and %v", reads, l.walk(), tc.reads, tc.reads == 2)
			}
		})
	}
}

// A slice that somebody deletes wakes the loop, so the pass writes it
// again. The pass walks sysfs only on a wake that is not the ticker's,
// so without this wake the slice would stay deleted. An update, which
// is what the operator's own write is, wakes nothing.
func TestADeletedSliceWakesTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		fake := newPassAPI()
		client, watcher := passClients(t, fake)
		slice, _ := json.Marshal(fakeapi.Object("resource.k8s.io/v1", "ResourceSlice", "", "node-1-liken.sh", nil))
		if err := client.RequestJSON(http.MethodPost, kubernetes.ResourceSlicesPath, slice, nil); err != nil {
			t.Fatal(err)
		}
		wakes := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := watchThisMachine(ctx, watcher, client, "node-1", "lab", watch.Signal(wakes), func(string) {})
		awaitCopies(t, r)
		<-wakes

		if err := client.RequestJSON(http.MethodPut, kubernetes.ResourceSlicesPath+"/node-1-liken.sh", slice, nil); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		wokeOnUpdate := len(wakes) > 0
		if err := client.RequestJSON(http.MethodDelete, kubernetes.ResourceSlicesPath+"/node-1-liken.sh", nil, nil); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		if wokeOnUpdate || len(wakes) != 1 {
			t.Errorf("woke on the update: %v, woke on the delete: %v; want only the delete", wokeOnUpdate, len(wakes) == 1)
		}
	})
}

// refusingSlices answers every write of a ResourceSlice with a 503 and
// counts them.
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

// A slice write that failed leaves the walk not current, so the next
// tick walks again and writes again, until a write lands.
func TestATickWalksAgainAfterASliceWriteFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		plugInSoundCard(t, draSysfsRoot)
		api := &refusingSlices{next: newPassAPI()}
		l, _ := testLoop(t, api, &fakeFactsWatch{wake: make(chan struct{}, 1)})
		ticks := make(chan time.Time)
		l.ticks = ticks
		startLoop(t, l)
		synctest.Wait()
		first := api.writes.Load()

		ticks <- time.Now()
		synctest.Wait()

		if first != 1 || api.writes.Load() != 2 {
			t.Errorf("the slice was written %d times by the first pass and %d after a tick, want 1 and 2", first, api.writes.Load())
		}
	})
}

// A facts watch whose Sync fails logs it and still runs the pass.
func TestAPassRunsWhenTheFactsWatchCannotSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
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

// A facts watch that is already dead when the loop starts ends the
// loop before its first pass, because the pass would read a tree that
// nothing watches.
func TestALoopEndsBeforeItsFirstPassOnADeadFactsWatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		api := &countingMachineReads{next: newPassAPI()}
		dead := &fakeFactsWatch{wake: make(chan struct{})}
		close(dead.wake)
		l, _ := testLoop(t, api, dead)

		err := <-startLoop(t, l)

		if !errors.Is(err, errFactsStopped) || api.reads.Load() != 1 {
			t.Errorf("run answered %v after %d passes, want %v and none", err, api.reads.Load()-1, errFactsStopped)
		}
	})
}

// A process that keeps writing another value to a file the pass owns
// makes a pass every five seconds at most, not one for each write.
func TestAFightOverAFileMakesAPassEveryFiveSecondsAtMost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
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
