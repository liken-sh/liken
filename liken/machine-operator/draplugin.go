package main

// The DRA kubelet plugin: the kubelet calls it to prepare a claim's
// devices before it starts a pod that uses the claim.
//
// The plugin runs two gRPC servers on unix sockets. The kubelet watches
// its plugin registry directory, dials the registration socket there,
// and calls GetInfo, which returns the path of the DRA socket in the
// driver's own directory. Both directories are under the kubelet's
// state directory, so their file permissions are the only
// authentication.
//
// A prepare request has only the claim's namespace, name, and UID. So
// the plugin reads the claim's allocation from the API server, walks
// sysfs, and returns the device nodes of each allocated device.
//
// A failure is an error string in that claim's response, not a gRPC
// error. The kubelet keeps the pod in ContainerCreating and retries,
// which suits a device that is still enumerating or an API server
// error. For hardware that is gone, the retries never succeed, but
// `kubectl describe pod` shows the error.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/grpc"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// Variables so that tests can use a temporary directory.
var (
	draRegistryDir = "/var/lib/kubelet/plugins_registry"
	draPluginDir   = "/var/lib/kubelet/plugins/liken.sh"
)

// draPlugin serves the kubelet's DRA calls. It reads the claim and
// sysfs on each call and caches nothing.
//
// Each unprepare sends on released, if it is not nil, to wake the
// reconcile loop. The ResourceSlice keeps the taint on an unplugged
// device until its claim is unprepared, so the loop must then publish
// the slice again without it (unplugged.go).
type draPlugin struct {
	drav1.UnimplementedDRAPluginServer
	client   *apiclient.Client
	released chan<- struct{}
}

// draRegistrar serves the kubelet's plugin registration handshake.
type draRegistrar struct {
	regv1.UnimplementedRegistrationServer
	endpoint string
}

func (r *draRegistrar) GetInfo(ctx context.Context, req *regv1.InfoRequest) (*regv1.PluginInfo, error) {
	return &regv1.PluginInfo{
		Type:     regv1.DRAPlugin,
		Name:     kubernetes.DriverName,
		Endpoint: r.endpoint,
		// gRPC service names, of which the kubelet uses the newest that
		// it supports. The Kubernetes that liken ships has the v1 API,
		// so the plugin serves only that. The kubelet opens a
		// device-health stream only when this list has dra-health/v1,
		// and the plugin does not report device health.
		SupportedVersions: []string{drav1.DRAPluginService},
	}, nil
}

func (r *draRegistrar) NotifyRegistrationStatus(ctx context.Context, status *regv1.RegistrationStatus) (*regv1.RegistrationStatusResponse, error) {
	if !status.PluginRegistered {
		fmt.Fprintf(os.Stderr, "dra: the kubelet rejected the plugin registration: %s\n", status.Error)
	}
	return &regv1.RegistrationStatusResponse{}, nil
}

// serveDRAPlugin serves both sockets until ctx ends or a server
// fails. It listens on the plugin socket before it creates the
// registration socket, because the kubelet dials the plugin as soon as
// the registration socket appears. It removes each socket file first,
// because bind fails on a file left by a previous process.
func serveDRAPlugin(ctx context.Context, client *apiclient.Client, released chan<- struct{}) error {
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
	drav1.RegisterDRAPluginServer(pluginServer, &draPlugin{client: client, released: released})

	registrationSocket := filepath.Join(draRegistryDir, kubernetes.DriverName+"-reg.sock")
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
// kubelet treats a missing entry as a failure. A failure of one claim
// does not fail the others.
func (p *draPlugin) NodePrepareResources(ctx context.Context, req *drav1.NodePrepareResourcesRequest) (*drav1.NodePrepareResourcesResponse, error) {
	resp := &drav1.NodePrepareResourcesResponse{Claims: map[string]*drav1.NodePrepareResourceResponse{}}
	for _, claim := range req.Claims {
		resp.Claims[claim.Uid] = p.prepareClaim(claim)
	}
	return resp, nil
}

// resolveAllocated returns the entry that publishFor gives now for an
// allocated device name, so prepare delivers what the ResourceSlice
// would publish at this moment. A device's own name is its primary
// entry, and any other name is a device's name plus a companion's
// suffix. An exact name match comes first, because one device's name
// can begin with another's. A virtualization device has no hardware,
// so its name resolves through publishingvirtualization.go.
//
// It returns errWithheld for a device that delivers a protected disk,
// and errNotPublished for a name that no present device publishes.
func resolveAllocated(name string, sysRoot string, byName map[string]hardware.Device, serio []machine.SerioAttachment, platform protection) (publishedDevice, error) {
	if published, ok, err := resolveVirtualization(name, sysRoot, sysctlRoot); ok {
		return published, err
	}
	if device, ok := byName[name]; ok {
		delivery := claimDelivery(sysRoot, device)
		if platform.withholds(delivery) {
			return publishedDevice{}, errWithheld
		}
		for _, p := range publishFor(device, delivery, serio) {
			if p.Suffix == "" {
				return p, nil
			}
		}
		return publishedDevice{}, errNotPublished
	}
	for bare, device := range byName {
		if !strings.HasPrefix(name, bare+"-") {
			continue
		}
		delivery := claimDelivery(sysRoot, device)
		for _, p := range publishFor(device, delivery, serio) {
			if bare+p.Suffix != name {
				continue
			}
			if platform.withholds(delivery) {
				return publishedDevice{}, errWithheld
			}
			return p, nil
		}
	}
	return publishedDevice{}, errNotPublished
}

var errNotPublished = errors.New("no device in this machine's inventory publishes that name")

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
		// The claim was deleted and created again with the same name,
		// so its allocation is not the one this pod was scheduled with.
		return fail("the claim's UID changed (%s became %s)", claim.Uid, allocated.Metadata.UID)
	}
	if allocated.Status.Allocation == nil {
		return fail("the claim has no allocation yet")
	}

	// The same walk and naming that publish the ResourceSlice, so an
	// allocated name refers to the device that the slice published.
	byName := map[string]hardware.Device{}
	for _, d := range hardware.DiscoverInventory(draSysfsRoot, draNaming()) {
		byName[deviceName(d)] = d
	}

	var specDevices []cdiDevice
	var devices []*drav1.Device
	for _, result := range allocated.Status.Allocation.Devices.Results {
		if result.Driver != kubernetes.DriverName {
			continue
		}
		published, err := resolveAllocated(result.Device, draSysfsRoot, byName, declaredSerio(), currentProtection())
		if err != nil {
			return fail("resolving the allocated device %s: %v", result.Device, err)
		}
		nodes := deviceNodes(published.Nodes)
		name := claim.Uid + "-" + result.Device
		unit, _ := resolveHardware(result.Device, byName)
		specDevices = append(specDevices, cdiDevice{
			Name:           name,
			Annotations:    deviceAnnotations(claim.Namespace, claim.Name, allocated.PodOwned(), unit),
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

// NodeUnprepareResources rebinds the kernel drivers of each claim's
// devices and removes the claim's CDI spec. As in prepare, each claim
// gets its own entry. The rebind runs first, because the spec file is
// the only record of which devices the claim used (rebinding.go). The
// sysfs walk runs at most once for the request, and only when a claim
// still has a spec file, so a repeated unprepare does not walk sysfs.
func (p *draPlugin) NodeUnprepareResources(ctx context.Context, req *drav1.NodeUnprepareResourcesRequest) (*drav1.NodeUnprepareResourcesResponse, error) {
	devices := sync.OnceValue(func() map[string]hardware.Device {
		byName := map[string]hardware.Device{}
		for _, d := range hardware.DiscoverInventory(draSysfsRoot, draNaming()) {
			byName[deviceName(d)] = d
		}
		return byName
	})
	resp := &drav1.NodeUnprepareResourcesResponse{Claims: map[string]*drav1.NodeUnprepareResourceResponse{}}
	for _, claim := range req.Claims {
		rebindClaimDevices(claim.Uid, devices)
		if err := removeCDISpec(claim.Uid); err != nil {
			resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{Error: err.Error()}
			continue
		}
		resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{}
	}
	if p.released != nil {
		select {
		case p.released <- struct{}{}:
		default:
		}
	}
	return resp, nil
}
