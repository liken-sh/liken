// display-operator publishes each monitor output of a graphics card as
// its own DRA device under the driver name display.liken.sh. A pod
// claims one screen by its connector name or by the monitor's identity,
// and gets a Wayland socket that puts its window on that screen.
//
// The operator's own pod claims the card through an ordinary liken.sh
// claim for the card's display device. Only a machine with a graphics
// card publishes that device, so the claim schedules the pod onto the
// machines with monitors, and no node selector is needed. liken
// publishes the card as an exclusive device, so the pod that holds the
// claim is the only program that sets a mode on the card. The scheduler
// also allocates each screen to one claim, so a pod cannot take a screen
// that another claim holds by writing its name in a config file.
//
// Weston runs with ivi-shell in its own container in the same pod, with
// the operator's controller module loaded. When the operator restarts
// Weston, the compositor container starts it again without exiting.
// When Weston crashes, the container exits and the kubelet restarts it
// (restartrequests.go).
//
// The operator uses only interfaces that every DRA driver has: the
// claim, the ResourceSlices it writes, and the CDI files it writes for
// the container runtime.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

const (
	// settleWindow is how long the loop waits for no new event before it
	// writes the ResourceSlice. Plugging in one monitor produces a burst of
	// uevents, and one write should cover the whole burst. Each
	// ResourceSlice write wakes every pod in the cluster that waits for DRA
	// allocation, because the scheduler's event for a slice change has no
	// queueing hint, so a flapping cable must not cause a write per event.
	settleWindow = 1500 * time.Millisecond

	// settleLimit is the longest that a burst can delay the write, counted
	// from its first event.
	settleLimit = 10 * time.Second

	// backstopInterval is how often the loop runs a pass with no event, to
	// cover a change that no event reported. A lost uevent and a failed
	// write each wake the loop on their own (uevents.go, writeRetryDelay).
	// The Display controller uses the same interval to retry the DDC/CI
	// probe of a panel that refused it (panels.go), and to sweep the
	// Display resources of panels that left while the node's set of panels
	// did not change.
	backstopInterval = 60 * time.Second

	// writeRetryDelay is how long the loop waits after a failed write
	// before it runs another pass. The retry covers a conflict with another
	// writer and an API server that is restarting. The wait is a timer that
	// requests a pass, because a sleep would block the loop, which also
	// watches the compositor and the pod's shutdown.
	writeRetryDelay = 2 * time.Second
)

// componentName is the component label of this process's
// liken_build_info metric. Every liken component sets that label, so
// one dashboard can list the release of each component in the cluster.
const componentName = "display-operator"

// version is the release of this build. The Dockerfile sets it with
// -ldflags, so liken_build_info reports the release that a pod runs.
var version = "dev"

// westonConfigPath is weston.ini. The declare container writes it and
// the compositor container reads it, through a volume that only this
// pod's containers share. It describes the monitors that this pod
// found, so no manifest supplies it, and an edit by hand ends with the
// pod. It is a variable so that tests can move it.
var westonConfigPath = "/etc/weston/weston.ini"

// modeRecordPath records the mode that each claim asked for, in the
// same volume as weston.ini. The operator writes weston.ini from this
// record and the connectors. The declare container reads the record
// too, so a compositor that restarts comes back at the modes of the
// claims that are still held. The volume ends with the pod, so a new
// pod starts every monitor at its preferred mode.
var modeRecordPath = "/etc/weston/modes.json"

// procRoot is the process tree that the operator reads to find the
// compositor. The pod has one process namespace, so the compositor's
// process is in this tree. It is a variable so that tests can move it.
var procRoot = "/proc"

// defaultSocketDir holds the compositor's Wayland sockets, and each
// consumer's container mounts it. The CDI mount uses one path for both
// sides, so the path is the same in this pod and in the consumer's
// container.
const defaultSocketDir = "/var/run/display.liken.sh"

// socketName is the compositor's own socket in that directory, the
// value of weston's --socket flag. It is a constant and not read from
// WAYLAND_DISPLAY, because a WAYLAND_DISPLAY in the operator pod's
// environment would move the socket that the output watch connects to,
// and the watch would report a compositor that serves nothing.
//
// Each prepared claim gets its own socket, which the layout module
// opens and names after the claim's UID. A pod whose environment sets
// WAYLAND_DISPLAY=wayland-0 connects to this socket instead, and its
// surfaces belong to no claim.
const socketName = "wayland-0"

// sysRoot is the sysfs mount that the operator reads. It is a variable
// so the tests can point it at a directory they control.
var sysRoot = "/sys"

// driRoot is the directory where the claim puts the card node. It is a
// variable so that tests can move it.
var driRoot = "/dev/dri"

// main selects the role of the process from its first argument, so one
// binary runs every container of the display pods: the config writer,
// the compositor, the capture sidecar, the API, and the OpenAPI
// printer. With no argument, the process runs the driver.
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case declareMode:
			declare()
			return
		case compositorMode:
			compose()
			return
		case captureMode:
			serveCaptureSidecar()
			return
		case apiMode:
			serveAPI()
			return
		case openapiMode:
			printOpenAPI()
			return
		}
	}
	operate()
}

// claimedCard returns the card node that the pod's claim put in
// driRoot, and exits unless there is exactly one. Each role reads the
// directory and takes no device path from configuration, because the
// kernel renumbers cards across a reboot.
func claimedCard() string {
	cards, err := cardNode(driRoot)
	if err != nil {
		fatal("reading %s: %v", driRoot, err)
	}
	switch len(cards) {
	case 1:
	case 0:
		fatal("no card node in %s, so this pod has no claim for a display device", driRoot)
	default:
		fatal("this pod holds %d card nodes (%v), and one compositor drives one card", len(cards), cards)
	}
	return cards[0]
}

func operate() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The DaemonSet sets NODE_NAME from spec.nodeName through the downward
	// API. The ResourceSlice names the node that it describes.
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		fatal("NODE_NAME is not set, and the DaemonSet must set it from spec.nodeName")
	}
	socketDir := envOr("SOCKET_DIR", defaultSocketDir)
	fmt.Printf("%s: operating the monitors on %s\n", DriverName, nodeName)

	// An empty METRICS_ADDR disables the listener, so a run on a
	// workstation and every test in this repository listen on no port.
	readings := newMetrics(componentName, version)
	metricsListener, err := readings.listen(envOr("METRICS_ADDR", defaultMetricsAddr))
	if err != nil {
		fatal("opening the metrics listener: %v", err)
	}
	if metricsListener != nil {
		go serveMetrics(ctx, metricsListener, readings)
	}

	// A failure during setup ends the process. The kubelet restarts it with
	// backoff, so the setup needs no retry of its own, and the failure
	// shows as a restart in kubectl and not only in a log.
	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		fatal("reading the in-cluster configuration: %v", err)
	}
	watcher, err := informer.InCluster()
	if err != nil {
		fatal("reading the in-cluster configuration for the watches: %v", err)
	}
	owner, err := NodeOwner(client, nodeName)
	if err != nil {
		fatal("reading node %s: %v", nodeName, err)
	}

	// One recorder posts every Event of this operator. Its
	// reportingInstance is the pod's name, so a person can tell which
	// node's operator wrote an Event. notices finds the Display of a
	// connector for code that acts on a connector, once the Display store
	// is set below.
	recorder := events.New(ctx, client, componentName, events.Options{Instance: os.Getenv("POD_NAME")})
	notices := &screenNotices{node: nodeName}

	card := claimedCard()
	socketPath := socketDir + "/" + socketName

	// layout is the connection to the compositor's controller module. The
	// module opens a socket for each claim and reports every surface in the
	// compositor, and layout keeps the reports in a store that the
	// placement pass reads.
	layout := newLayoutLink(layoutSocketPath)

	// The plugin registers even while no compositor serves, so that a
	// prepare call that arrives then is refused with a reason. The kubelet
	// gets no response at all from a driver that is not registered.
	plugin := newDRAPlugin(client, card, socketDir, layout)
	plugin.metrics = readings
	plugin.notices = notices
	// A panel whose claim ended while the previous operator container ran
	// can still be waiting to go to standby. resumeReleases reads the power
	// record and starts their grace periods again.
	plugin.resumeReleases()

	// A compositor restart closes the module's connection and every claim
	// socket that the module opened. So on each new connection, the link
	// opens the sockets of the prepared claims again. The hook is set
	// before the link dials, because the first connection replays too.
	layout.replay = plugin.replaySockets
	go layout.run(ctx)

	uevents, err := listenForUevents(ctx)
	if err != nil {
		fatal("watching for kernel events: %v", err)
	}

	// A hotplug hold keeps publishing a disconnected connector's last
	// connected reading until a deadline (hotplughold.go). The end of a
	// hold and a failed write each request another pass through retries.
	retries := make(chan struct{}, 1)
	requestPass := func() {
		select {
		case retries <- struct{}{}:
		default:
		}
	}
	// countHotplugs counts each drm uevent before it wakes a pass, so the
	// hold can tell a connector that stayed disconnected from one that
	// reconnected and dropped again between two reads.
	hotplugs := newHotplugCounts(card)
	uevents = countHotplugs(ctx, uevents, hotplugs)
	hold := newCardHold(retries, hotplugs.count)

	// The Display controller and the placement pass share one read of the
	// card through screensView, so they see the same connectors. The slice
	// pass reads the card through its own view, sliceHold. The views share
	// the time that each connector first read disconnected, so each of them
	// reports a monitor that left at the same deadline.
	screensView := hold.view()
	screensOf := screensView.wrap(func() []Output {
		return screens(card, plugin.currentModes, plugin.connectorModes)
	})
	sliceHold := hold.view()

	// One link history serves the whole process, because it tracks each
	// connector's monitor across passes.
	links := newLinkHistory()
	// A restarted operator has no link history, and a machine that booted
	// with its panel on another input has no EDID in sysfs. The Display
	// resources outlive the operator, so the history starts from them.
	// When the list fails, the operator logs it and starts with an empty
	// history, and a disconnected screen has no identity until its monitor
	// reconnects.
	if displays, err := listDisplays(client); err != nil {
		fmt.Fprintf(os.Stderr, "reading the screens this node last served: %v\n", err)
	} else {
		links.loadFromDisplays(displays, nodeName, discoverOutputs(sysRoot, card))
	}
	// An operator that starts while a link is down reads that screen as
	// disconnected, and the Display controller would report the panel gone
	// at once. So screensView starts a hold on each screen that the history
	// remembers. A screen that reconnects before the hold ends changes
	// nothing, and one that does not is reported gone when the hold ends.
	screensView.assumeConnected(rememberedScreens(discoverOutputs(sysRoot, card), links))
	// The prepare path reads the same history as the slice pass, so a claim
	// for a disconnected screen is prepared exactly when the slice still
	// publishes that screen.
	plugin.links = links

	// The Display controller writes the Display resources. It reads the
	// same connectors as the slice pass and shares its probe cache,
	// plugin.controls, so no panel is probed twice. It runs its own loop,
	// so a panel override does not wait for settleWindow.
	panels := newDisplayControl(client, nodeName, plugin.controls, screensOf)
	panels.metrics = readings
	// A declared mode and a claim's mode both go through applyMode, so both
	// reach the compositor the same way and take the same lock. When the
	// compositor destroys an output and creates it again, the Display
	// controller repairs the canvas sizes with restartCompositor, which
	// restarts the compositor with no change to the config.
	panels.setMode = func(ctx context.Context, output Output, mode string) error {
		return plugin.applyMode(ctx, output, mode)
	}
	panels.restart = plugin.restartCompositor
	// The output watch keeps one connection open to the compositor's socket
	// and reads the registry, which reports an output that was destroyed
	// and created again. Every restart that the operator makes closes that
	// connection, so the operator's own restarts are not reported as moved
	// outputs.
	watch := newOutputWatch(socketPath, panels.outputsChanged)
	// After a mode switch, the prepare path uses the same connection to
	// wait for the restarted compositor to report the mode that the
	// claim asked for.
	plugin.served = watch.served
	// The same connection supplies status.mode.weston, the mode at which
	// the compositor serves canvases. status.mode.kernel is the mode
	// that the card is synced to.
	panels.served = watch.served
	// The output watch's connection also opens the card gate. With no
	// connection to a compositor, the gate refuses every read of the card,
	// so a read by the operator cannot take DRM master from a compositor
	// that is still starting. A new connection wakes the slice pass and the
	// Display controller, to fill the fields that the closed gate left
	// empty.
	plugin.gate.live = watch.serving
	connections := make(chan struct{}, 1)
	watch.connected = func() {
		select {
		case connections <- struct{}{}:
		default:
		}
		panels.wake()
	}
	// When a read of the card finds that the operator's own file became DRM
	// master, the compositor has no master and shows no frame (cardgate.go).
	// restartMasterless restarts it from its own goroutine, because
	// applyMode reads the card while it holds the lock that a restart takes.
	go plugin.restartMasterless(ctx, plugin.gate.masterless)
	go watch.run(ctx)

	// The placement pass places each screen's surfaces in the Layout that
	// the screen's Display names. It probes the same socket as the slice
	// pass, so when the compositor stops replying, the slice taints the
	// screens and the placement pass reports no surfaces.
	places := newPlacementPass(client, nodeName, socketPath, layout, plugin.claims, screensOf)
	places.metrics = readings

	// The pod, Layout, and Display watches wake the loops through one
	// channel, because each wake means read everything again, and one
	// placement pass reads every pod and every Layout. A Display change
	// also wakes the Display controller, which writes the panel's status.
	resources := make(chan struct{}, 1)
	resourceWake := func() {
		select {
		case resources <- struct{}{}:
		default:
		}
	}
	pods := openPods(ctx, watcher, nodeName, resourceWake, readings)
	layouts := openLayouts(ctx, watcher, resourceWake, readings)
	displays := openDisplays(ctx, watcher, func() {
		panels.wake()
		resourceWake()
	}, readings)
	// Both passes read pods and Layouts from the watches' stores, and
	// they share one Display store, so each reads the other's writes
	// (objectcache.go).
	stores := clusterStores{layouts: layouts.View(), pods: pods.View()}
	shared := newDisplayStore(client, displays.View())
	shared.recorder = recorder
	notices.displays.Store(shared)
	panels.displays = shared
	places.displays, places.stores = shared, stores
	// The Display controller starts after its store is set, because its
	// goroutine reads the store on its first pass.
	go panels.run(ctx)

	// restarts counts the kubelet's restarts of the compositor's container
	// from this pod's status, and publish records them as a metric. The pod
	// reads its own name from the downward API. An operator run by hand has
	// no pod name, so restarts is nil and counts nothing.
	restarts := newWestonRestarts(client, os.Getenv("POD_NAMESPACE"), os.Getenv("POD_NAME"))
	if restarts != nil {
		restarts.pods = stores
	}

	publish := func() {
		err := readings.reconciled(kindResourceSlice, func() error {
			return reconcile(client, nodeName, owner, card, socketPath, plugin.currentModes, plugin.controls, links,
				sliceHold, readings)
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "publishing the ResourceSlice: %v, trying again in %s\n", err, writeRetryDelay)
			time.AfterFunc(writeRetryDelay, requestPass)
		}
		if growth, err := restarts.growth(); err != nil {
			fmt.Fprintf(os.Stderr, "reading the compositor restart count: %v\n", err)
		} else {
			readings.recordCompositorRestarts(growth)
		}
		// A hardware change reaches the Display controller through this
		// same settled pass, so one burst of uevents causes one pass
		// over the Display resources.
		panels.wake()
	}

	// A prepare republishes through the same function that every event
	// uses, so the modes that it read reach the slice without waiting
	// for a wake. republish is set before the plugin serves, because
	// the kubelet calls the plugin from its own goroutine and a later
	// assignment would race with it.
	plugin.republish = publish
	go func() {
		if err := serveDRAPlugin(ctx, plugin); err != nil {
			fatal("serving the DRA plugin: %v", err)
		}
	}()
	// A placement that fails is logged and left for the next pass. Every
	// wake source reaches the same pass, and the backstop tick
	// guarantees that there is a next one.
	place := func() {
		if err := readings.reconciled(kindLayout, places.pass); err != nil {
			fmt.Fprintf(os.Stderr, "placing the surfaces on each screen: %v\n", err)
		}
	}

	// The card's uevents, retries, the socket watch, and the output watch's
	// connections go through settle, because a flapping monitor produces a
	// burst of uevents. The module's reports and the resource watches wake
	// the placement pass at once, because a new surface can be the first
	// frame of a video that a person is watching. The placement pass writes
	// nothing when nothing changed.
	settled := settle(ctx,
		wakes(ctx, uevents, retries, watchSocket(ctx, socketPath, probeCompositor, plugin.killHungCompositor), connections, nil, nil),
		settleWindow, settleLimit)
	immediate := wakes(ctx, nil, nil, nil, nil, layout.reports, resources)

	// The first pass runs before any event. It replaces the slice that
	// the previous pod left, and records whether a compositor serves
	// now. The slice is tainted if the socket is not up yet.
	publish()
	place()

	for {
		select {
		case <-ctx.Done():
			// The slice is not deleted at shutdown. A consumer's
			// allocation names a device, and a device that leaves the
			// inventory leaves the kubelet's prepare call retrying
			// with no limit. The Node owns the slice, so the slice is
			// deleted when the node leaves the cluster.
			return
		case _, ok := <-settled:
			if !ok {
				if err := eventsEnded(ctx); err != nil {
					fatal("%v", err)
				}
				return
			}
			publish()
			place()
		case _, ok := <-immediate:
			if !ok {
				return
			}
			place()
		}
	}
}

// screens reads the card for the Display controller with the same
// functions as the slice pass, so the Display resources and the slice
// describe the same card. A failed read loses only the field that it
// fills. The card gate logs once when the operator loses its connection
// to the compositor, so screens does not log errCompositorAbsent.
func screens(card string,
	currentModes func() (map[string]string, error),
	connectorModes func() (map[string][]drmMode, error),
) []Output {
	outputs := discoverOutputs(sysRoot, card)
	current, err := currentModes()
	if err != nil && !errors.Is(err, errCompositorAbsent) {
		fmt.Fprintf(os.Stderr, "reading the mode each output runs: %v\n", err)
	}
	offered, err := connectorModes()
	if err != nil && !errors.Is(err, errCompositorAbsent) {
		fmt.Fprintf(os.Stderr, "reading the modes each connector offers: %v\n", err)
	}
	return withOfferedModes(withCurrentModes(outputs, current), offered)
}

// eventsEnded returns nil when the wake channel closed because the
// process is shutting down. Otherwise the kernel's uevent socket
// closed, and the operator would publish only on the backstop tick, up
// to backstopInterval after a monitor changes. The error makes the
// process exit, so the kubelet restarts the pod.
func eventsEnded(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("the kernel event stream ended while the operator was running")
}

// reconcile writes the node's ResourceSlice from the card's connectors
// in sysfs and from whether a compositor serves them. It returns an
// error when the write fails, and the caller schedules another pass.
// reconcile writes links, so the caller must not share links with
// another pass.
//
// When the walk finds no connector, reconcile writes nothing. A card
// registers its connectors when its driver binds and keeps them until
// the card is removed, so an empty walk means that the card is going
// away or that the walk read the wrong path. Publishing it would delete
// every device that a consumer holds.
//
// The currentMode attribute is the mode that each output runs now, read
// from the card with currentModes. The prepare path makes the same
// read, so the slice and a prepared claim agree. When the read fails,
// or while the card gate refuses reads, the slice is published without
// currentMode.
//
// The link history keeps publishing a disconnected connector with the
// monitor it last had, and no pass taints it for staying disconnected.
// A connector that reconnects with a different monitor is tainted for
// disconnectGrace.
func reconcile(client *apiclient.Client, nodeName string, owner OwnerReference, card, socketPath string,
	currentModes func() (map[string]string, error), controls *panelControls, links *linkHistory,
	hold *hotplugView, readings *metrics) error {
	outputs := discoverOutputs(sysRoot, card)
	if len(outputs) == 0 {
		return fmt.Errorf("%s registers no connectors, so the ResourceSlice is not written", card)
	}
	now := time.Now()
	modes, err := currentModes()
	// display_observation_valid{source="card"} records whether this read
	// succeeded. errCompositorAbsent records nothing, because a compositor
	// restart is not a broken card, and the card gate logs it once.
	switch {
	case errors.Is(err, errCompositorAbsent):
	case err != nil:
		readings.recordObservation("card", false, now)
		fmt.Fprintf(os.Stderr, "reading the mode each output runs: %v\n", err)
	default:
		readings.recordObservation("card", true, now)
	}
	// withControls asks each panel which DDC/CI controls it supports. The
	// result is cached by the monitor's EDID, so a pass over unchanged
	// hardware sends nothing on any i2c bus. A panel that refuses DDC/CI
	// gets no control attribute and no control device.
	//
	// hold.apply runs after withLinks, so the link history reads each
	// connector as the kernel reports it, and a monitor that is replaced
	// during a hold still counts as replaced. While a connector is held,
	// the slice publishes what it published while connected.
	observed := hold.apply(withLinks(withControls(withCurrentModes(outputs, modes), controls), links))

	// The claimed gauge comes from the same pass as the connected gauge, so
	// the two describe the same moment.
	claimed, err := preparedOutputs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the claims the kubelet prepared: %v\n", err)
	}
	readings.recordOutputs(observed, claimed)
	devices := sliceDevices(observed)
	// compositorServing is the handshake that also decides the taints
	// below. It returns false for a socket that refuses the connection and
	// for one that accepts and never replies.
	serving := compositorServing(socketPath)
	readings.recordObservation("compositor", serving, now)
	readings.recordCompositorServing(serving)
	if !serving {
		// With no compositor serving, every output and draw device gets a
		// NoExecute taint, which evicts the pods whose connections ended with
		// the socket. A control device keeps its own taints, because its i2c
		// bus needs no compositor. An output whose claim is still preparing
		// gets no taint, because its pod has not started (slices.go). When the
		// ResourceClaims cannot be listed, every output is tainted.
		allocated, err := allocatedOutputs(client, nodeName, clusterStores{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading which claims hold a screen: %v\n", err)
		}
		devices = compositorDown(devices, preparingOutputs(allocated, claimed))
	}
	return EnsureResourceSlice(client, nodeName, owner, devices)
}

// watchSocket probes the compositor's socket every socketWatchInterval
// and sends on the returned channel each time the result changes
// between serving and not serving.
//
// It polls because a compositor that freezes keeps its socket open and
// raises no event, and only an exchange that gets no reply detects it.
// The same probe detects an exit within one interval. The output watch
// and the layout link already receive the socket's arrival as an event
// (arrivals.go).
//
// When the probe has reported the compositor hung for
// compositorHungLimit, watchSocket calls repair once for that outage.
// The operator passes killHungCompositor, which sends SIGKILL. A nil
// repair kills nothing.
//
// probe is a parameter because a probe of a real socket waits on the
// kernel, and a test runs this watch on synctest's fake clock, which
// advances only while nothing waits on the kernel.
func watchSocket(ctx context.Context, socketPath string, probe func(socketPath string) compositorLiveness,
	repair func() error) <-chan struct{} {
	out := make(chan struct{}, 1)
	// The first reading is taken before the ticker starts, so it
	// matches the state that the caller's first pass publishes, and no
	// change falls between the two.
	live := probe(socketPath)
	serving := live.serving
	frozen := &hungCompositor{}
	frozen.due(live, time.Now())
	go func() {
		defer close(out)
		tick := time.NewTicker(socketWatchInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case at := <-tick.C:
				live := probe(socketPath)
				if frozen.due(live, at) && repair != nil {
					endHungCompositor(socketPath, frozen, repair)
				}
				if live.serving == serving {
					continue
				}
				serving = live.serving
				fmt.Printf("the compositor socket %s is serving: %v\n", socketPath, serving)
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out
}

// endHungCompositor calls repair for a compositor that has replied
// nothing for compositorHungLimit. When repair fails, it logs the
// failure, and the next tick calls repair again, because the outage
// continues.
func endHungCompositor(socketPath string, frozen *hungCompositor, repair func() error) {
	if err := repair(); err != nil {
		fmt.Fprintf(os.Stderr, "ending the compositor that does not reply on %s: %v\n", socketPath, err)
		return
	}
	frozen.done()
	fmt.Printf("ended the compositor that did not reply on %s for %s, and its container starts it again\n",
		socketPath, compositorHungLimit)
}

// wakes merges the loop's event sources into one channel and adds a
// tick every backstopInterval. A wake carries no data: each one means
// that the loop reads sysfs, the module's store, and the resources
// again. A nil source never sends.
func wakes(ctx context.Context, uevents <-chan drmEvent,
	retries, sockets, connections, reports, resources <-chan struct{}) <-chan struct{} {
	out := make(chan struct{}, 1)
	wake := func() {
		select {
		case out <- struct{}{}:
		default:
		}
	}
	go func() {
		defer close(out)
		tick := time.NewTicker(backstopInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-uevents:
				if !ok {
					return
				}
				fmt.Printf("drm %s: %s\n", event.Action, event.DevPath)
				wake()
			case _, ok := <-retries:
				if !ok {
					return
				}
				wake()
			case _, ok := <-sockets:
				if !ok {
					return
				}
				wake()
			case _, ok := <-connections:
				if !ok {
					return
				}
				wake()
			case _, ok := <-reports:
				if !ok {
					return
				}
				wake()
			case _, ok := <-resources:
				if !ok {
					return
				}
				wake()
			case <-tick.C:
				wake()
			}
		}
	}()
	return out
}

// settle merges a burst of events into one wake. It sends when the
// input has been quiet for window, or when limit has passed since the
// first event of the burst, whichever comes first. Without the limit, a
// cable that flaps faster than window would restart the wait on every
// event, and the loop would never write.
func settle(ctx context.Context, in <-chan struct{}, window, limit time.Duration) <-chan struct{} {
	out := make(chan struct{}, 1)
	go func() {
		defer close(out)

		var quiet, deadline *time.Timer
		var quietC, deadlineC <-chan time.Time
		emit := func() {
			quiet.Stop()
			deadline.Stop()
			quiet, deadline = nil, nil
			quietC, deadlineC = nil, nil
			select {
			case out <- struct{}{}:
			default:
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-in:
				if !ok {
					return
				}
				if quiet == nil {
					quiet = time.NewTimer(window)
					deadline = time.NewTimer(limit)
					quietC, deadlineC = quiet.C, deadline.C
					continue
				}
				quiet.Stop()
				quiet.Reset(window)
			case <-quietC:
				emit()
			case <-deadlineC:
				emit()
			}
		}
	}()
	return out
}

// envOr returns the environment variable name, or fallback when the
// variable is unset or empty.
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
