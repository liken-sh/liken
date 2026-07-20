package main

// The DRA driver's kubelet half: the plugin the kubelet calls before
// it starts a pod that holds a claim.
//
// The wire arrangement is the opposite of what the word "plugin"
// suggests. The driver runs two gRPC servers, and the kubelet is
// the only client of both. The first service is registration: the
// kubelet watches a well-known directory for sockets, dials each
// one, and calls GetInfo to learn what is there. The second service
// is the DRA plugin API itself, on a socket of the driver's own,
// whose path GetInfo announces. Both sockets live under the
// kubelet's own state directory. Unix sockets are the entire
// transport: nothing here touches the network, and file permissions
// on the kubelet's directories provide the authentication.
//
// The prepare protocol deliberately tells the driver almost
// nothing: a claim's namespace, name, and UID. What was allocated
// lives on the claim's status in the API server, so the driver
// reads that back (kubernetes/resourceclaims.go) and works out the
// delivery again from its own inventory walk. This follows the same
// rule liken applies everywhere: a call only signals that something
// happened, and the driver acts on the shared, durable record, not
// on data carried in the call itself.
//
// Failures are per-claim strings inside the response, not gRPC
// errors. The kubelet holds the affected pod in ContainerCreating
// and retries. This is the right behavior for every temporary
// cause, such as a device still being enumerated or an API hiccup,
// and it is also the honest behavior for a permanent one, such as
// hardware that has been removed. The pod waits, visibly, for
// hardware the cluster said it could have, and a describe of the
// pod shows why.

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	healthv1alpha1 "k8s.io/kubelet/pkg/apis/dra-health/v1alpha1"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"

	"github.com/liken-sh/liken/hardware"
	"github.com/liken-sh/liken/kubernetes"
)

// The kubelet's plugin directories. The registry is where the
// kubelet discovers plugins. The plugin's own directory holds the
// socket that does the real work. These are variables so the tests
// can substitute them.
var (
	draRegistryDir = "/var/lib/kubelet/plugins_registry"
	draPluginDir   = "/var/lib/kubelet/plugins/liken.sh"
)

// draPlugin answers the kubelet's DRA calls. The API client is its
// only state. It derives everything else again on each call, from
// the claim and from sysfs.
type draPlugin struct {
	drav1.UnimplementedDRAPluginServer
	client *kubernetes.Client
}

// draRegistrar answers the kubelet's plugin-watcher handshake.
type draRegistrar struct {
	regv1.UnimplementedRegistrationServer
	endpoint string
}

func (r *draRegistrar) GetInfo(ctx context.Context, req *regv1.InfoRequest) (*regv1.PluginInfo, error) {
	return &regv1.PluginInfo{
		Type:     regv1.DRAPlugin,
		Name:     kubernetes.DriverName,
		Endpoint: r.endpoint,
		// These strings name gRPC services, not semantic versions. The
		// kubelet picks the newest version that it also supports.
		// liken serves exactly the v1 API. The Kubernetes version
		// liken ships is never older than its own OS components, so
		// there is no version gap to bridge with a beta shim.
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
// already be listening before the registration socket exists,
// because the kubelet dials the announced endpoint as soon as it
// sees the registration. The function removes stale sockets from a
// previous operator first. A bind to an orphaned socket file fails
// even when nothing is listening on it.
func serveDRAPlugin(ctx context.Context, client *kubernetes.Client) error {
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

	registrationSocket := filepath.Join(draRegistryDir, kubernetes.DriverName+"-reg.sock")
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
// treats a missing entry as a failure to retry. Each entry stands
// on its own, so trouble with one claim never blocks another
// claim's pod.
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

	allocated, err := kubernetes.GetResourceClaim(p.client, claim.Namespace, claim.Name)
	if err != nil {
		return fail("reading the claim: %v", err)
	}
	if allocated.Metadata.UID != claim.Uid {
		// The named claim was deleted and recreated after the
		// kubelet asked. Whatever this new claim holds, it is not
		// the grant this pod was scheduled against.
		return fail("the claim's UID changed (%s became %s)", claim.Uid, allocated.Metadata.UID)
	}
	if allocated.Status.Allocation == nil {
		return fail("the claim has no allocation yet")
	}

	// One walk maps allocated device names back to hardware. It uses
	// the same walk and the same naming that published the
	// inventory, so the two can never disagree about which device a
	// name identifies.
	byName := map[string]hardware.Device{}
	for _, d := range hardware.DiscoverDevices(draSysfsRoot, draNaming()) {
		byName[deviceName(d)] = d
	}

	var specDevices []cdiDevice
	var devices []*drav1.Device
	for _, result := range allocated.Status.Allocation.Devices.Results {
		if result.Driver != kubernetes.DriverName {
			// This is another driver's allocation in the same claim.
			// That driver's own plugin prepares it.
			continue
		}
		device, ok := byName[result.Device]
		if !ok {
			return fail("allocated device %s is not present", result.Device)
		}
		delivery := hardware.InspectDelivery(draSysfsRoot, device)
		if len(delivery.DevNodes) == 0 {
			return fail("allocated device %s has no device nodes to deliver", result.Device)
		}
		nodes := make([]cdiDeviceNode, 0, len(delivery.DevNodes))
		for _, path := range delivery.DevNodes {
			nodes = append(nodes, cdiDeviceNode{Path: path})
		}
		name := claim.Uid + "-" + result.Device
		specDevices = append(specDevices, cdiDevice{
			Name:           name,
			ContainerEdits: cdiEdits{DeviceNodes: nodes},
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

// draHealth is the device-health stream. The driver keeps it open
// and sends nothing on it. The service is optional in the DRA
// protocol, but the kubelet does not treat it that way in practice:
// an unregistered service produces an Unimplemented error and a
// retry every few seconds, forever, in the k3s log. Accepting the
// stream and reporting nothing states the truth, because liken
// makes no health claims about devices yet. This is the same stream
// that real health reports will use once the uevent watcher starts
// feeding it (see the plan doc's device-health note).
type draHealth struct {
	healthv1alpha1.UnimplementedDRAResourceHealthServer
}

func (h *draHealth) NodeWatchResources(req *healthv1alpha1.NodeWatchResourcesRequest, stream grpc.ServerStreamingServer[healthv1alpha1.NodeWatchResourcesResponse]) error {
	<-stream.Context().Done()
	return nil
}

// NodeUnprepareResources removes each claim's CDI spec. As with
// prepare, every claim gets an answer, and failures stay specific to
// each claim.
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
