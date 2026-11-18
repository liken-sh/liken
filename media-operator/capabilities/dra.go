package main

// The kubelet-facing side of the DRA driver. Before the kubelet starts
// a pod whose claim holds a device of this driver, it calls the
// driver's plugin, and it does not start the pod while no plugin of the
// driver is registered on the node. So this program registers a plugin
// for `media.liken.sh` even though the plugin has nothing to prepare.
//
// The driver runs two gRPC servers, and only the kubelet calls them.
// The kubelet finds the registration socket in its registry directory
// and calls GetInfo, which returns the socket of the DRA plugin server.
//
// A `media.liken.sh` device describes a GPU and gives the pod nothing
// to use. The pod gets the render node from `liken`, through the paired
// request in the same claim. So a prepare returns an entry with no
// device and no CDI edit for each claim, and does not read the API
// server. An unprepare has nothing to undo.

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
)

// The kubelet's plugin directories. They are variables so a test can
// point them at a temporary directory.
var (
	draRegistryDir = "/var/lib/kubelet/plugins_registry"
	draPluginDir   = "/var/lib/kubelet/plugins/" + DriverName
)

// draPlugin handles the kubelet's prepare and unprepare calls.
type draPlugin struct {
	drav1.UnimplementedDRAPluginServer
}

// NodePrepareResources returns an entry with no device for each claim.
// The kubelet treats a claim missing from the response as a failure.
func (draPlugin) NodePrepareResources(ctx context.Context, req *drav1.NodePrepareResourcesRequest) (*drav1.NodePrepareResourcesResponse, error) {
	resp := &drav1.NodePrepareResourcesResponse{Claims: map[string]*drav1.NodePrepareResourceResponse{}}
	for _, claim := range req.Claims {
		resp.Claims[claim.Uid] = &drav1.NodePrepareResourceResponse{}
	}
	return resp, nil
}

// NodeUnprepareResources reports success for each claim, because a
// prepare leaves nothing on the node to undo.
func (draPlugin) NodeUnprepareResources(ctx context.Context, req *drav1.NodeUnprepareResourcesRequest) (*drav1.NodeUnprepareResourcesResponse, error) {
	resp := &drav1.NodeUnprepareResourcesResponse{Claims: map[string]*drav1.NodeUnprepareResourceResponse{}}
	for _, claim := range req.Claims {
		resp.Claims[claim.Uid] = &drav1.NodeUnprepareResourceResponse{}
	}
	return resp, nil
}

// draRegistrar handles the kubelet's plugin registration handshake.
type draRegistrar struct {
	regv1.UnimplementedRegistrationServer
	endpoint string
}

func (r *draRegistrar) GetInfo(ctx context.Context, req *regv1.InfoRequest) (*regv1.PluginInfo, error) {
	return &regv1.PluginInfo{
		Type:     regv1.DRAPlugin,
		Name:     DriverName,
		Endpoint: r.endpoint,
		// The entries are gRPC service names, not version numbers. The
		// list leaves out the device-health service, because a
		// `media.liken.sh` device has no health of its own.
		SupportedVersions: []string{drav1.DRAPluginService},
	}, nil
}

func (r *draRegistrar) NotifyRegistrationStatus(ctx context.Context, status *regv1.RegistrationStatus) (*regv1.RegistrationStatusResponse, error) {
	if !status.PluginRegistered {
		fmt.Fprintf(os.Stderr, "dra: the kubelet rejected the registration of the plugin: %s\n", status.Error)
	}
	return &regv1.RegistrationStatusResponse{}, nil
}

// serveDRAPlugin starts both servers and returns when the context ends
// or a server fails. It listens on the plugin socket before it creates
// the registration socket, because the kubelet dials the endpoint from
// GetInfo as soon as the registration socket appears. It removes each
// socket file before it listens, because a bind fails on a socket file
// that an earlier pod left, even when nothing listens on it.
func serveDRAPlugin(ctx context.Context) error {
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
	drav1.RegisterDRAPluginServer(pluginServer, draPlugin{})

	registrationSocket := filepath.Join(draRegistryDir, DriverName+"-reg.sock")
	_ = os.Remove(registrationSocket)
	registrationListener, err := net.Listen("unix", registrationSocket)
	if err != nil {
		pluginListener.Close()
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
		registrationServer.Stop()
		pluginServer.Stop()
		return err
	}
}
