package main

// A cluster can lack the Television or the Display definition, and the
// watch of such a kind reads it as empty, with no request at each pass.

import (
	"context"
	"testing"
	"time"
)

// defineTelevisions installs the Television definition on the fake.
func (a *cecAPI) defineTelevisions() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.noTelevisionDefinition = false
}

// A node workload on a cluster without the Television definition, or
// whose Display definition refuses the node's field selector, reads
// that kind at its start and then from its store. The passes that the
// CECBus events wake send no more lists of it.
func TestTheNodeListsAnAbsentKindOnlyAtItsStart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		set  func(*cecAPI)
	}{
		{"no Television definition", televisionsPath, func(api *cecAPI) { api.noTelevisionDefinition = true }},
		{"the node's Display selector refused", displaysPath, func(api *cecAPI) { api.noDisplayNodeField = true }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
				time.Sleep(20 * time.Millisecond)
			}

			mustMatch(t, api.readCountOf(c.path), before)
		})
	}
}

// The Deployment's loop on a cluster without the Television definition
// reads the Televisions from its store at each tick of its clock, and
// sends no list of them.
func TestTheLoopListsAnAbsentKindOnlyAtItsStart(t *testing.T) {
	shorten(t, &cecBusClock, 5*time.Millisecond)
	api := startCECAPI(t)
	api.noTelevisionDefinition = true
	api.putBus(*busWith(CECControl, []string{"node-1"}))
	controller := newCECBusController(api.client)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		controller.run(ctx, testMetrics(t))
	}()
	t.Cleanup(func() { cancel(); <-stopped })
	awaitStores(t, controller.buses, controller.televisions, controller.displays, controller.receivers)
	time.Sleep(20 * time.Millisecond)
	before := api.readCountOf(televisionsPath)

	time.Sleep(100 * time.Millisecond)

	mustMatch(t, api.readCountOf(televisionsPath), before)
}

// A watch of a kind whose definition is absent holds an empty store,
// holds the kind's objects once the definition arrives, and then
// follows them, a deletion included.
func TestAnAbsentKindArrivesInTheStore(t *testing.T) {
	shorten(t, &optionalRecheck, 20*time.Millisecond)
	api := startCECAPI(t)
	api.noTelevisionDefinition = true
	held := &watchStore{}
	runHeldWatch(t, api.client, watchTelevisions, nil, held)
	awaitStores(t, held)
	mustMatch(t, len(held.view().Store.ListKeys()), 0)

	api.defineTelevisions()
	api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})

	api.waitUntil(t, "the store to hold the Television", func() bool {
		return len(held.view().Store.ListKeys()) == 1
	})
	mustSucceed(t, DeleteTelevision(api.client, "lounge"))
	api.waitUntil(t, "the store to drop the Television", func() bool {
		return len(held.view().Store.ListKeys()) == 0
	})
}
