package main

import (
	"context"
	"io"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNodeGetInfoNamesTheNode(t *testing.T) {
	client := csi.NewNodeClient(startServer(t, io.Discard))
	info, err := client.NodeGetInfo(t.Context(), &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatalf("NodeGetInfo: %v", err)
	}
	if info.GetNodeId() != "node-1" {
		t.Errorf("NodeGetInfo answered %q, want %q", info.GetNodeId(), "node-1")
	}
	if info.GetAccessibleTopology() != nil {
		t.Errorf("NodeGetInfo answered topology %v, want none", info.GetAccessibleTopology())
	}
}

func TestNodeGetCapabilitiesDeclaresTheThree(t *testing.T) {
	client := csi.NewNodeClient(startServer(t, io.Discard))
	answer, err := client.NodeGetCapabilities(t.Context(), &csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("NodeGetCapabilities: %v", err)
	}
	var got []csi.NodeServiceCapability_RPC_Type
	for _, capability := range answer.GetCapabilities() {
		got = append(got, capability.GetRpc().GetType())
	}
	want := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		csi.NodeServiceCapability_RPC_VOLUME_CONDITION,
	}
	if len(got) != len(want) {
		t.Fatalf("NodeGetCapabilities answered %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("NodeGetCapabilities answered %v, want %v", got, want)
		}
	}
}

func TestEveryOtherNodeRPCNamesThePlanThatAddsIt(t *testing.T) {
	client := csi.NewNodeClient(startServer(t, io.Discard))
	for _, c := range []struct {
		name    string
		call    func(context.Context, csi.NodeClient) error
		message string
	}{
		{
			name: "NodeStageVolume",
			call: func(ctx context.Context, client csi.NodeClient) error {
				_, err := client.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{})
				return err
			},
			message: "NodeStageVolume: plan 03",
		},
		{
			name: "NodeUnstageVolume",
			call: func(ctx context.Context, client csi.NodeClient) error {
				_, err := client.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{})
				return err
			},
			message: "NodeUnstageVolume: plan 03",
		},
		{
			name: "NodePublishVolume",
			call: func(ctx context.Context, client csi.NodeClient) error {
				_, err := client.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{})
				return err
			},
			message: "NodePublishVolume: plan 03",
		},
		{
			name: "NodeUnpublishVolume",
			call: func(ctx context.Context, client csi.NodeClient) error {
				_, err := client.NodeUnpublishVolume(ctx, &csi.NodeUnpublishVolumeRequest{})
				return err
			},
			message: "NodeUnpublishVolume: plan 03",
		},
		{
			name: "NodeGetVolumeStats",
			call: func(ctx context.Context, client csi.NodeClient) error {
				_, err := client.NodeGetVolumeStats(ctx, &csi.NodeGetVolumeStatsRequest{})
				return err
			},
			message: "NodeGetVolumeStats: plan 03",
		},
		{
			name: "NodeExpandVolume",
			call: func(ctx context.Context, client csi.NodeClient) error {
				_, err := client.NodeExpandVolume(ctx, &csi.NodeExpandVolumeRequest{})
				return err
			},
			message: "NodeExpandVolume: never; git volumes have no size",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.call(t.Context(), client)
			if got := status.Code(err); got != codes.Unimplemented {
				t.Fatalf("%s answered %v, want %v", c.name, got, codes.Unimplemented)
			}
			if got := status.Convert(err).Message(); got != c.message {
				t.Errorf("%s said %q, want %q", c.name, got, c.message)
			}
		})
	}
}
