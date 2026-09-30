package main

// The handlers of the watches, alone and through client-go's real
// reflector against the scripted server in watchserver_test.go.

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

// receiverAt is a Receiver at one generation, with a status the
// operator wrote.
func receiverAt(uid string, generation int64, address string) Receiver {
	return Receiver{
		APIVersion: equipmentAPIVersion,
		Kind:       "Receiver",
		Metadata:   ObjectMeta{Name: "theater", UID: uid, Generation: generation},
		Spec:       ReceiverSpec{Inputs: []ReceiverInput{{Name: "GAME"}}},
		Status:     ReceiverStoredStatus{ReceiverStatus: ReceiverStatus{Address: address}, PowerGeneration: 3},
	}
}

// displayAt is a Display on one node at one physical address.
func displayAt(uid, node, physicalAddress string) Display {
	display := Display{Metadata: ObjectMeta{Name: "den-hdmi-1", UID: uid}}
	display.Status.Node = node
	display.Status.PhysicalAddress = physicalAddress
	return display
}

// An object converts into the operator's own struct, its embedded
// status included, and one that does not convert is an error that
// names it. A tombstone, which the informer hands a handler for an
// object deleted while the watch was down, converts as the object it
// holds.
func TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt(t *testing.T) {
	good := receiverAt("uid-1", 2, "192.0.2.10")
	mistyped := asObject(t, good)
	if err := unstructured.SetNestedField(mistyped.Object, "two", "metadata", "generation"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		object  any
		wantErr string
	}{
		{name: "an object", object: asObject(t, good)},
		{name: "a tombstone", object: cache.DeletedFinalStateUnknown{Key: "theater", Obj: asObject(t, good)}},
		{name: "a field of the wrong type", object: mistyped, wantErr: "Receiver theater does not convert"},
		{name: "a tombstone with no copy", object: cache.DeletedFinalStateUnknown{Key: "theater"}, wantErr: "the tombstone for theater holds no copy"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				got, err := informer.Convert[Receiver](c.object)
				if c.wantErr == "" {
					mustSucceed(t, err)
					mustMatch(t, got.Metadata.Generation, 2)
					mustMatch(t, got.Status.Address, "192.0.2.10")
					mustMatch(t, got.Spec.Inputs[0].Name, "GAME")
					return
				}
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("convert error = %v, want one that says %q", err, c.wantErr)
				}
			})
		})
	}
}

// A marked watch wakes the loop for a new object and a removed one, and
// for an update only when the part the loop reads moved. An object
// that does not convert wakes it too, because nothing says what
// changed.
func TestAMarkedWatchWakesOnlyWhenTheMarkMoves(t *testing.T) {
	receivers := func(wake chan<- struct{}) cache.ResourceEventHandler {
		return markHandler[Receiver, specMark]{what: "the Receivers", wake: wake, mark: receiverSpecMark}.handler()
	}
	displays := func(wake chan<- struct{}) cache.ResourceEventHandler {
		return markHandler[Display, displayPlace]{what: "the Displays", wake: wake, mark: displayMark}.handler()
	}
	mistyped := asObject(t, receiverAt("uid-1", 1, ""))
	_ = unstructured.SetNestedField(mistyped.Object, "one", "metadata", "generation")
	cases := []struct {
		name    string
		handler func(chan<- struct{}) cache.ResourceEventHandler
		send    func(cache.ResourceEventHandler)
		wakes   int
	}{
		{"a new Receiver", receivers, func(h cache.ResourceEventHandler) {
			h.OnAdd(asObject(t, receiverAt("uid-1", 1, "")), false)
		}, 1},
		{"a removed Receiver", receivers, func(h cache.ResourceEventHandler) {
			h.OnDelete(asObject(t, receiverAt("uid-1", 1, "")))
		}, 1},
		{"a removal whose tombstone holds no copy", receivers, func(h cache.ResourceEventHandler) {
			h.OnDelete(cache.DeletedFinalStateUnknown{Key: "theater"})
		}, 1},
		{"a Receiver status write", receivers, func(h cache.ResourceEventHandler) {
			h.OnUpdate(asObject(t, receiverAt("uid-1", 1, "")), asObject(t, receiverAt("uid-1", 1, "192.0.2.10")))
		}, 0},
		{"a Receiver spec edit", receivers, func(h cache.ResourceEventHandler) {
			h.OnUpdate(asObject(t, receiverAt("uid-1", 1, "")), asObject(t, receiverAt("uid-1", 2, "")))
		}, 1},
		{"a Receiver created again with the same name", receivers, func(h cache.ResourceEventHandler) {
			h.OnUpdate(asObject(t, receiverAt("uid-1", 1, "")), asObject(t, receiverAt("uid-2", 1, "")))
		}, 1},
		{"a Receiver that does not convert", receivers, func(h cache.ResourceEventHandler) {
			h.OnUpdate(asObject(t, receiverAt("uid-1", 1, "")), mistyped)
		}, 1},
		{"a held copy that did not convert", receivers, func(h cache.ResourceEventHandler) {
			h.OnUpdate(mistyped, asObject(t, receiverAt("uid-1", 1, "")))
		}, 1},
		{"a Display write that keeps its place", displays, func(h cache.ResourceEventHandler) {
			before := asObject(t, displayAt("uid-1", "node-1", "1.0.0.0"))
			after := asObject(t, displayAt("uid-1", "node-1", "1.0.0.0"))
			_ = unstructured.SetNestedField(after.Object, "1920x1080", "status", "mode")
			h.OnUpdate(before, after)
		}, 0},
		{"a Display at a new address", displays, func(h cache.ResourceEventHandler) {
			h.OnUpdate(asObject(t, displayAt("uid-1", "node-1", "1.0.0.0")), asObject(t, displayAt("uid-1", "node-1", "2.0.0.0")))
		}, 1},
		{"a Display on a new node", displays, func(h cache.ResourceEventHandler) {
			h.OnUpdate(asObject(t, displayAt("uid-1", "node-1", "1.0.0.0")), asObject(t, displayAt("uid-1", "node-2", "1.0.0.0")))
		}, 1},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wake := make(chan struct{}, 1)

				c.send(c.handler(wake))

				mustMatch(t, len(wake), c.wakes)
			})
		})
	}
}

// Through the reflector: each watch wakes the loop when its first read
// is done, even a read of an empty collection. The pass can read a
// collection before the watch does, and an object removed between the
// two reads is in neither the watch's read nor any event.
func TestEachWatchWakesTheLoopWhenItsFirstReadIsDone(t *testing.T) {
	const displayAPIVersion = "display.liken.sh/v1alpha1"
	cases := []struct {
		name       string
		path       string
		apiVersion string
		kind       string
		watch      watchFunc
	}{
		{"the Receivers", receiversPath, equipmentAPIVersion, "Receiver", func(ctx context.Context, client *Client, wake chan<- struct{}, _ func(), held *watchStore) {
			watchReceivers(ctx, client, wake, nil, testMetrics(t), held)
		}},
		{"the Receivers' specs", receiversPath, equipmentAPIVersion, "Receiver", watchReceiverSpecs},
		{"the CECBuses", cecBusesPath, equipmentAPIVersion, "CECBus", watchCECBuses},
		{"the Televisions", televisionsPath, equipmentAPIVersion, "Television", watchTelevisions},
		{"the Displays", displaysPath, displayAPIVersion, "Display", watchDisplays},
		{"the Displays of one machine", displaysPath, displayAPIVersion, "Display", nodeDisplays("node-1")},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				server := newWatchServer(c.path, c.apiVersion, c.kind, []string{"[]"})

				wakes := runWatch(t, startWatchServer(t, server), c.watch, nil)

				server.awaitWatches(t, 1)
				awaitWake(t, wakes, testTimeout)
			})
		})
	}
}

// sharedSpecWake runs the Deployment's shared Receiver watch, and
// answers its wake of the CECBus loop as the watch's wake.
func sharedSpecWake(t *testing.T) watchFunc {
	return func(ctx context.Context, client *Client, wake chan<- struct{}, _ func(), held *watchStore) {
		watchReceivers(ctx, client, make(chan struct{}, 1), wake, testMetrics(t), held)
	}
}

// Through the reflector: a watch that reads only a Receiver's spec
// wakes the loop for a spec edit and not for a status write, and a
// watch that reads the whole Receiver wakes it for both. The
// Deployment's shared watch wakes the CECBus loop as the spec watch
// does.
func TestAReceiverEventWakesTheLoopThatReadsIt(t *testing.T) {
	cases := []struct {
		name  string
		after Receiver
		watch watchFunc
		want  bool
	}{
		{"a status write, to the spec watch", receiverAt("uid-1", 1, "192.0.2.10"), watchReceiverSpecs, false},
		{"a spec edit, to the spec watch", receiverAt("uid-1", 2, ""), watchReceiverSpecs, true},
		{"a status write, to the Receiver loop's watch", receiverAt("uid-1", 1, "192.0.2.10"), func(ctx context.Context, client *Client, wake chan<- struct{}, _ func(), held *watchStore) {
			watchReceivers(ctx, client, wake, nil, testMetrics(t), held)
		}, true},
		{"a status write, to the shared watch's CECBus wake", receiverAt("uid-1", 1, "192.0.2.10"), sharedSpecWake(t), false},
		{"a spec edit, to the shared watch's CECBus wake", receiverAt("uid-1", 2, ""), sharedSpecWake(t), true},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				changed := c.after
				changed.Metadata.ResourceVersion = "150"
				server := newWatchServer(receiversPath, equipmentAPIVersion, "Receiver",
					[]string{encodeAll(t, receiverAt("uid-1", 1, ""))},
					[]string{pause, event(t, "MODIFIED", changed), holdOpen})
				wakes := runWatch(t, startWatchServer(t, server), c.watch, nil)
				server.awaitWatches(t, 1)
				settleWakes(wakes, watchQuiet)

				server.release()

				mustMatch(t, wokeWithin(wakes, watchQuiet), c.want)
			})
		})
	}
}

// Through the reflector: a watch that closes at once makes the
// reflector read the collection again after its backoff. The informer
// reports what changed between the two reads, so an edit made while
// the watch was down wakes the loop, and a status write made in the
// same time does not.
func TestAChangeWhileTheWatchWasDownWakesTheLoopOnlyForAnEdit(t *testing.T) {
	cases := []struct {
		name  string
		after Receiver
		want  bool
	}{
		{"a status write", receiverAt("uid-1", 1, "192.0.2.10"), false},
		{"a spec edit", receiverAt("uid-1", 2, ""), true},
		{"a Receiver created again with the same name", receiverAt("uid-2", 1, ""), true},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				reads := []string{encodeAll(t, receiverAt("uid-1", 1, "")), encodeAll(t, c.after)}
				server := newWatchServer(receiversPath, equipmentAPIVersion, "Receiver", reads, []string{}, []string{holdOpen})
				wakes := runWatch(t, startWatchServer(t, server), watchReceiverSpecs, nil)
				server.awaitWatches(t, 1)
				settleWakes(wakes, watchQuiet)

				server.awaitWatches(t, 1)

				mustMatch(t, wokeWithin(wakes, watchQuiet), c.want)
			})
		})
	}
}

// Through the reflector: a watch that ends is opened again, and each
// watch opened after the first is counted.
func TestAWatchThatEndsIsOpenedAgainAndCounted(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		kind  string
		watch watchFunc
	}{
		{"CECBus", cecBusesPath, "CECBus", watchCECBuses},
		{"Television", televisionsPath, "Television", watchTelevisions},
		{"Display", displaysPath, "Display", nodeDisplays("node-1")},
		{"Receiver spec", receiversPath, "Receiver", watchReceiverSpecs},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				server := newWatchServer(c.path, equipmentAPIVersion, c.kind, []string{"[]"}, []string{}, []string{holdOpen})
				restarts := make(chan struct{}, 64)

				runWatch(t, startWatchServer(t, server), c.watch, func() { restarts <- struct{}{} })

				server.awaitWatches(t, 2)
				select {
				case <-restarts:
				case <-time.After(testTimeout):
					t.Fatal("the reopened watch was not counted")
				}
				mustMatch(t, len(restarts), 0)
			})
		})
	}
}

// The watches reach the API server the way the Client's own requests
// do: over TLS that trusts the ServiceAccount's CA, with its token. A
// CA file that is missing ends the watch at once, with the error in
// the log.
func TestTheWatchesUseTheServiceAccount(t *testing.T) {
	t.Parallel()
	server := newWatchServer(cecBusesPath, equipmentAPIVersion, "CECBus", []string{"[]"})
	tokens := make(chan string, 64)
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens <- r.Header.Get("Authorization")
		server.handler(t).ServeHTTP(w, r)
	}))
	t.Cleanup(api.Close)
	credentials := t.TempDir()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw})
	mustSucceed(t, os.WriteFile(filepath.Join(credentials, "ca.crt"), caPEM, 0o600))
	mustSucceed(t, os.WriteFile(filepath.Join(credentials, "token"), []byte("watch-token"), 0o600))

	wakes := runWatch(t, NewClient(api.URL, http.DefaultClient, credentials), watchCECBuses, nil)

	awaitWake(t, wakes, testTimeout)
	mustMatch(t, <-tokens, "Bearer watch-token")

	missing := NewClient(api.URL, http.DefaultClient, t.TempDir())
	watchCECBuses(t.Context(), missing, make(chan struct{}, 1), nil, nil)
	_, err := missing.watcher()
	mustFail(t, err)
}

// A Receiver as the API server sends it converts to the same struct
// that the Client's own JSON decode gives, so a pass that reads the
// store acts on what a list would have given it. The API server sends
// a whole number in a float field as an integer, and the watch decodes
// it as one.
func TestAReceiverFromTheWatchIsTheReceiverAListGives(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const stored = `{
		"apiVersion": "equipment.liken.sh/v1alpha1", "kind": "Receiver",
		"metadata": {"name": "theater", "uid": "uid-1", "generation": 4, "resourceVersion": "812", "labels": {"liken.sh/discovered": "wiim"}},
		"spec": {
			"denon": {"address": "receiver.example", "settings": {"tone": {"control": true, "bass": -2}, "channelVolumes": {"FL": 1, "C": 0.5}}},
			"wiim": {"uuid": "uuid:0000-1111"},
			"volume": {"max": 75, "step": 1},
			"inputs": [{"name": "GAME", "machine": "node-1", "monitor": "hdmi-a-1", "soundMode": "MULTI CH IN"}],
			"session": {"player": "house/theater", "input": "GAME", "volumeTopic": "liken/media/house/theater/volume", "active": true}
		},
		"status": {
			"address": "192.0.2.10", "driver": "denon",
			"zones": {"main": {"power": "ON", "input": "GAME", "volume": "-30.5", "sleep": 30}},
			"denon": {"system": {"power": "ON", "speakerPreset": 1}, "tone": {"treble": 3}, "channelVolumes": {"SW": 2}},
			"wiim": {"audio": {"balance": 0, "maxVolume": 100}, "playback": {"mode": 10}},
			"session": {"player": "house/theater", "input": "GAME", "active": true},
			"powerGeneration": 3,
			"conditions": [{"type": "Connected", "status": "True", "reason": "Answered", "lastTransitionTime": "2026-09-27T12:00:00Z"}]
		}
	}`
		var listed Receiver
		mustSucceed(t, json.Unmarshal([]byte(stored), &listed))
		watched := &unstructured.Unstructured{}
		mustSucceed(t, watched.UnmarshalJSON([]byte(stored)))

		converted, err := informer.Convert[Receiver](watched)

		mustSucceed(t, err)
		mustDeepEqual(t, converted, listed)
	})
}
