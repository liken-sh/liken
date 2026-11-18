package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// startController runs the driver's controller subcommand on its own
// socket and returns a connection to it.
func startController(t *testing.T) *grpc.ClientConn {
	t.Helper()
	dir := t.TempDir()
	return start(t, &config{
		endpoint:   "unix://" + filepath.Join(dir, "csi.sock"),
		store:      filepath.Join(dir, "store"),
		controller: true,
	}, io.Discard)
}

func TestTheControllerDeclaresTheControllerService(t *testing.T) {
	client := csi.NewIdentityClient(startController(t))
	response, err := client.GetPluginCapabilities(t.Context(),
		&csi.GetPluginCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("calling GetPluginCapabilities: %v", err)
	}
	declared := response.GetCapabilities()
	if len(declared) != 1 {
		t.Fatalf("GetPluginCapabilities returned %v, want the controller service", declared)
	}
	if got := declared[0].GetService().GetType(); got != csi.PluginCapability_Service_CONTROLLER_SERVICE {
		t.Errorf("GetPluginCapabilities declared %v, want CONTROLLER_SERVICE", got)
	}
}

func TestTheControllerDeclaresModifyVolumeAndDeleteVolume(t *testing.T) {
	client := csi.NewControllerClient(startController(t))
	response, err := client.ControllerGetCapabilities(t.Context(),
		&csi.ControllerGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("calling ControllerGetCapabilities: %v", err)
	}
	var declared []csi.ControllerServiceCapability_RPC_Type
	for _, one := range response.GetCapabilities() {
		declared = append(declared, one.GetRpc().GetType())
	}
	want := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_MODIFY_VOLUME,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	}
	if !slices.Equal(declared, want) {
		t.Errorf("ControllerGetCapabilities declared %v, want %v", declared, want)
	}
}

func TestDeleteVolumeSucceedsForEveryVolumeID(t *testing.T) {
	client := csi.NewControllerClient(startController(t))
	if _, err := client.DeleteVolume(t.Context(),
		&csi.DeleteVolumeRequest{VolumeId: "config"}); err != nil {
		t.Errorf("DeleteVolume returned an error: %v, want none", err)
	}
}

func TestDeleteVolumeRefusesACallThatNamesNoVolume(t *testing.T) {
	client := csi.NewControllerClient(startController(t))
	_, err := client.DeleteVolume(t.Context(), &csi.DeleteVolumeRequest{})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("DeleteVolume returned %v, want %v", got, codes.InvalidArgument)
	}
}

func TestTheControllerDeclaresNoNodeCapability(t *testing.T) {
	client := csi.NewNodeClient(startController(t))
	response, err := client.NodeGetCapabilities(t.Context(),
		&csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("calling NodeGetCapabilities: %v", err)
	}
	if declared := response.GetCapabilities(); len(declared) != 0 {
		t.Errorf("NodeGetCapabilities returned %v, want none", declared)
	}
}

func TestTheControllerTracksNoVolumesOfANode(t *testing.T) {
	client := csi.NewNodeClient(startController(t))
	_, err := client.NodeGetInfo(t.Context(), &csi.NodeGetInfoRequest{})
	if got := status.Code(err); got != codes.Unimplemented {
		t.Fatalf("NodeGetInfo returned %v, want %v", got, codes.Unimplemented)
	}
	if got := status.Convert(err).Message(); !strings.Contains(got, "NodeGetInfo") {
		t.Errorf("NodeGetInfo returned the message %q, want one that names the call", got)
	}
}

func TestControllerModifyVolumeAcceptsAValidClass(t *testing.T) {
	client := csi.NewControllerClient(startController(t))
	for _, c := range []struct {
		name       string
		parameters map[string]string
	}{
		{name: "an empty class"},
		{
			name: "every parameter",
			parameters: map[string]string{
				quiesceParameter:     "30s",
				maxLatencyParameter:  "5m",
				maxFileSizeParameter: "1Mi",
				authorParameter:      "Home Assistant <homeassistant@home.example>",
				ignoreParameter:      ".storage/,*.db*",
				metadataParameter:    "true",
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := client.ControllerModifyVolume(t.Context(),
				&csi.ControllerModifyVolumeRequest{
					VolumeId:          "config",
					MutableParameters: c.parameters,
				}); err != nil {
				t.Errorf("ControllerModifyVolume returned an error: %v, want none", err)
			}
		})
	}
}

func TestControllerModifyVolumeRefusesAClassItCannotRead(t *testing.T) {
	client := csi.NewControllerClient(startController(t))
	_, err := client.ControllerModifyVolume(t.Context(), &csi.ControllerModifyVolumeRequest{
		VolumeId:          "config",
		MutableParameters: map[string]string{quiesceParameter: "1s"},
	})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("ControllerModifyVolume returned %v, want %v", got, codes.InvalidArgument)
	}
	want := "push.quiesce: 1s is shorter than 5s"
	if got := status.Convert(err).Message(); got != want {
		t.Errorf("ControllerModifyVolume returned %q, want %q", got, want)
	}
}

func TestTheControllerServesNoOtherCall(t *testing.T) {
	client := csi.NewControllerClient(startController(t))
	for _, c := range []struct {
		name string
		call func(ctx context.Context) error
	}{
		{
			name: "CreateVolume",
			call: func(ctx context.Context) error {
				_, err := client.CreateVolume(ctx, &csi.CreateVolumeRequest{})
				return err
			},
		},
		{
			name: "ControllerPublishVolume",
			call: func(ctx context.Context) error {
				_, err := client.ControllerPublishVolume(ctx, &csi.ControllerPublishVolumeRequest{})
				return err
			},
		},
		{
			name: "ControllerUnpublishVolume",
			call: func(ctx context.Context) error {
				_, err := client.ControllerUnpublishVolume(ctx, &csi.ControllerUnpublishVolumeRequest{})
				return err
			},
		},
		{
			name: "ValidateVolumeCapabilities",
			call: func(ctx context.Context) error {
				_, err := client.ValidateVolumeCapabilities(ctx, &csi.ValidateVolumeCapabilitiesRequest{})
				return err
			},
		},
		{
			name: "ListVolumes",
			call: func(ctx context.Context) error {
				_, err := client.ListVolumes(ctx, &csi.ListVolumesRequest{})
				return err
			},
		},
		{
			name: "GetCapacity",
			call: func(ctx context.Context) error {
				_, err := client.GetCapacity(ctx, &csi.GetCapacityRequest{})
				return err
			},
		},
		{
			name: "CreateSnapshot",
			call: func(ctx context.Context) error {
				_, err := client.CreateSnapshot(ctx, &csi.CreateSnapshotRequest{})
				return err
			},
		},
		{
			name: "DeleteSnapshot",
			call: func(ctx context.Context) error {
				_, err := client.DeleteSnapshot(ctx, &csi.DeleteSnapshotRequest{})
				return err
			},
		},
		{
			name: "ListSnapshots",
			call: func(ctx context.Context) error {
				_, err := client.ListSnapshots(ctx, &csi.ListSnapshotsRequest{})
				return err
			},
		},
		{
			name: "GetSnapshot",
			call: func(ctx context.Context) error {
				_, err := client.GetSnapshot(ctx, &csi.GetSnapshotRequest{})
				return err
			},
		},
		{
			name: "ControllerExpandVolume",
			call: func(ctx context.Context) error {
				_, err := client.ControllerExpandVolume(ctx, &csi.ControllerExpandVolumeRequest{})
				return err
			},
		},
		{
			name: "ControllerGetVolume",
			call: func(ctx context.Context) error {
				_, err := client.ControllerGetVolume(ctx, &csi.ControllerGetVolumeRequest{})
				return err
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.call(t.Context())
			if got := status.Code(err); got != codes.Unimplemented {
				t.Fatalf("%s returned %v, want %v", c.name, got, codes.Unimplemented)
			}
			if got := status.Convert(err).Message(); !strings.HasPrefix(got, c.name+": ") {
				t.Errorf("%s returned the message %q, want a reason after the call name", c.name, got)
			}
		})
	}
}

func TestTheControllerTracksNoVolume(t *testing.T) {
	client := csi.NewNodeClient(startController(t))
	_, err := client.NodeGetInfo(t.Context(), &csi.NodeGetInfoRequest{})
	if got := status.Code(err); got != codes.Unimplemented {
		t.Errorf("NodeGetInfo returned %v, want %v", got, codes.Unimplemented)
	}
}

func TestTheControllerReportsAWebhookAddressItCannotListenOn(t *testing.T) {
	dir := t.TempDir()
	_, err := newServer(t.Context(), &config{
		endpoint:   "unix://" + filepath.Join(dir, "csi.sock"),
		store:      filepath.Join(dir, "store"),
		controller: true,
		webhook:    "127.0.0.1:-1",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Error("newServer returned no error for a webhook address that cannot be bound, want one")
	}
}
