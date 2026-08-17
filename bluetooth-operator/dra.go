package main

// The DRA driver's kubelet half: the plugin the kubelet calls before
// it starts a pod that holds a claim on a controller.
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
// namespace, name, and UID. What was allocated lives on the claim's
// status in the API server, so the driver reads that back, walks
// sysfs again, and hands the claim the nodes the named controller
// registers now. A call signals that something happened, and the
// driver acts on the durable record instead of on data carried in the
// call.
//
// Failures are per-claim strings inside the response, not gRPC
// errors. The kubelet holds the affected pod in ContainerCreating and
// retries, which is the correct behavior for a controller that is
// paired and switched off: the pod waits, visibly, and a describe of
// the pod says why.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	healthv1alpha1 "k8s.io/kubelet/pkg/apis/dra-health/v1alpha1"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
)

// The kubelet's plugin directories. The registry is where the kubelet
// discovers plugins, and the plugin's own directory holds the socket
// that does the real work. These are variables so the tests can
// substitute them.
var (
	draRegistryDir = "/var/lib/kubelet/plugins_registry"
	draPluginDir   = "/var/lib/kubelet/plugins/" + DriverName
)

// draSysfsRoot is the sysfs mount this driver reads. The pod runs in
// the host's network namespace and reads the host's own sysfs.
var draSysfsRoot = "/sys"

// draPlugin answers the kubelet's DRA calls. The API client is its
// only state, and it derives everything else again on each call, from
// the claim and from sysfs.
type draPlugin struct {
	drav1.UnimplementedDRAPluginServer
	client *Client
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

// serveDRAPlugin starts both servers and blocks until the context
// ends or a server fails. The order matters: the plugin socket must
// already be listening before the registration socket exists, because
// the kubelet dials the announced endpoint as soon as it sees the
// registration. The function removes stale sockets from a previous
// pod first, because a bind to an orphaned socket file fails even
// when nothing is listening on it.
func serveDRAPlugin(ctx context.Context, client *Client) error {
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
	drav1.RegisterDRAPluginServer(pluginServer, &draPlugin{client: client})
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
// response must carry one entry for each claim, because the kubelet
// treats a missing entry as a failure to retry. Each entry stands on
// its own, so trouble with one claim never blocks another claim's
// pod.
func (p *draPlugin) NodePrepareResources(ctx context.Context, req *drav1.NodePrepareResourcesRequest) (*drav1.NodePrepareResourcesResponse, error) {
	resp := &drav1.NodePrepareResourcesResponse{Claims: map[string]*drav1.NodePrepareResourceResponse{}}
	for _, claim := range req.Claims {
		resp.Claims[claim.Uid] = p.prepareClaim(claim)
	}
	return resp, nil
}

func (p *draPlugin) prepareClaim(claim *drav1.Claim) *drav1.NodePrepareResourceResponse {
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
		// The named claim was deleted and recreated after the kubelet
		// asked. Whatever this new claim holds, it is not the grant
		// this pod was scheduled against.
		return fail("the claim's UID changed (%s became %s)", claim.Uid, allocated.Metadata.UID)
	}
	if allocated.Status.Allocation == nil {
		return fail("the claim has no allocation yet")
	}

	// One walk answers every result in the claim, and it is the same
	// walk that publishes the slice, so the two can never disagree
	// about which nodes a controller registers.
	nodes := nodesByMAC(discoverHIDDevices(draSysfsRoot))

	var specDevices []cdiDevice
	var devices []*drav1.Device
	for _, result := range allocated.Status.Allocation.Devices.Results {
		if result.Driver != DriverName {
			// This is another driver's allocation in the same claim.
			// That driver's own plugin prepares it.
			continue
		}
		mac := macFromDeviceName(result.Device)
		current := nodes[mac]
		if len(current) == 0 {
			// The controller is paired and off the air. The pod waits in
			// ContainerCreating, and the device's NoExecute taint is
			// what the scheduler and the eviction controller act on.
			return fail("controller %s registers no input node right now", publishedMAC(mac))
		}
		name := claim.Uid + "-" + result.Device
		specDevices = append(specDevices, cdiDevice{
			Name:           name,
			ContainerEdits: cdiEdits{DeviceNodes: deviceNodes(current)},
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

// NodeUnprepareResources removes each claim's CDI spec. As with
// prepare, every claim gets an answer and failures stay specific to
// each claim. Nothing else has to be given back: a controller's
// nodes belong to the kernel, and the next claim reads them again.
func (p *draPlugin) NodeUnprepareResources(ctx context.Context, req *drav1.NodeUnprepareResourcesRequest) (*drav1.NodeUnprepareResourcesResponse, error) {
	resp := &drav1.NodeUnprepareResourcesResponse{Claims: map[string]*drav1.NodeUnprepareResourceResponse{}}
	for _, claim := range req.Claims {
		if err := removeCDISpec(claim.Uid); err != nil {
			resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{Error: err.Error()}
			continue
		}
		resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{}
	}
	return resp, nil
}

// draHealth is the device-health stream. The driver keeps it open and
// sends nothing on it. The service is optional in the DRA protocol,
// and the kubelet does not treat it that way in practice: an
// unregistered service produces an Unimplemented error and a retry
// every few seconds, forever, in the kubelet's log. This operator
// reports health through the device taint instead, which is the
// mechanism that evicts a pod when a controller goes quiet.
type draHealth struct {
	healthv1alpha1.UnimplementedDRAResourceHealthServer
}

func (h *draHealth) NodeWatchResources(req *healthv1alpha1.NodeWatchResourcesRequest, stream grpc.ServerStreamingServer[healthv1alpha1.NodeWatchResourcesResponse]) error {
	<-stream.Context().Done()
	return nil
}

// ResourceClaim holds the part of a claim that the driver reads:
// which devices were allocated, from which driver's pools. This
// operator never writes a claim. Workloads create them and the
// scheduler allocates them.
type ResourceClaim struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		UID       string `json:"uid"`
	} `json:"metadata"`
	Status struct {
		Allocation *struct {
			Devices struct {
				Results []AllocatedDevice `json:"results"`
			} `json:"devices"`
		} `json:"allocation"`
	} `json:"status"`
}

// AllocatedDevice is one allocation result. The scheduler chose
// Device from Pool, published by Driver, to satisfy the claim's named
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
