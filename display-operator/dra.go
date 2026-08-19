package main

// The DRA driver's kubelet half: the plugin the kubelet calls before
// it starts a pod that holds a claim on an output.
//
// The wire arrangement is the opposite of what the word "plugin"
// suggests. The driver runs two gRPC servers and the kubelet is the
// only client of both. The first is registration: the kubelet watches
// a well-known directory for sockets, dials each one, and calls
// GetInfo to read what is there. The second is the DRA plugin API
// itself, on a socket of the driver's own, whose path GetInfo
// announces. Unix sockets are the whole transport, and file
// permissions on the kubelet's directories are the authentication.
//
// The prepare protocol tells the driver almost nothing: a claim's
// namespace, name, and UID. What was allocated is on the claim's
// status in the API server, so the driver reads that back, walks sysfs
// again, and answers for the output the claim holds now.
//
// Failures are per-claim strings inside the response, not gRPC errors.
// The kubelet holds the affected pod in ContainerCreating and retries,
// which is what should happen for an output whose monitor is dark:
// the pod waits, visibly, and a describe of the pod says why.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	healthv1alpha1 "k8s.io/kubelet/pkg/apis/dra-health/v1alpha1"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
)

// The kubelet's plugin directories. The registry is where the kubelet
// discovers plugins, and the plugin's own directory holds the socket
// that answers the prepare calls. These are variables so the tests can
// substitute them.
var (
	draRegistryDir = "/var/lib/kubelet/plugins_registry"
	draPluginDir   = "/var/lib/kubelet/plugins/" + DriverName
)

// draPlugin answers the kubelet's DRA calls. It holds the API client,
// the card whose connectors it publishes, and the directory of the
// socket a consumer receives. Everything else it derives again on each
// call, from the claim and from sysfs.
//
// The mode fields are what a claim that states a resolution
// touches: the compositor's config and the record beside it, the two
// seams a switch acts through, and the bounds of the wait for the
// screen to come back.
type draPlugin struct {
	drav1.UnimplementedDRAPluginServer
	client    *Client
	sysRoot   string
	card      string
	socketDir string
	// The two files in the pod's config volume. The operator
	// mounts the same volume the declare container writes and the
	// compositor reads.
	configPath string
	recordPath string
	// CurrentModes reads what each output runs, and
	// endCompositor is the restart that makes a new mode take. Both are
	// fields rather than calls to the functions themselves, so a test
	// drives a prepare with no card node and no compositor behind it.
	currentModes  func() (map[string]string, error)
	endCompositor func() error
	// The bounds of the wait for the socket and the mode to come
	// back.
	switchTimeout  time.Duration
	switchInterval time.Duration
	// ModeSwitches serializes the whole switch. The record is
	// one file for every connector, and two prepares that rewrote it at
	// once would restart the compositor twice for one config.
	modeSwitches sync.Mutex
	// Restarted remembers the restart this process already
	// ordered for a connector, which is the restart budget: a readback
	// that still disagrees after one restart means weston declined the
	// mode, and a second restart would blank every screen for the same
	// wrong answer.
	restarted map[string]string
}

// NewDRAPlugin builds the plugin the kubelet talks to.
//
// Every seam takes its real implementation here and a stand-in
// only in a test, so this is the one place the card readback and the
// compositor's restart are named together.
func newDRAPlugin(client *Client, card, socketDir string) *draPlugin {
	return &draPlugin{
		client:     client,
		sysRoot:    sysRoot,
		card:       card,
		socketDir:  socketDir,
		configPath: westonConfigPath,
		recordPath: modeRecordPath,
		currentModes: func() (map[string]string, error) {
			return readCurrentModes(filepath.Join(driRoot, card))
		},
		endCompositor:  func() error { return endCompositor(procRoot) },
		switchTimeout:  modeSwitchTimeout,
		switchInterval: modeSwitchInterval,
	}
}

// draRegistrar answers the kubelet's plugin-watcher handshake.
type draRegistrar struct {
	regv1.UnimplementedRegistrationServer
	endpoint string
}

func (r *draRegistrar) GetInfo(ctx context.Context, req *regv1.InfoRequest) (*regv1.PluginInfo, error) {
	return &regv1.PluginInfo{
		Type:     regv1.DRAPlugin,
		Name:     DriverName,
		Endpoint: r.endpoint,
		// These strings name gRPC services, not semantic versions. The
		// kubelet picks the newest version it also supports, and this
		// driver serves exactly the v1 API.
		SupportedVersions: []string{drav1.DRAPluginService},
	}, nil
}

func (r *draRegistrar) NotifyRegistrationStatus(ctx context.Context, status *regv1.RegistrationStatus) (*regv1.RegistrationStatusResponse, error) {
	if !status.PluginRegistered {
		fmt.Fprintf(os.Stderr, "dra: the kubelet rejected the plugin registration: %s\n", status.Error)
	}
	return &regv1.RegistrationStatusResponse{}, nil
}

// serveDRAPlugin starts both servers and blocks until the context ends
// or a server fails. The order matters: the plugin socket must already
// be listening before the registration socket exists, because the
// kubelet dials the announced endpoint as soon as it sees the
// registration. The function removes stale sockets from a previous pod
// first, because a bind to an orphaned socket file fails even when
// nothing is listening on it.
func serveDRAPlugin(ctx context.Context, plugin *draPlugin) error {
	if err := os.MkdirAll(draPluginDir, 0o755); err != nil {
		return err
	}
	pluginSocket := filepath.Join(draPluginDir, "dra.sock")
	_ = os.Remove(pluginSocket)
	pluginListener, err := net.Listen("unix", pluginSocket)
	if err != nil {
		return fmt.Errorf("the plugin socket: %w", err)
	}
	pluginServer := grpc.NewServer()
	drav1.RegisterDRAPluginServer(pluginServer, plugin)
	healthv1alpha1.RegisterDRAResourceHealthServer(pluginServer, &draHealth{})

	registrationSocket := filepath.Join(draRegistryDir, DriverName+"-reg.sock")
	_ = os.Remove(registrationSocket)
	registrationListener, err := net.Listen("unix", registrationSocket)
	if err != nil {
		return fmt.Errorf("the registration socket: %w", err)
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

// NodePrepareResources prepares every claim in the request. The
// response must include one entry for each claim, because the kubelet
// treats a missing entry as a failure to retry. Each entry is
// independent, so trouble with one claim never blocks another claim's
// pod.
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

	// The delivery is the compositor's socket, so a claim prepared
	// while the compositor is restarting would hand a client a path
	// with nothing behind it. The kubelet holds the pod in
	// ContainerCreating and asks again, and the retry is the wait.
	socketPath := filepath.Join(p.socketDir, socketName)
	if !compositorServing(socketPath) {
		return fail("no compositor is serving %s right now", socketPath)
	}

	allocated, err := GetResourceClaim(p.client, claim.Namespace, claim.Name)
	if err != nil {
		return fail("reading the claim: %v", err)
	}
	if allocated.Metadata.UID != claim.Uid {
		// The named claim was deleted and recreated after the kubelet
		// asked. Whatever this new claim holds, it is not the grant
		// this pod was scheduled against.
		return fail("the claim's UID changed (%s became %s)", claim.Uid, allocated.Metadata.UID)
	}
	if allocated.Status.Allocation == nil {
		return fail("the claim has no allocation yet")
	}

	// The allocation's config is the resolved list: the claim's
	// own blocks and the DeviceClass's, each marked with its source.
	// The scheduler passed every opaque block through unread, so this
	// is the first code anywhere that reads this driver's parameters.
	selection, err := claimModes(allocated.Status.Allocation.Devices.Config)
	if err != nil {
		return fail("%v", err)
	}

	// One walk answers every result in the claim, and it is the same
	// walk that publishes the slice, so the two always report the same
	// set of outputs with a monitor on them.
	live := map[string]Output{}
	for _, output := range connected(discoverOutputs(p.sysRoot, p.card)) {
		live[deviceName(output.Connector)] = output
	}

	var specDevices []cdiDevice
	var devices []*drav1.Device
	for _, result := range allocated.Status.Allocation.Devices.Results {
		if result.Driver != DriverName {
			// This is another driver's allocation in the same claim.
			// That driver's own plugin prepares it. A claim that asks
			// for a screen and that screen's speakers holds two
			// results, and each driver answers for its own.
			continue
		}
		output, lit := live[result.Device]
		if !lit {
			// The monitor left between the allocation and this call.
			// The pod waits in ContainerCreating, and the output's
			// NoExecute taint is what the eviction controller acts on.
			//
			// A mode on a connector with nothing on it fails
			// here too. There is no mode list to validate against and
			// no screen to light.
			return fail("output %s has no monitor on it right now", result.Device)
		}
		// The delivery waits for the switch. Between the restart
		// and the readback the screen runs the mode the claim replaced,
		// and a delivery that raced it would start the consumer against
		// a screen that is about to go dark.
		if mode := selection.forRequest(result.Request); mode != "" {
			if err := p.applyMode(ctx, output, mode, socketPath); err != nil {
				return fail("%v", err)
			}
		}
		name := claim.Uid + "-" + result.Device
		specDevices = append(specDevices, cdiDevice{
			Name:           name,
			ContainerEdits: outputEdits(p.socketDir, socketName, appID(result.Device)),
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

// NodeUnprepareResources removes each claim's CDI spec and takes the
// modes it stated out of the record. As with prepare, every claim gets
// an answer and failures stay specific to each claim. The socket needs
// nothing given back: the compositor keeps the screen, and the next
// claim receives the same socket.
//
// Nothing restarts here, so the screen keeps the mode until the
// next compositor start. The record is what the next start reads, so
// dropping the entry is what returns the screen to the mode its
// monitor prefers.
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

// UnprepareClaim gives back what one claim held.
//
// The record is released before the spec is removed, because
// the spec is what names the claim's outputs. A failure between the
// two leaves the spec in place, and the kubelet's next unprepare
// reads it again and releases what is left.
func (p *draPlugin) unprepareClaim(claimUID string) error {
	devices, err := preparedDevices(claimUID)
	if err != nil {
		return err
	}
	if err := p.releaseModes(devices); err != nil {
		return err
	}
	return removeCDISpec(claimUID)
}

// draHealth is the device-health stream. The driver keeps it open and
// sends nothing on it. The service is optional in the DRA protocol,
// and the kubelet does not treat it that way in practice. An
// unregistered service produces an Unimplemented error and a retry
// in the kubelet's log every few seconds, with no end. This operator
// reports health through the device taints instead, which is the
// mechanism that evicts a pod when a monitor goes dark.
type draHealth struct {
	healthv1alpha1.UnimplementedDRAResourceHealthServer
}

func (h *draHealth) NodeWatchResources(req *healthv1alpha1.NodeWatchResourcesRequest, stream grpc.ServerStreamingServer[healthv1alpha1.NodeWatchResourcesResponse]) error {
	<-stream.Context().Done()
	return nil
}

// ResourceClaim holds the part of a claim that the driver reads: which
// devices were allocated, from which driver's pools. This operator
// never writes a claim. Workloads create them and the scheduler
// allocates them.
type ResourceClaim struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		UID       string `json:"uid"`
	} `json:"metadata"`
	Status struct {
		Allocation *struct {
			// The driver reads the allocation's config and never
			// the claim's own spec. The scheduler resolves the
			// DeviceClass's blocks and the claim's into this one list
			// and marks each entry's source, so cluster policy is
			// visible here and nowhere else.
			Devices struct {
				Results []AllocatedDevice `json:"results"`
				Config  []AllocatedConfig `json:"config"`
			} `json:"devices"`
		} `json:"allocation"`
	} `json:"status"`
}

// AllocatedDevice is one allocation result. The scheduler chose Device
// from Pool, published by Driver, to satisfy the claim's named
// Request. Driver matters because one claim can mix devices from
// several drivers.
type AllocatedDevice struct {
	Request string `json:"request"`
	Driver  string `json:"driver"`
	Pool    string `json:"pool"`
	Device  string `json:"device"`
}

// GetResourceClaim reads one claim. Claims are namespaced, because a
// claim belongs to the workload that created it.
func GetResourceClaim(c *Client, namespace, name string) (*ResourceClaim, error) {
	path := "/apis/resource.k8s.io/v1/namespaces/" + namespace + "/resourceclaims/" + name
	claim := &ResourceClaim{}
	if err := c.RequestJSON(http.MethodGet, path, nil, claim); err != nil {
		return nil, err
	}
	return claim, nil
}
