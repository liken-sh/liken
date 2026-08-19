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
// status in the API server, so the driver reads that back, reads
// PipeWire's graph again, and hands the claim the name the allocated
// output's sink has now. A call signals that something happened,
// and the driver acts on the durable record instead of on data
// carried in the call.
//
// Failures are per-claim strings inside the response, not gRPC
// errors. The kubelet holds the affected pod in ContainerCreating and
// retries, which is the right behavior for an output whose monitor
// is unplugged: the pod waits, visibly, and a describe of the pod
// says why.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	healthv1alpha1 "k8s.io/kubelet/pkg/apis/dra-health/v1alpha1"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
)

// The kubelet's plugin directories. The registry is where the kubelet
// discovers plugins, and the plugin's own directory holds the socket
// that answers the prepare calls. These are variables so the tests
// can substitute them.
var (
	draRegistryDir = "/var/lib/kubelet/plugins_registry"
	draPluginDir   = "/var/lib/kubelet/plugins/" + DriverName
)

// draPlugin answers the kubelet's DRA calls. The API client is its
// only state, and it derives everything else again on each call, from
// the claim and from PipeWire.
type draPlugin struct {
	drav1.UnimplementedDRAPluginServer
	client *Client
	// graph reads PipeWire's graph. It is a field rather than a call
	// to readGraph, so a test drives a prepare without a PipeWire
	// behind it.
	graph func(context.Context) (pwGraph, error)
	// setCodec and setVolumes are the two writes a codec switch makes,
	// fields for the same reason.
	setCodec   func(ctx context.Context, device, codec int) error
	setVolumes func(ctx context.Context, node int, volumes []float64) error
	// codecTimeout and codecInterval bound the wait for the rebuilt
	// node.
	codecTimeout  time.Duration
	codecInterval time.Duration
}

// newDRAPlugin builds the plugin the kubelet talks to.
//
// Every seam takes its real implementation here and a stand-in
// only in a test, so this is the one place the production graph
// read and the two writes are named together.
func newDRAPlugin(client *Client) *draPlugin {
	return &draPlugin{
		client:        client,
		graph:         readGraph,
		setCodec:      setDeviceCodec,
		setVolumes:    setNodeVolumes,
		codecTimeout:  codecSwitchTimeout,
		codecInterval: codecSwitchInterval,
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
	drav1.RegisterDRAPluginServer(pluginServer, newDRAPlugin(client))
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
// independent, so trouble with one claim never blocks another
// claim's pod.
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
		// The named claim was deleted and recreated after the kubelet
		// asked. Whatever this new claim holds, it is not the grant
		// this pod was scheduled against.
		return fail("the claim's UID changed (%s became %s)", claim.Uid, allocated.Metadata.UID)
	}
	if allocated.Status.Allocation == nil {
		return fail("the claim has no allocation yet")
	}

	// The allocation's config is the resolved list: the claim's own
	// blocks and the DeviceClass's, each marked with its source. The
	// scheduler passed every opaque block through unread, so this is
	// the first code anywhere that reads this driver's parameters.
	selection, err := claimCodecs(allocated.Status.Allocation.Devices.Config)
	if err != nil {
		return fail("%v", err)
	}

	// One graph read answers every result in the claim, and it is the
	// same read that publishes the slice, so the two always report the
	// same sink for an output.
	graph, err := p.graph(ctx)
	if err != nil {
		return fail("reading PipeWire's graph: %v", err)
	}

	var specDevices []cdiDevice
	var devices []*drav1.Device
	for _, result := range allocated.Status.Allocation.Devices.Results {
		if result.Driver != DriverName {
			// This is another driver's allocation in the same claim.
			// That driver's own plugin prepares it. A claim that pairs a
			// screen with its speakers holds one of each.
			continue
		}
		// A codec on anything but a Bluetooth speaker fails before
		// the graph is asked for a sink: a sound card has no air
		// codec, and failing here names the real mistake instead of
		// whatever the graph happens to lack.
		codec := selection.forRequest(result.Request)
		address, isSpeaker := speakerFromDeviceName(result.Device)
		if codec != "" && !isSpeaker {
			return fail("the claim states the codec %s for %s, which is not a Bluetooth speaker",
				codec, result.Device)
		}
		// A device with no sink right now leaves no name to give the
		// consumer. The pod waits in ContainerCreating, and the device's
		// taints are what the scheduler and the eviction controller act
		// on.
		sink, err := deliveredSink(result.Device, graph)
		if err != nil {
			return fail("%v", err)
		}
		if isSpeaker {
			speakerSink := graph.Speakers[address]
			if codec != "" {
				// The delivery waits for the switch. The rebuilt node
				// keeps its name, but between the write and the
				// rebuild the name names nothing, and a delivery that
				// raced it could start the consumer against a sink
				// still playing the old codec.
				switched, err := p.selectCodec(ctx, address, codec, speakerSink)
				if err != nil {
					return fail("%v", err)
				}
				speakerSink = switched
				sink = switched.Node
			}
			// The unity write sits on the delivery path, not inside
			// the switch, so a claim that states no codec and a claim
			// that states the codec already playing deliver the same
			// sink in the same state.
			if err := p.deliverAtUnity(ctx, address, speakerSink); err != nil {
				return fail("%v", err)
			}
		}
		name := claim.Uid + "-" + result.Device
		specDevices = append(specDevices, cdiDevice{
			Name:           name,
			ContainerEdits: sinkEdits(sink),
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

// deliverAtUnity sets every channel of one speaker's sink to 1.0.
//
// A failed write fails the whole prepare: the kubelet retries and
// a retry converges, where a delivery that went ahead would play
// at a level nobody chose.
//
// Object 0 is the PipeWire core, never a node, so a graph read
// that reported no id for this sink reported nothing to write to,
// and the prepare proceeds without the write.
//
// The card's own outputs take no such write: their level lives on
// a device route, which is a different write with different
// semantics.
func (p *draPlugin) deliverAtUnity(ctx context.Context, address string, sink bluezSink) error {
	if sink.NodeID == 0 {
		return nil
	}
	if err := p.setVolumes(ctx, sink.NodeID, unityLevels(sink)); err != nil {
		return fmt.Errorf("setting speaker %s's sink to unity: %w", speakerName(address), err)
	}
	return nil
}

// deliveredSink resolves one allocated device name to the PipeWire
// node a consumer's streams must target. The name is the whole of
// what a prepare call carries, so its shape decides which half of
// the graph answers: card<n>-pcm<n> is an ALSA output, and six
// dashed hexadecimal octets are a Bluetooth speaker. The two shapes
// cannot collide, because an output's name always holds the word
// card and a MAC never does.
func deliveredSink(device string, graph pwGraph) (string, error) {
	if card, pcm, ok := outputFromDeviceName(device); ok {
		sink, ok := graph.Outputs[pcmAddress{Card: card, PCM: pcm}]
		if !ok {
			return "", fmt.Errorf("output %s has no PipeWire sink right now", device)
		}
		return sink, nil
	}
	if address, ok := speakerFromDeviceName(device); ok {
		sink, ok := graph.Speakers[address]
		if !ok {
			return "", fmt.Errorf("speaker %s has no PipeWire sink right now", device)
		}
		return sink.Node, nil
	}
	return "", fmt.Errorf("%q does not name a device of this driver", device)
}

// NodeUnprepareResources removes each claim's CDI spec. As with
// prepare, every claim gets an answer and failures stay specific to
// each claim. Nothing else has to be given back: the socket belongs
// to PipeWire, and the next claim reads its name again.
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
// and the kubelet does not treat it that way in practice. An
// unregistered service produces an Unimplemented error and a retry
// in the kubelet's log every few seconds, with no end. This operator
// reports health through the device taints instead, which is the
// mechanism that evicts a pod when an output goes silent.
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
			// The driver reads the allocation's config and never the
			// claim's own spec. The scheduler resolves the
			// DeviceClass's blocks and the claim's into this one
			// list and marks each entry's source, so cluster policy
			// is visible here and nowhere else.
			Devices struct {
				Results []AllocatedDevice `json:"results"`
				Config  []AllocatedConfig `json:"config"`
			} `json:"devices"`
		} `json:"allocation"`
	} `json:"status"`
}

// AllocatedDevice is one allocation result. The scheduler chose
// Device from Pool, published by Driver, to satisfy the claim's named
// Request. Driver matters because one claim can mix devices from
// several drivers, which is what pairs a screen with its speakers.
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
