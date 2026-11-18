package main

// Tests of a cluster that has no CRD for Television or Display. The
// watch of such a kind has an empty store, and a pass sends no list
// request for the kind.

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// defineTelevisions makes the fake API serve the Television definition.
func (a *cecAPI) defineTelevisions() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.noTelevisionDefinition = false
}

// A node workload on a cluster with no Television CRD, or whose API
// server refuses the node's field selector on Displays, lists that kind
// once at start. The passes that CECBus events start read the kind from
// the store and send no list request.
func TestTheNodeListsAnAbsentKindOnlyAtItsStart(t *testing.T) {
	cases := []struct {
		name string
		path string
		set  func(*cecAPI)
	}{
		{"no Television definition", televisionsPath, func(api *cecAPI) { api.noTelevisionDefinition = true }},
		{"the node's Display selector refused", displaysPath, func(api *cecAPI) { api.noDisplayNodeField = true }},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := startCECAPI(t)
				c.set(api)
				api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
				bus := controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"})
				api.putBus(bus)
				node := runNode(t, api)
				awaitStores(t, node.televisions)
				before := api.readCountOf(c.path)

				for range 5 {
					api.putBus(bus)
					synctest.Wait()
				}

				mustMatch(t, api.readCountOf(c.path), before)
			})
		})
	}
}

// On a cluster without the Television definition, the Deployment's loop
// reads Televisions from its store on each pass and sends no list
// request for them.
func TestTheLoopListsAnAbsentKindOnlyAtItsStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.noTelevisionDefinition = true
		api.putBus(*busWith(CECControl, []string{"node-1"}))
		controller := newCECBusController(api.client, testNamespace)
		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			controller.run(ctx, testMetrics(t))
		}()
		t.Cleanup(func() { cancel(); <-stopped })
		awaitStores(t, controller.buses, controller.televisions, controller.displays, controller.receivers)
		synctest.Wait()
		before := api.readCountOf(televisionsPath)

		for range 3 {
			api.nudge()
			synctest.Wait()
		}

		mustMatch(t, api.readCountOf(televisionsPath), before)
	})
}

// When the CRD of an absent kind is created, the watch's store fills
// with the kind's objects and then follows their changes, including a
// deletion.
func TestAnAbsentKindArrivesInTheStore(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.noTelevisionDefinition = true
		held := &watchStore{}
		runHeldWatch(t, api.client, watchTelevisions, nil, held)
		awaitStores(t, held)
		mustMatch(t, len(held.view().Store.ListKeys()), 0)

		api.defineTelevisions()
		api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})
		time.Sleep(optionalRecheck)

		api.waitUntil(t, "the store to hold the Television", func() bool {
			return len(held.view().Store.ListKeys()) == 1
		})
		mustSucceed(t, DeleteTelevision(api.client, "lounge"))
		api.waitUntil(t, "the store to drop the Television", func() bool {
			return len(held.view().Store.ListKeys()) == 0
		})
	})
}
