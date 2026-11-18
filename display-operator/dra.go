package main

// This file is the kubelet side of the DRA driver: the plugin that the
// kubelet calls before it starts a pod whose claim holds an output.
//
// The kubelet is the client of two gRPC servers on Unix sockets. It
// watches the registry directory for sockets, dials each one, and calls
// GetInfo, which returns the path of the second socket, where the DRA
// plugin API runs. File permissions on the kubelet's directories are
// the only authentication.
//
// A prepare request has only a claim's namespace, name, and UID, so the
// driver reads the claim's allocation from the API server and reads the
// card's connectors from sysfs on each call.
//
// A failure is an error string in the claim's entry of the response,
// not a gRPC error. The kubelet keeps the pod in ContainerCreating and
// retries, so a pod whose monitor is disconnected waits, and
// `kubectl describe pod` shows the reason.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The kubelet finds plugins by their sockets in draRegistryDir, and
// draPluginDir holds the socket of the DRA plugin API. They are
// variables so that tests can point them at a temporary directory.
var (
	draRegistryDir = "/var/lib/kubelet/plugins_registry"
	draPluginDir   = "/var/lib/kubelet/plugins/" + DriverName
)

// draPlugin serves the kubelet's DRA calls. The function fields let a
// test replace the card and the compositor. main sets notices, served,
// republish, links, and metrics after newDRAPlugin returns, and each
// field's comment says what a nil value does.
type draPlugin struct {
	drav1.UnimplementedDRAPluginServer
	client *apiclient.Client
	// notices posts an Event for each mode change, each compositor
	// that the plugin ends, and each standby that fails. Nil posts
	// nothing.
	notices    *screenNotices
	sysRoot    string
	card       string
	socketDir  string
	configPath string
	recordPath string
	powerPath  string
	// controls sends DDC/CI commands to the panels. The same instance
	// publishes the control attributes of the slice, so a prepare right
	// after a publish reads the probe from its cache and sends nothing
	// to the panel.
	controls *panelControls
	// claims maps a claim's UID to its namespace and name. Prepare
	// records each claim, because the placement pass starts from a
	// socket name, which has only the UID.
	claims *claimIndex
	// layout is the link to the compositor's layout module, which opens
	// the Wayland socket that each claim receives.
	layout *layoutLink
	// gate opens the card for currentModes and connectorModes. It
	// returns errCompositorAbsent while the operator has no connection
	// to a compositor, and it reports a compositor that serves without
	// DRM master.
	gate *cardGate
	// currentModes reads the mode each output runs, and connectorModes
	// reads the modes each connector offers.
	currentModes   func() (map[string]string, error)
	connectorModes func() (map[string][]drmMode, error)
	// compositors lists the pids of the running compositors, and signal
	// sends one of them a signal. Every restart goes through
	// endCompositor, which writes a restart request file before it sends the
	// signal, so the main process of the compositor container starts
	// weston again instead of exiting.
	compositors     func() []int
	signal          func(pid int, signal syscall.Signal) error
	restartRequests restartRequests
	// served returns the outputs and modes that the compositor reports,
	// which a mode switch reads to confirm the new mode. While it is
	// nil, every mode switch waits for its time limit and fails.
	served func() servedOutputs
	// republish runs the operator's reconcile pass, so that the slice
	// shows the mode list that a prepare read from the kernel. Nil does
	// nothing.
	republish func()
	// links is the history of each connector's monitor, which supplies
	// the monitor of a disconnected connector. While it is nil, a
	// disconnected connector has no monitor.
	links *linkHistory
	// switchTimeout and switchFallback are modeSwitchTimeout and
	// modeSwitchFallback, in fields so that a test can shorten them.
	switchTimeout  time.Duration
	switchFallback time.Duration
	// modeSwitches serializes mode switches. The mode record is one
	// file for every connector, and two prepares that rewrote it at the
	// same time would restart the compositor twice for one config.
	modeSwitches sync.Mutex
	// powerRecords serializes writes to the power record. It is a
	// separate lock so that a power-down does not wait for a compositor
	// restart.
	powerRecords sync.Mutex
	// releaseGrace is how long a panel stays on after its
	// onWhileClaimed claim ends, before it goes to standby. afterGrace
	// schedules the standby, and nil uses time.AfterFunc. A test
	// replaces it to run the standby when it chooses.
	releaseGrace time.Duration
	afterGrace   func(delay time.Duration, run func())
	// restarted maps a connector to the mode that this process last
	// restarted the compositor for. applyMode reads it to restart at
	// most once for each mode, and releaseModes deletes the entry.
	restarted map[string]string
	// metrics counts the compositor restarts that the plugin requests, by
	// reason. Nil counts nothing.
	metrics *metrics
}

func newDRAPlugin(client *apiclient.Client, card, socketDir string, layout *layoutLink) *draPlugin {
	gate := newCardGate(filepath.Join(driRoot, card), procRoot)
	return &draPlugin{
		client:          client,
		sysRoot:         sysRoot,
		card:            card,
		socketDir:       socketDir,
		configPath:      westonConfigPath,
		recordPath:      modeRecordPath,
		powerPath:       powerRecordPath,
		controls:        newPanelControls(sysRoot, card),
		claims:          newClaimIndex(client),
		layout:          layout,
		gate:            gate,
		currentModes:    gate.currentModes,
		connectorModes:  gate.connectorModes,
		compositors:     func() []int { return compositorProcesses(procRoot) },
		signal:          signalProcess,
		restartRequests: restartRequests(restartRequestsPath),
		switchTimeout:   modeSwitchTimeout,
		switchFallback:  modeSwitchFallback,
		releaseGrace:    powerReleaseGrace,
	}
}

// draRegistrar serves the kubelet's plugin registration calls.
type draRegistrar struct {
	regv1.UnimplementedRegistrationServer
	endpoint string
}

func (r *draRegistrar) GetInfo(ctx context.Context, req *regv1.InfoRequest) (*regv1.PluginInfo, error) {
	return &regv1.PluginInfo{
		Type:     regv1.DRAPlugin,
		Name:     DriverName,
		Endpoint: r.endpoint,
		// The versions are gRPC service names. The list leaves out the
		// DRAResourceHealth service, so the kubelet opens no device
		// health stream. The operator reports health with device
		// taints instead, which evict a pod when its monitor
		// disconnects.
		SupportedVersions: []string{drav1.DRAPluginService},
	}, nil
}

func (r *draRegistrar) NotifyRegistrationStatus(ctx context.Context, status *regv1.RegistrationStatus) (*regv1.RegistrationStatusResponse, error) {
	if !status.PluginRegistered {
		fmt.Fprintf(os.Stderr, "dra: the kubelet rejected the plugin registration: %s\n", status.Error)
	}
	return &regv1.RegistrationStatusResponse{}, nil
}

// serveDRAPlugin runs the plugin and registration servers until the
// context ends or a server fails. The plugin socket listens before the
// registration socket exists, because the kubelet dials the plugin as
// soon as it sees the registration. A socket file left by a previous
// pod is removed first, because bind fails on an existing file even
// when nothing listens on it.
func serveDRAPlugin(ctx context.Context, plugin *draPlugin) error {
	if err := os.MkdirAll(draPluginDir, 0o755); err != nil {
		return err
	}
	pluginSocket := filepath.Join(draPluginDir, "dra.sock")
	_ = os.Remove(pluginSocket)
	pluginListener, err := net.Listen("unix", pluginSocket)
	if err != nil {
		return fmt.Errorf("listening on the plugin socket: %w", err)
	}
	pluginServer := grpc.NewServer()
	drav1.RegisterDRAPluginServer(pluginServer, plugin)

	registrationSocket := filepath.Join(draRegistryDir, DriverName+"-reg.sock")
	_ = os.Remove(registrationSocket)
	registrationListener, err := net.Listen("unix", registrationSocket)
	if err != nil {
		return fmt.Errorf("listening on the registration socket: %w", err)
	}
	registrationServer := grpc.NewServer()
	regv1.RegisterRegistrationServer(registrationServer, &draRegistrar{endpoint: pluginSocket})

	errs := make(chan error, 2)
	go func() { errs <- pluginServer.Serve(pluginListener) }()
	go func() { errs <- registrationServer.Serve(registrationListener) }()
	select {
	case <-ctx.Done():
		registrationServer.Stop()
		pluginServer.Stop()
		return nil
	case err := <-errs:
		return err
	}
}

// NodePrepareResources returns an entry for each claim, because the
// kubelet treats a missing entry as a failure and retries. A claim that
// fails does not block the pods of the other claims.
func (p *draPlugin) NodePrepareResources(ctx context.Context, req *drav1.NodePrepareResourcesRequest) (*drav1.NodePrepareResourcesResponse, error) {
	resp := &drav1.NodePrepareResourcesResponse{Claims: map[string]*drav1.NodePrepareResourceResponse{}}
	for _, claim := range req.Claims {
		resp.Claims[claim.Uid] = p.prepareClaim(ctx, claim)
	}
	return resp, nil
}

func (p *draPlugin) prepareClaim(ctx context.Context, claim *drav1.Claim) *drav1.NodePrepareResourceResponse {
	fail := func(format string, args ...any) *drav1.NodePrepareResourceResponse {
		message := fmt.Sprintf(format, args...)
		fmt.Fprintf(os.Stderr, "dra: preparing claim %s/%s: %s\n", claim.Namespace, claim.Name, message)
		return &drav1.NodePrepareResourceResponse{Error: message}
	}

	allocated, err := GetResourceClaim(p.client, claim.Namespace, claim.Name)
	if err != nil {
		return fail("reading the claim: %v", err)
	}
	if allocated.Metadata.UID != claim.Uid {
		// The claim was deleted and created again under the same name
		// after the kubelet sent the request, so the pod was scheduled
		// against the old allocation.
		return fail("the claim's UID changed (%s became %s)", claim.Uid, allocated.Metadata.UID)
	}
	if allocated.Status.Allocation == nil {
		return fail("the claim has no allocation yet")
	}
	p.claims.remember(claim.Uid, claim.Namespace, claim.Name)

	// The scheduler does not read opaque parameters, so prepare is the
	// first place that validates them.
	selection, err := claimModes(allocated.Status.Allocation.Devices.Config)
	if err != nil {
		return fail("%v", err)
	}
	controls, err := claimControls(allocated.Status.Allocation.Devices.Config)
	if err != nil {
		return fail("%v", err)
	}

	// discoverOutputs is the walk that also publishes the slice, so
	// prepare finds every connector that the slice lists. It includes
	// disconnected connectors, because a monitor that shows another
	// input reads as disconnected and is still the screen that the
	// claim was allocated.
	onCard := map[string]Output{}
	for _, output := range discoverOutputs(p.sysRoot, p.card) {
		onCard[deviceName(output.Connector)] = output
	}

	// The socket names are chosen before the loop, so that the output
	// and draw results of one connector get the same socket.
	sockets := claimSocketNames(claim.Uid, allocated.Status.Allocation.Devices.Results)

	var specDevices []cdiDevice
	var devices []*drav1.Device
	waylandChecked := false
	for _, result := range allocated.Status.Allocation.Devices.Results {
		if result.Driver != DriverName {
			// A claim for a screen and its speakers also has a result
			// from the audio driver, whose plugin prepares it.
			continue
		}
		// A connector publishes an output device, a control device for
		// its DDC/CI channel, and a draw device that many claims share.
		// outputOfControl and outputOfDraw return the output's name for
		// the other devices.
		device, control := outputOfControl(result.Device)
		draw := false
		if base, isDraw := outputOfDraw(result.Device); isDraw {
			device, draw = base, true
		}
		// Output and draw results deliver a Wayland socket, so they need
		// a serving compositor. A control result delivers an i2c node,
		// which works without one. The check runs before any result
		// writes to a panel or opens a socket, so a claim that fails it
		// changes nothing.
		if !control && !waylandChecked {
			if err := p.waylandServing(); err != nil {
				return fail("%v", err)
			}
			waylandChecked = true
		}
		output, onThisCard := onCard[device]
		if !onThisCard {
			// The operator publishes devices only for its card's
			// connectors, so the claim was allocated from another
			// machine's pool.
			return fail("this card has no connector named %s", device)
		}
		if !output.Connected {
			// A monitor that shows another input and an unplugged
			// monitor read the same in sysfs. Prepare continues when the
			// link history has a monitor for the connector.
			output.Remembered = p.links.remembered(output.Connector)
			if monitorID(output.Remembered) == "" {
				return fail("output %s has no monitor on it right now", device)
			}
		}
		var edits cdiEdits
		switch {
		case draw:
			// A draw result sets no mode and no panel power, because
			// many claims share the draw device and a write from one of
			// them would change the screen for the others.
			if err := p.layout.Listen(sockets[device], output.Connector); err != nil {
				return fail("%v", err)
			}
			edits = outputEdits(p.socketDir, sockets[device], appID(output.Connector))
		case control:
			// The consumer of a control device opens the i2c node and
			// sends its own commands, so prepare writes nothing to the
			// panel.
			if err := controlTakesNoParameters(result.Device,
				selection.forRequest(result.Request), controls.forRequest(result.Request)); err != nil {
				return fail("%v", err)
			}
			// The node number is read here and not published as an
			// attribute, because the kernel numbers i2c adapters in the
			// order it registers them, and the number can change at the
			// next boot.
			node := connectorBus(p.sysRoot, p.card, output.Connector)
			if node == "" {
				return fail("%s has no DDC/CI channel that this operator can pass to a pod", output.Connector)
			}
			edits = controlEdits(node)
		default:
			// The controls go before the mode, because a panel in
			// standby reports no mode, and the mode switch would wait
			// for one until the panel woke.
			if err := p.applyControls(output, controls.forRequest(result.Request)); err != nil {
				return fail("%v", err)
			}
			// applyMode returns after the compositor reports the new
			// mode, because a consumer started before that would lose
			// its screen when the compositor restarts.
			if mode := selection.forRequest(result.Request); mode != "" {
				// A disconnected connector has no mode list to validate
				// the mode against, so prepare fails until the monitor
				// is connected again.
				if !output.Connected {
					return fail("%s has no monitor connected right now, so it cannot take the mode %s",
						output.Connector, mode)
				}
				if err := p.applyMode(ctx, output, mode); err != nil {
					return fail("%v", err)
				}
			}
			// The socket opens after the mode switch, because a
			// compositor restart closes every socket that the layout
			// module opened. When the module is not serving again yet,
			// Listen fails, and the kubelet's retry finds the mode
			// already on screen and opens the socket.
			if err := p.layout.Listen(sockets[device], output.Connector); err != nil {
				return fail("%v", err)
			}
			edits = outputEdits(p.socketDir, sockets[device], appID(device))
		}
		name := claim.Uid + "-" + result.Device
		specDevices = append(specDevices, cdiDevice{
			Name:           name,
			ContainerEdits: edits,
		})
		devices = append(devices, &drav1.Device{
			PoolName:     result.Pool,
			DeviceName:   result.Device,
			RequestNames: []string{result.Request},
			CdiDeviceIds: []string{cdiKind + "=" + name},
		})
	}
	if len(specDevices) > 0 {
		if err := writeCDISpec(claim.Uid, specDevices); err != nil {
			return fail("writing the CDI spec: %v", err)
		}
	}
	return &drav1.NodePrepareResourceResponse{Devices: devices}
}

// waylandServing returns an error unless the compositor and its layout
// module both serve. While the compositor restarts, its socket file can
// exist with nothing listening on it. The layout module opens each
// claim's socket, and without it the CDI spec would name a socket that
// nothing listens on, and the client would fail to connect with no
// message that says why.
func (p *draPlugin) waylandServing() error {
	socketPath := filepath.Join(p.socketDir, socketName)
	if live := probeCompositor(socketPath); !live.serving {
		return fmt.Errorf("no compositor is serving %s (%s: %s)", socketPath, live.reason, live.detail)
	}
	if !p.layout.moduleServing() {
		return fmt.Errorf("the layout module is not serving %s", layoutSocketPath)
	}
	return nil
}

// controlTakesNoParameters fails a claim that sets a mode, a
// brightness, or a power on a control request. The consumer of a
// control device sends its own commands to the panel, so the operator
// would ignore the parameter, and the person would get no error.
//
// The usual cause is a parameter block with no request name, which
// applies to every request in the claim. A claim for a screen and its
// control channel must name the screen's request in the block.
func controlTakesNoParameters(device, mode string, want requestedControls) error {
	var parameters []string
	if mode != "" {
		parameters = append(parameters, modeParameter)
	}
	if want.Brightness.Set {
		parameters = append(parameters, brightnessParameter)
	}
	if want.Power != "" {
		parameters = append(parameters, powerParameter)
	}
	if len(parameters) == 0 {
		return nil
	}
	return fmt.Errorf("the claim sets %s for %s, and a control device takes no parameters:"+
		" set the request name of the screen on the parameter block that sets them",
		strings.Join(parameters, " and "), device)
}

// NodeUnprepareResources removes each claim's modes from the mode
// record, closes the claim's sockets, and removes its CDI spec. Like
// prepare, it returns an entry for every claim, and a failure applies
// to its claim only.
//
// Unprepare does not restart the compositor, because a restart blanks
// every screen on the card. The screen keeps the claim's mode until the
// next compositor start, which reads the record and starts the screen
// at its declared mode or at the mode the monitor prefers.
//
// A panel whose claim set onWhileClaimed goes to standby after
// releaseGrace, unless another claim prepares on the connector first.
// A panel whose claim set on stays as it is.
func (p *draPlugin) NodeUnprepareResources(ctx context.Context, req *drav1.NodeUnprepareResourcesRequest) (*drav1.NodeUnprepareResourcesResponse, error) {
	resp := &drav1.NodeUnprepareResourcesResponse{Claims: map[string]*drav1.NodeUnprepareResourceResponse{}}
	for _, claim := range req.Claims {
		if err := p.unprepareClaim(claim.Uid); err != nil {
			fmt.Fprintf(os.Stderr, "dra: unpreparing claim %s/%s: %v\n", claim.Namespace, claim.Name, err)
			resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{Error: err.Error()}
			continue
		}
		resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{}
	}
	return resp, nil
}

// unprepareClaim reads the claim's devices from its CDI spec, so it
// removes the spec last. When releasing the modes or removing the spec
// fails, the spec stays, and the kubelet's next unprepare reads it
// again and finishes the release.
func (p *draPlugin) unprepareClaim(claimUID string) error {
	devices, err := preparedDevices(claimUID)
	if err != nil {
		return err
	}
	if err := p.releaseModes(devices); err != nil {
		return err
	}
	p.releasePower(devices)
	p.releaseSockets(claimUID)
	if err := removeCDISpec(claimUID); err != nil {
		return err
	}
	// The republish also wakes the Display controller, which does not
	// restore a declared mode or restart the compositor while a CDI spec
	// lists the screen. So it runs after the spec is removed.
	p.republishSlice()
	return nil
}

// ResourceClaim holds the fields of a ResourceClaim that the driver
// reads. The operator never writes a claim.
type ResourceClaim struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		UID       string `json:"uid"`
	} `json:"metadata"`
	Status struct {
		Allocation *struct {
			// The driver reads the config here and not in the claim's
			// spec, because the scheduler merges the DeviceClass's
			// parameter blocks and the claim's blocks into this list.
			Devices struct {
				Results []AllocatedDevice `json:"results"`
				Config  []AllocatedConfig `json:"config"`
			} `json:"devices"`
		} `json:"allocation"`
		// A Layout region's selector matches the labels of the pods
		// listed here, so the placement pass finds a socket's region
		// from the pods that its claim is reserved for.
		ReservedFor []ClaimConsumer `json:"reservedFor,omitempty"`
	} `json:"status"`
}

type ResourceClaimList struct {
	Items []ResourceClaim `json:"items"`
}

// ClaimConsumer is one entry of a claim's reservedFor list. Resource is
// the plural name of its kind, and the operator uses only pods.
type ClaimConsumer struct {
	Resource string `json:"resource"`
	Name     string `json:"name"`
	UID      string `json:"uid"`
}

// AllocatedDevice is one allocation result: the scheduler allocated
// Device from Pool, which Driver publishes, for the request named
// Request.
type AllocatedDevice struct {
	Request string `json:"request"`
	Driver  string `json:"driver"`
	Pool    string `json:"pool"`
	Device  string `json:"device"`
}

// GetResourceClaim reads one claim from the API server.
func GetResourceClaim(c *apiclient.Client, namespace, name string) (*ResourceClaim, error) {
	path := "/apis/resource.k8s.io/v1/namespaces/" + namespace + "/resourceclaims/" + name
	claim := &ResourceClaim{}
	if err := c.RequestJSON(http.MethodGet, path, nil, claim); err != nil {
		return nil, err
	}
	return claim, nil
}
