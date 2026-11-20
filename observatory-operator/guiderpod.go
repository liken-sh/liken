package main

// A Guider runs as a ConfigMap with PHD2's profile and weston's config,
// a pod that runs PHD2 beside a headless weston, and a Service for
// PHD2's event server. PHD2 is a GTK program with no headless mode, so
// it draws on weston through a Wayland socket in an emptyDir that both
// containers mount. weston creates the socket with no write permission
// for other users, so both containers run as the same user. PHD2
// rewrites its config while it runs, so the
// image's entrypoint copies the profile from the ConfigMap into $HOME
// before each start, and every start uses the operator's profile.

import (
	"fmt"
	"math"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/drivers"
	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2"
)

const roleGuider = "guider"

// These paths must match the environment of the indi/images/phd2 image.
// PHD2 writes its config, logs, and instance lock in guiderHome, and the
// entrypoint copies profileDir/PHDGuidingV2 to $HOME/.PHDGuidingV2.
const (
	guiderHome    = "/home/phd2"
	waylandDir    = "/run/wayland"
	waylandSocket = "wayland-0"
	profileDir    = "/etc/phd2"
	profileKey    = "PHDGuidingV2"
)

// weston reads its config from the read-only ConfigMap mount, and its
// layout module writes a control socket in westonRunDir, so that
// directory is a separate, writeable emptyDir.
const (
	westonConfigDir = "/etc/weston-config"
	westonKey       = "weston.ini"
	westonRunDir    = "/etc/weston"
)

// westonConfig is the config that weston/smoke/weston.sh tests, with
// the layout module that display-operator also loads. The pod has no
// monitor or input device, so weston runs the headless backend and
// does not require input.
const westonConfig = `[core]
shell=ivi-shell.so
modules=liken-layout.so
renderer=gl
require-input=false
`

// guiderEquipment is the equipment that PHD2's profile selects: the
// INDI device names of the guide camera and the mount on the
// telescope's INDI server, and the guide tube's focal length.
type guiderEquipment struct {
	camera string
	// mount is empty for a telescope with no Mount.
	mount string
	// focalLength is in millimeters.
	focalLength float64
}

// guiderProfile returns PHD2's whole config, with one profile. The
// keys are the ones that PHD2 reads (OpenPHDGuiding/phd2, src/):
//
//   - ConfigVersion is CURRENT_CONFIG_VERSION in phdconfig.h. With no
//     version, PhdConfig treats the config as new, and PHD2 opens its
//     modal first-light wizard (phdconfig.cpp, phd.cpp).
//   - currentProfile selects profile 1 (PhdConfig::InitializeProfile).
//   - /camera/LastMenuChoice and /scope/LastMenuChoice select the
//     equipment (GearDialog::Initialize). The INDI choices are the
//     strings that camera.cpp and scope.cpp list: "INDI Camera
//     [<device>]" and "INDI Mount [<device>]". "On-camera" sends guide
//     pulses through the camera's ST-4 port (Scope::MountList).
//   - /indi/INDIhost, INDIport, INDIcam, INDIcam_ccd, and INDImount
//     are the server and the devices (cam_indi.cpp, scope_indi.cpp).
//     When a device name is left at its default, PHD2 opens a modal
//     setup dialog as it connects.
//   - /frame/focalLength is the guide tube's focal length, an integer
//     in millimeters (MyFrame::LoadProfileSettings). PHD2 computes the
//     pixel scale from it and the camera's pixel size.
//   - AutoLoadDarks and AutoLoadDefectMap are off, because a new $HOME
//     has no dark library.
//
// The profile has no /camera/pixelsize, so PHD2 reads the pixel size
// from the driver. When a stored size differs from the driver's by 1%,
// PHD2 opens the modal "Camera Change Warning" (gear_dialog.cpp).
func guiderProfile(guider string, pulses observatory.PulseTarget, server string, gear guiderEquipment) string {
	scope := "On-camera"
	if pulses == observatory.PulsesMount {
		scope = "INDI Mount [" + gear.mount + "]"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ConfigVersion=2001\ncurrentProfile=1\n")
	fmt.Fprintf(&b, "[profile]\n[profile/1]\nname=%s\n", guider)
	fmt.Fprintf(&b, "[profile/1/camera]\nLastMenuChoice=INDI Camera [%s]\nAutoLoadDarks=0\nAutoLoadDefectMap=0\n", gear.camera)
	fmt.Fprintf(&b, "[profile/1/scope]\nLastMenuChoice=%s\n", scope)
	fmt.Fprintf(&b, "[profile/1/frame]\nfocalLength=%d\n", int(math.Round(gear.focalLength)))
	fmt.Fprintf(&b, "[profile/1/indi]\nINDIhost=%s\nINDIport=%d\nINDIcam=%s\nINDIcam_ccd=0\n", server, serverPort, gear.camera)
	if gear.mount != "" {
		fmt.Fprintf(&b, "INDImount=%s\n", gear.mount)
	}
	fmt.Fprintf(&b, "[profile/1/stepguider]\nLastMenuChoice=None\n[profile/1/rotator]\nLastMenuChoice=None\n")
	return b.String()
}

// guiderPod returns the ConfigMap, the pod, and the Service of one
// Guider. Its PHD2 is a client of the INDI server ref.
func guiderPod(namespace string, ref serverRef, guider *observatory.Guider, gear guiderEquipment) (*configMap, *pod, *service, error) {
	name, err := objectName(observatory.GuiderKind, guider.Metadata.Name)
	if err != nil {
		return nil, nil, nil, err
	}
	owners := []ownerReference{owner(observatory.APIVersion, observatory.GuiderKind, guider.Metadata.Name, guider.Metadata.UID)}
	podLabels := labels(name, roleGuider, ref.String(), observatory.GuiderKind, guider.Metadata.Name)
	files := &configMap{
		APIVersion: "v1", Kind: "ConfigMap",
		Metadata: meta{Name: name, Namespace: namespace, Labels: podLabels, OwnerReferences: owners},
		Data: map[string]string{
			profileKey: guiderProfile(guider.Metadata.Name, guider.Spec.Pulses, serviceHost(ref.String(), namespace), gear),
			westonKey:  westonConfig,
		},
	}
	runtime := volumeMount{Name: "wayland", MountPath: waylandDir}
	compositor := container{
		Name:  "compositor",
		Image: drivers.CompositorImage(),
		// As a native sidecar (an init container with RestartPolicy
		// Always), weston starts first, and the kubelet starts PHD2
		// only after the startup probe passes. So the Wayland socket
		// exists before PHD2 starts.
		RestartPolicy:   "Always",
		Args:            []string{"--backend=headless", "--config=" + westonConfigDir + "/" + westonKey, "--socket=" + waylandSocket},
		Env:             []envVar{{Name: "XDG_RUNTIME_DIR", Value: waylandDir}, {Name: "WAYLAND_DISPLAY", Value: waylandSocket}},
		SecurityContext: restricted(),
		VolumeMounts: []volumeMount{
			runtime,
			{Name: "files", MountPath: westonConfigDir, ReadOnly: true},
			{Name: "weston", MountPath: westonRunDir},
		},
		// wayland-info fails until weston serves the socket.
		StartupProbe: &probe{Exec: &execAction{Command: []string{"/usr/bin/wayland-info"}}, PeriodSeconds: 1, FailureThreshold: 30},
	}
	guide := container{
		Name:            "phd2",
		Image:           drivers.GuiderImage(),
		Ports:           []containerPort{{Name: "events", ContainerPort: phd2.Port}},
		SecurityContext: restricted(),
		VolumeMounts: []volumeMount{
			runtime,
			{Name: "files", MountPath: profileDir, ReadOnly: true},
			{Name: "home", MountPath: guiderHome},
			{Name: "tmp", MountPath: tmpDir},
		},
		// The operator connects to the event server once the pod is
		// Ready. The probe opens a TCP connection and closes it at once,
		// which does no harm, because PHD2 only sends each new client its
		// current state.
		ReadinessProbe: &probe{TCPSocket: &tcpSocket{Port: "events"}, PeriodSeconds: 10},
	}
	p := &pod{
		APIVersion: "v1", Kind: "Pod",
		Metadata: meta{Name: name, Namespace: namespace, Labels: podLabels, OwnerReferences: owners},
		Spec: podSpec{
			RestartPolicy: "Always",
			// PHD2 and weston run as process 1 of their containers and
			// neither handles SIGTERM, so a stop would wait out the
			// whole grace period. PHD2 has no state to save, because the
			// profile comes from the ConfigMap at each start.
			TerminationGracePeriodSeconds: int64Pointer(1),
			EnableServiceLinks:            boolPointer(false),
			AutomountServiceAccountToken:  boolPointer(false),
			InitContainers:                []container{compositor},
			Containers:                    []container{guide},
			Volumes: []volume{
				{Name: "files", ConfigMap: &configMapSource{Name: name}},
				{Name: "wayland", EmptyDir: &emptyDir{}},
				{Name: "weston", EmptyDir: &emptyDir{}},
				{Name: "home", EmptyDir: &emptyDir{}},
				{Name: "tmp", EmptyDir: &emptyDir{}},
			},
			// PHD2 downloads every guide frame from the INDI server, so
			// the pod runs on the server's node (placement.go).
			Affinity: besideServer(ref),
		},
	}
	return files, stampedWith(p, files.Data[profileKey]), serviceFor(namespace, name, podLabels, owners, "events", phd2.Port), nil
}

// guiderEndpoint returns the address of a guider's event server.
func guiderEndpoint(namespace, guider string) observatory.Endpoint {
	name := generatedName(observatory.GuiderKind, guider)
	return observatory.Endpoint{Service: name, Host: serviceHost(name, namespace), Port: phd2.Port}
}
