package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StartController is the controller plugin on a socket of its own, which
// is the same binary with --controller.
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
	answer, err := client.GetPluginCapabilities(t.Context(),
		&csi.GetPluginCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetPluginCapabilities: %v", err)
	}
	declared := answer.GetCapabilities()
	if len(declared) != 1 {
		t.Fatalf("GetPluginCapabilities answered %v, want the controller service", declared)
	}
	if got := declared[0].GetService().GetType(); got != csi.PluginCapability_Service_CONTROLLER_SERVICE {
		t.Errorf("GetPluginCapabilities declared %v, want CONTROLLER_SERVICE", got)
	}
}

func TestTheControllerDeclaresModifyVolumeAlone(t *testing.T) {
	client := csi.NewControllerClient(startController(t))
	answer, err := client.ControllerGetCapabilities(t.Context(),
		&csi.ControllerGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("ControllerGetCapabilities: %v", err)
	}
	declared := answer.GetCapabilities()
	if len(declared) != 1 {
		t.Fatalf("ControllerGetCapabilities answered %v, want MODIFY_VOLUME alone", declared)
	}
	if got := declared[0].GetRpc().GetType(); got != csi.ControllerServiceCapability_RPC_MODIFY_VOLUME {
		t.Errorf("ControllerGetCapabilities declared %v, want MODIFY_VOLUME", got)
	}
}

func TestControllerModifyVolumeTakesAClassItCanRead(t *testing.T) {
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
				t.Errorf("ControllerModifyVolume: %v", err)
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
		t.Fatalf("ControllerModifyVolume answered %v, want %v", got, codes.InvalidArgument)
	}
	want := "push.quiesce: 1s is shorter than 5s"
	if got := status.Convert(err).Message(); got != want {
		t.Errorf("ControllerModifyVolume said %q, want %q", got, want)
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
			name: "DeleteVolume",
			call: func(ctx context.Context) error {
				_, err := client.DeleteVolume(ctx, &csi.DeleteVolumeRequest{})
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
				t.Fatalf("%s answered %v, want %v", c.name, got, codes.Unimplemented)
			}
			if got := status.Convert(err).Message(); !strings.HasPrefix(got, c.name+": never; ") {
				t.Errorf("%s said %q, want a reason", c.name, got)
			}
		})
	}
}

func TestTheControllerHoldsNoVolume(t *testing.T) {
	client := csi.NewNodeClient(startController(t))
	_, err := client.NodeGetInfo(t.Context(), &csi.NodeGetInfoRequest{})
	if got := status.Code(err); got != codes.Unimplemented {
		t.Errorf("NodeGetInfo answered %v, want %v", got, codes.Unimplemented)
	}
}
