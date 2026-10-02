package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthv1alpha1 "k8s.io/kubelet/pkg/apis/dra-health/v1alpha1"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
)

func TestAPrepareAnswersEachClaimWithNoDevice(t *testing.T) {
	resp, err := draPlugin{}.NodePrepareResources(context.Background(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{
			{Namespace: "media", Name: "worker-gpu", Uid: "uid-1"},
			{Namespace: "jellyfin", Name: "transcode", Uid: "uid-2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Claims) != 2 {
		t.Fatalf("claims = %v, want an entry for each", resp.Claims)
	}
	for uid, claim := range resp.Claims {
		if claim.Error != "" || len(claim.Devices) != 0 {
			t.Errorf("claim %s = %+v, want no error and no device", uid, claim)
		}
	}
}

func TestAnUnprepareAnswersEachClaimWithSuccess(t *testing.T) {
	resp, err := draPlugin{}.NodeUnprepareResources(context.Background(), &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "worker-gpu", Uid: "uid-1"}},
	})
	if err != nil || resp.Claims["uid-1"] == nil || resp.Claims["uid-1"].Error != "" {
		t.Errorf("resp = %+v, err = %v", resp, err)
	}
}

// The kubelet dials the registration socket and reads the driver's
// name and the plugin socket from GetInfo, then dials that socket.
func TestTheKubeletFindsThePluginThroughTheRegistrationSocket(t *testing.T) {
	dir := t.TempDir()
	savedRegistry, savedPlugin := draRegistryDir, draPluginDir
	draRegistryDir, draPluginDir = dir, filepath.Join(dir, DriverName)
	t.Cleanup(func() { draRegistryDir, draPluginDir = savedRegistry, savedPlugin })

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serveDRAPlugin(ctx) }()

	info := waitForInfo(t, filepath.Join(dir, DriverName+"-reg.sock"))
	if info.Name != DriverName || info.Type != regv1.DRAPlugin {
		t.Errorf("info = %+v", info)
	}
	plugin := dial(t, info.Endpoint)
	resp, err := drav1.NewDRAPluginClient(plugin).NodePrepareResources(ctx, &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Uid: "uid-1"}},
	})
	if err != nil || resp.Claims["uid-1"] == nil {
		t.Errorf("prepare over the socket = %+v, %v", resp, err)
	}

	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve returned %v after its context ended", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after its context ended")
	}
}

func dial(t *testing.T, socket string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// waitForInfo calls GetInfo until the socket answers, for up to ten
// seconds, because the servers start on their own goroutine.
func waitForInfo(t *testing.T, socket string) *regv1.PluginInfo {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := regv1.NewRegistrationClient(dial(t, socket)).GetInfo(ctx, &regv1.InfoRequest{}, grpc.WaitForReady(true))
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	return info
}

func TestTheRegistrarAnswersEitherRegistrationStatus(t *testing.T) {
	registrar := &draRegistrar{}
	for _, status := range []*regv1.RegistrationStatus{
		{PluginRegistered: true},
		{PluginRegistered: false, Error: "a plugin of this name is registered"},
	} {
		if _, err := registrar.NotifyRegistrationStatus(context.Background(), status); err != nil {
			t.Errorf("status %+v: %v", status, err)
		}
	}
}

// The health stream stays open and sends nothing until the kubelet
// closes it.
func TestTheHealthStreamEndsWhenTheKubeletClosesIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (draHealth{}).NodeWatchResources(nil, closedStream{ctx: ctx}); err != nil {
		t.Errorf("err = %v", err)
	}
}

type closedStream struct {
	grpc.ServerStreamingServer[healthv1alpha1.NodeWatchResourcesResponse]
	ctx context.Context
}

func (s closedStream) Context() context.Context { return s.ctx }

func TestAPluginDirectoryThatCannotBeMadeFailsTheServe(t *testing.T) {
	saved := draPluginDir
	draPluginDir = "/dev/null/media.liken.sh"
	t.Cleanup(func() { draPluginDir = saved })
	if err := serveDRAPlugin(context.Background()); err == nil {
		t.Error("serve made a directory under /dev/null")
	}
}
