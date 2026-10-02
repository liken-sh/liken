package main

// The DRA driver's kubelet half. The kubelet calls a driver's plugin
// before it starts a pod whose claim holds one of that driver's
// devices, and refuses to start the pod while no plugin of the driver
// is registered on the node. So this program registers for
// media.liken.sh although its devices deliver nothing.
//
// The driver runs two gRPC servers, and the kubelet is the only client
// of both. The kubelet finds the registration socket in its registry
// directory and calls GetInfo, which names the socket of the second
// server, the DRA plugin API itself.
//
// A media.liken.sh device is a statement about a GPU. The render node
// that the statement is about comes to the pod from `liken`, through
// the paired request in the same claim. So a prepare answers each
// claim with no device and no CDI edit, and reads nothing from the API
// server, and an unprepare has nothing to give back.

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
)

// The kubelet's plugin directories. These are variables so a test can
// substitute them.
var (
	draRegistryDir = "/var/lib/kubelet/plugins_registry"
	draPluginDir   = "/var/lib/kubelet/plugins/" + DriverName
)

// draPlugin answers the kubelet's prepare and unprepare calls.
type draPlugin struct {
	drav1.UnimplementedDRAPluginServer
}

// NodePrepareResources answers every claim with no device. The kubelet
// treats a claim missing from the response as a failure, so each claim
// gets an entry.
func (draPlugin) NodePrepareResources(ctx context.Context, req *drav1.NodePrepareResourcesRequest) (*drav1.NodePrepareResourcesResponse, error) {
	resp := &drav1.NodePrepareResourcesResponse{Claims: map[string]*drav1.NodePrepareResourceResponse{}}
	for _, claim := range req.Claims {
		resp.Claims[claim.Uid] = &drav1.NodePrepareResourceResponse{}
	}
	return resp, nil
}

// NodeUnprepareResources answers every claim with success, because a
// prepare left nothing on the node.
func (draPlugin) NodeUnprepareResources(ctx context.Context, req *drav1.NodeUnprepareResourcesRequest) (*drav1.NodeUnprepareResourcesResponse, error) {
	resp := &drav1.NodeUnprepareResourcesResponse{Claims: map[string]*drav1.NodeUnprepareResourceResponse{}}
	for _, claim := range req.Claims {
		resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{}
	}
	return resp, nil
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
		// The strings name gRPC services, not versions. This driver
		// serves the v1 API.
		SupportedVersions: []string{drav1.DRAPluginService},
	}, nil
}

func (r *draRegistrar) NotifyRegistrationStatus(ctx context.Context, status *regv1.RegistrationStatus) (*regv1.RegistrationStatusResponse, error) {
	if !status.PluginRegistered {
		fmt.Fprintf(os.Stderr, "dra: the kubelet rejected the plugin registration: %s\n", status.Error)
	}
	return &regv1.RegistrationStatusResponse{}, nil
}

// draHealth is the device-health stream. The service is optional in
// the protocol, but a kubelet that finds it unregistered logs an
// Unimplemented error and retries every few seconds. The driver keeps
// the stream open and sends nothing, because a statement about a GPU
// has no health of its own.
type draHealth struct {
	healthv1alpha1.UnimplementedDRAResourceHealthServer
}

func (draHealth) NodeWatchResources(req *healthv1alpha1.NodeWatchResourcesRequest, stream grpc.ServerStreamingServer[healthv1alpha1.NodeWatchResourcesResponse]) error {
	<-stream.Context().Done()
	return nil
}

// serveDRAPlugin starts both servers and returns when the context ends
// or a server fails. The plugin socket listens before the registration
// socket exists, because the kubelet dials the announced endpoint as
// soon as it reads the registration. A stale socket from an earlier pod
// is removed first, because a bind to a socket file fails even when
// nothing listens on it.
func serveDRAPlugin(ctx context.Context) error {
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
	drav1.RegisterDRAPluginServer(pluginServer, draPlugin{})
	healthv1alpha1.RegisterDRAResourceHealthServer(pluginServer, draHealth{})

	registrationSocket := filepath.Join(draRegistryDir, DriverName+"-reg.sock")
	_ = os.Remove(registrationSocket)
	registrationListener, err := net.Listen("unix", registrationSocket)
	if err != nil {
		pluginListener.Close()
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
		registrationServer.Stop()
		pluginServer.Stop()
		return err
	}
}
