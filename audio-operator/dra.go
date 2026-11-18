package main

// The kubelet calls NodePrepareResources before it starts a pod that
// has a claim on an audio device, and NodeUnprepareResources when the
// claim is released.
//
// The kubelet finds the driver through a registration socket in its
// plugin registry directory. It calls GetInfo there, which returns the
// path of the plugin socket that serves the DRA calls. Both are gRPC
// servers on unix sockets, and the permissions on the kubelet's
// directories are their only authentication.
//
// A prepare request contains only the claim's namespace, name, and UID,
// so the driver reads the allocation from the API server and the node
// name from PipeWire's graph at prepare time.
//
// A failed claim gets an error string on its entry in the response, not
// a gRPC error. The kubelet keeps the pod in ContainerCreating and
// retries, and kubectl describe pod shows the error.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// These are variables so that tests can point them at a temporary
// directory.
var (
	draRegistryDir = "/var/lib/kubelet/plugins_registry"
	draPluginDir   = "/var/lib/kubelet/plugins/" + DriverName
)

// draPlugin serves the kubelet's DRA calls. It shares endpoints and
// claims with the reconcile pass, and reads the claim and the graph
// again on each call.
type draPlugin struct {
	drav1.UnimplementedDRAPluginServer
	client *apiclient.Client
	// endpoints is the inventory that the last reconcile pass read
	// from the hardware. A device name contains no ALSA card or PCM
	// number, so a prepare looks them up here.
	endpoints *endpointInventory
	// claims records which claims hold each device. The plugin writes
	// it, and the reconcile pass reads it to write status.claims.
	claims *preparedClaims
	// graph and changes read the graph feed that the reconcile pass
	// also reads, so a prepare starts no pw-dump process. A codec
	// switch waits on changes for the rebuilt sink. These and the
	// PipeWire writes below are fields so that a test can run a prepare
	// without PipeWire.
	graph      func(context.Context) (pwGraph, error)
	changes    func() <-chan struct{}
	setCodec   func(ctx context.Context, device, codec int) error
	setVolumes func(ctx context.Context, node int, volumes []float64) error
}

func newDRAPlugin(client *apiclient.Client, endpoints *endpointInventory, claims *preparedClaims, feed *graphFeed) *draPlugin {
	return &draPlugin{
		client:     client,
		endpoints:  endpoints,
		claims:     claims,
		graph:      feed.read,
		changes:    feed.changes,
		setCodec:   setDeviceCodec,
		setVolumes: setNodeVolumes,
	}
}

// draRegistrar serves the kubelet's plugin registration handshake.
type draRegistrar struct {
	regv1.UnimplementedRegistrationServer
	endpoint string
}

func (r *draRegistrar) GetInfo(ctx context.Context, req *regv1.InfoRequest) (*regv1.PluginInfo, error) {
	return &regv1.PluginInfo{
		Type:     regv1.DRAPlugin,
		Name:     DriverName,
		Endpoint: r.endpoint,
		// These are gRPC service names, not semantic versions, and
		// the driver serves only the v1 API. The list has no health
		// service, so the kubelet opens no device health stream. The
		// operator reports health with device taints instead.
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
// registration socket exists, because the kubelet dials the plugin
// socket as soon as it finds the registration socket. A socket file
// that a previous pod left is removed first, because binding a unix
// socket fails when the file exists, even when nothing listens on it.
func serveDRAPlugin(ctx context.Context, client *apiclient.Client, endpoints *endpointInventory, claims *preparedClaims, feed *graphFeed) error {
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
	drav1.RegisterDRAPluginServer(pluginServer, newDRAPlugin(client, endpoints, claims, feed))

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
// kubelet treats a missing entry as a failure and retries. A failure of
// one claim does not affect the others.
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
		// The claim was deleted and created again after the kubelet
		// sent the call, so this allocation is not the one that the
		// pod was scheduled against.
		return fail("the claim's UID changed (%s became %s)", claim.Uid, allocated.Metadata.UID)
	}
	if allocated.Status.Allocation == nil {
		return fail("the claim has no allocation yet")
	}

	// The API server stores this driver's opaque config without parsing
	// it, and the scheduler copies it into the allocation, so a bad
	// parameter fails here first.
	selection, err := claimCodecs(allocated.Status.Allocation.Devices.Config)
	if err != nil {
		return fail("%v", err)
	}

	// The graph is read once, and every result in the claim uses that
	// read. It comes from the feed that the ResourceSlice is published
	// from, so the slice and the prepare agree on which node an output
	// has.
	graph, err := p.graph(ctx)
	if err != nil {
		return fail("reading PipeWire's graph: %v", err)
	}

	var specDevices []cdiDevice
	var devices []*drav1.Device
	var held []string
	for _, result := range allocated.Status.Allocation.Devices.Results {
		if result.Driver != DriverName {
			// A claim can pair this driver's speakers with another
			// driver's screen, and that driver's plugin prepares it.
			continue
		}
		// Only a Bluetooth speaker has a codec to select. This check
		// runs before the node lookup, so that the error names the
		// mistake in the claim and not a missing node.
		codec := selection.forRequest(result.Request)
		address, isSpeaker := speakerFromDeviceName(result.Device)
		if codec != "" && !isSpeaker {
			return fail("the claim selects the codec %s for %s, which is not a Bluetooth speaker",
				codec, result.Device)
		}
		// A codec switch rebuilds the sink under every claim that
		// shares the speaker, so a shared claim cannot select a codec.
		// The Sink's spec.codec sets the codec of a shared speaker.
		shared := claimShares(allocated, result.Request)
		if codec != "" && shared {
			return fail("the claim shares %s and selects the codec %s, but a codec switch rebuilds the sink "+
				"under every claim that shares the speaker, so a shared claim cannot select one",
				result.Device, codec)
		}
		node, err := endpointNode(p.endpoints, result.Device, graph)
		if err != nil {
			return fail("%v", err)
		}
		if isSpeaker {
			speakerSink := graph.Speakers[address]
			if codec != "" {
				// Wait until the sink reports the new codec, because a
				// pod that connects before the rebuild plays through the
				// old codec.
				switched, err := p.selectCodec(ctx, address, codec, speakerSink)
				if err != nil {
					return fail("%v", err)
				}
				speakerSink = switched
				node = switched.Node
			}
			// Each prepare of a speaker sets its sink to unity volume,
			// with or without a codec, so every claim starts from the
			// same level. A claim that shares a speaker that another
			// claim holds skips the write, because it would change the
			// volume of the stream that already plays.
			if !p.claims.heldByAnother(result.Device, claim.Uid) {
				if err := p.setSinkToUnity(ctx, address, speakerSink); err != nil {
					return fail("%v", err)
				}
			}
		}
		held = append(held, result.Device)
		name := claim.Uid + "-" + result.Device
		specDevices = append(specDevices, cdiDevice{
			Name:           name,
			ContainerEdits: endpointEdits(node),
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
	// Record the holders after writing the CDI spec, so status.claims
	// never lists a device whose pod cannot start yet.
	p.claims.record(claim.Uid, EndpointClaim{Namespace: claim.Namespace, Name: claim.Name}, held)
	return &drav1.NodePrepareResourceResponse{Devices: devices}
}

// setSinkToUnity sets every channel of a speaker's sink to 1.0. A
// failed write fails the prepare, because the kubelet retries, and a
// pod that started anyway would play at an unknown volume.
//
// Object 0 in PipeWire is the core, so a NodeID of 0 means that the
// graph had no id for this sink, and the prepare continues without the
// write. A sound card's output gets no write at prepare. The reconcile
// pass sets a sink node that PipeWire has just built to unity when its
// spec declares no level (resting.go).
func (p *draPlugin) setSinkToUnity(ctx context.Context, address string, sink bluezSink) error {
	if sink.NodeID == 0 {
		return nil
	}
	if err := p.setVolumes(ctx, sink.NodeID, unityLevels(sink)); err != nil {
		return fmt.Errorf("setting speaker %s's sink to unity: %w", speakerName(address), err)
	}
	return nil
}

// endpointNode returns the name of the PipeWire node that the pod's
// streams must target. A sound card device's name has no card or PCM
// number, so it is looked up in the inventory. A Bluetooth speaker's
// device name is its MAC address with dashes, so its sink is found from
// the name alone.
func endpointNode(endpoints *endpointInventory, device string, graph pwGraph) (string, error) {
	if output, published := endpoints.lookup(device); published {
		node, running := graph.Nodes[output.graphAddress()]
		if !running {
			return "", fmt.Errorf("%s %s has no PipeWire node right now", output.direction(), device)
		}
		return node.Name, nil
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

// NodeUnprepareResources removes each claim's CDI spec and its record
// in claims. Nothing else needs releasing, because the PipeWire socket
// belongs to PipeWire and the next prepare looks up the node again.
func (p *draPlugin) NodeUnprepareResources(ctx context.Context, req *drav1.NodeUnprepareResourcesRequest) (*drav1.NodeUnprepareResourcesResponse, error) {
	resp := &drav1.NodeUnprepareResourcesResponse{Claims: map[string]*drav1.NodeUnprepareResourceResponse{}}
	for _, claim := range req.Claims {
		if err := removeCDISpec(claim.Uid); err != nil {
			resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{Error: err.Error()}
			continue
		}
		p.claims.release(claim.Uid)
		resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{}
	}
	return resp, nil
}

// ResourceClaim is the part of a claim that the driver reads. The
// operator never writes a claim.
type ResourceClaim struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		UID       string `json:"uid"`
	} `json:"metadata"`
	Spec struct {
		Devices claimRequests `json:"devices"`
	} `json:"spec"`
	Status struct {
		Allocation *struct {
			// The driver reads config from the allocation and not from
			// the claim's spec, because the scheduler merges the
			// DeviceClass's config into this list with the claim's and
			// marks the source of each entry.
			Devices struct {
				Results []AllocatedDevice `json:"results"`
				Config  []AllocatedConfig `json:"config"`
			} `json:"devices"`
		} `json:"allocation"`
	} `json:"status"`
}

// AllocatedDevice is one allocation result. A claim can mix devices of
// several drivers, such as a screen and speakers, so the driver checks
// Driver.
type AllocatedDevice struct {
	Request string `json:"request"`
	Driver  string `json:"driver"`
	Pool    string `json:"pool"`
	Device  string `json:"device"`
}

func GetResourceClaim(c *apiclient.Client, namespace, name string) (*ResourceClaim, error) {
	path := "/apis/resource.k8s.io/v1/namespaces/" + namespace + "/resourceclaims/" + name
	claim := &ResourceClaim{}
	if err := c.RequestJSON(http.MethodGet, path, nil, claim); err != nil {
		return nil, err
	}
	return claim, nil
}
