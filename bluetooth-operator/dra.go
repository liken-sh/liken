package main

// The kubelet side of the DRA driver. The kubelet calls it before it
// starts a pod whose claim holds a controller or the media bus.
//
// The driver serves two gRPC services on Unix sockets. The kubelet
// finds the registration socket in its plugins_registry directory and
// calls GetInfo, which returns the path of the socket that serves the
// DRA plugin API.
//
// A prepare call has only the claim's namespace, name, and UID. The
// driver reads the claim's allocation from the API server and writes a
// CDI spec file for the container runtime. For a controller, the spec
// grants the relay's virtual input nodes, which keep their numbers
// while the controller sleeps and reconnects (relay.go), so a pod can
// start while its controller sleeps. For the media bus, the spec adds
// a mount and the bus address variable.
//
// A failure is returned as the claim's error string, not as a gRPC
// error. The kubelet keeps the pod in ContainerCreating, retries, and
// shows the error in the pod's events.

import (
	"context"
	"encoding/json"
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

// The kubelet's directory of registration sockets, and this driver's
// directory for its plugin socket. Tests point them at a temporary
// directory.
var (
	draRegistryDir = "/var/lib/kubelet/plugins_registry"
	draPluginDir   = "/var/lib/kubelet/plugins/" + DriverName
)

// draPlugin serves the kubelet's DRA calls. It reads each claim from
// the API server and reads the virtual input nodes from the relays.
type draPlugin struct {
	drav1.UnimplementedDRAPluginServer
	client *apiclient.Client
	relays *relays
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
		// The kubelet reads these as gRPC service names and uses the
		// newest one it supports. The list has no health service, so the
		// kubelet opens no device health stream. This operator reports
		// device health with device taints.
		SupportedVersions: []string{drav1.DRAPluginService},
	}, nil
}

func (r *draRegistrar) NotifyRegistrationStatus(ctx context.Context, status *regv1.RegistrationStatus) (*regv1.RegistrationStatusResponse, error) {
	if !status.PluginRegistered {
		fmt.Fprintf(os.Stderr, "dra: the kubelet rejected the plugin registration: %s\n", status.Error)
	}
	return &regv1.RegistrationStatusResponse{}, nil
}

// serveDRAPlugin runs both gRPC servers until the context ends or one
// of them fails. The plugin socket listens before the registration
// socket is created, because the kubelet dials the plugin socket as
// soon as it finds the registration socket. A socket file left by a
// previous run is removed first, because listen fails on a path that
// exists even when no process listens on it.
func serveDRAPlugin(ctx context.Context, client *apiclient.Client, inputRelays *relays) error {
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
	drav1.RegisterDRAPluginServer(pluginServer, &draPlugin{client: client, relays: inputRelays})

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

// NodePrepareResources returns an entry for every claim in the
// request, because the kubelet treats a missing entry as a failure and
// retries. A claim that fails does not fail the other claims.
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
		// The claim was deleted and created again under the same name,
		// so this allocation is not the one the pod was scheduled with.
		return fail("the claim's UID changed (%s became %s)", claim.Uid, allocated.Metadata.UID)
	}
	if allocated.Status.Allocation == nil {
		return fail("the claim has no allocation yet")
	}

	var specDevices []cdiDevice
	var devices []*drav1.Device
	// The input classes and axis settings that this claim requests,
	// for each controller by MAC address.
	settings := map[string]inputSettings{}
	for _, result := range allocated.Status.Allocation.Devices.Results {
		if result.Driver != DriverName {
			// The kubelet prepares another driver's devices through
			// that driver's plugin.
			continue
		}
		// The media bus has no input nodes and no relay, and it is ready
		// whenever bluetoothd runs. Every other device is a paired
		// controller.
		var edits cdiEdits
		var inputs []string
		var axes string
		if isMediaBusName(result.Device) {
			edits = busEdits()
		} else {
			mac := macFromDeviceName(result.Device)
			current := p.relays.virtualNodes(mac)
			if len(current) == 0 {
				// The controller has not connected since it was paired, so
				// no capabilities were read or stored, and the relay has no
				// virtual device for it. The device's NoSchedule taint keeps
				// other pods off it until the controller connects once.
				return fail("controller %s has no input relay yet, because it has not connected since it was paired", publishedMAC(mac))
			}
			// A claim that misspells a class fails here, so it does not
			// receive every class without an error.
			want, err := requestInputSettings(allocated.Status.Allocation.Devices.Config, result.Request)
			if err != nil {
				return fail("reading the configuration of device %s: %v", result.Device, err)
			}
			settings[mac] = want
			inputs, axes = recordedInputs(want.classes), want.axes.record()
			edits = cdiEdits{DeviceNodes: deviceNodes(current)}
		}
		name := claim.Uid + "-" + result.Device
		specDevices = append(specDevices, cdiDevice{
			Name:           name,
			ContainerEdits: edits,
			Annotations:    cdiAnnotations(inputs, axes),
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
	// The relays record a claim's settings only after its spec file is
	// written, because they rebuild the settings from the spec files
	// when the operator restarts. A claim recorded without a file would
	// be lost at the next restart.
	for mac, want := range settings {
		p.relays.prepare(mac, claim.Uid, want)
	}
	return &drav1.NodePrepareResourceResponse{Devices: devices}
}

// NodeUnprepareResources deletes each claim's CDI spec and removes the
// claim's settings from the relays, so the relays stop copying input
// that only this claim requested. The virtual nodes stay, because the
// relay keeps them whether or not a claim holds the controller.
func (p *draPlugin) NodeUnprepareResources(ctx context.Context, req *drav1.NodeUnprepareResourcesRequest) (*drav1.NodeUnprepareResourcesResponse, error) {
	resp := &drav1.NodeUnprepareResourcesResponse{Claims: map[string]*drav1.NodeUnprepareResourceResponse{}}
	for _, claim := range req.Claims {
		if err := removeCDISpec(claim.Uid); err != nil {
			resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{Error: err.Error()}
			continue
		}
		p.relays.unprepare(claim.Uid)
		resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{}
	}
	return resp, nil
}

// ResourceClaim is the part of a ResourceClaim that the driver reads.
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
				Config  []AllocatedConfig `json:"config"`
			} `json:"devices"`
		} `json:"allocation"`
	} `json:"status"`
}

// AllocatedConfig is one configuration block of an allocation. Source
// is FromClass for a block from the DeviceClass and FromClaim for a
// block from the claim. An empty Requests means the block applies to
// every request of the claim.
type AllocatedConfig struct {
	Source   string        `json:"source"`
	Requests []string      `json:"requests"`
	Opaque   *OpaqueConfig `json:"opaque"`
}

// OpaqueConfig holds parameters for the driver named in Driver. The
// API server does not read them.
type OpaqueConfig struct {
	Driver     string          `json:"driver"`
	Parameters json.RawMessage `json:"parameters"`
}

// AllocatedDevice is one allocation result: the device that the
// scheduler chose for one of the claim's requests.
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
