package main

// The guider's objects: a ConfigMap with PHD2's profile and weston's
// config, a pod that runs PHD2 beside a headless weston, and a Service
// for PHD2's event server. PHD2 is a GTK program with no headless mode,
// so it draws on the compositor through a Wayland socket that the two
// containers share in an emptyDir. PHD2 rewrites its config while it
// runs, so the image's entrypoint copies the profile from the
// ConfigMap into $HOME before each start, and PHD2 keeps nothing
// between starts.

import (
	"fmt"
	"math"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/drivers"
	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2"
)

// roleGuider is the role of a guider's pod and Service.
const roleGuider = "guider"

// The paths that the image of indi/images/phd2 sets in its environment:
// PHD2's $HOME, where it writes its config, its logs, and its instance
// lock, the Wayland runtime directory, and the directory whose
// PHDGuidingV2 the entrypoint copies to $HOME/.PHDGuidingV2.
const (
	guiderHome    = "/home/phd2"
	waylandDir    = "/run/wayland"
	waylandSocket = "wayland-0"
	profileDir    = "/etc/phd2"
	profileKey    = "PHDGuidingV2"
)

// The compositor reads its config from the ConfigMap, and its layout
// module writes its control socket in /etc/weston, so that directory is
// an emptyDir of its own.
const (
	westonConfigDir = "/etc/weston-config"
	westonKey       = "weston.ini"
	westonRunDir    = "/etc/weston"
)

// westonConfig is the config of weston/smoke/weston.sh, the one the
// image's smoke check proves: ivi-shell with the layout module that
// display-operator also loads, the GL renderer, and no input device.
// The headless backend draws to memory, and no person watches it.
const westonConfig = `[core]
shell=ivi-shell.so
modules=liken-layout.so
renderer=gl
require-input=false
`

// guiderEquipment is what PHD2's profile names: the INDI devices of
// the guide camera and the mount, as the telescope's server names
// them, and the guide tube's focal length.
type guiderEquipment struct {
	camera string
	// mount is empty for a telescope with no Mount.
	mount string
	// focalLength is in millimeters.
	focalLength float64
}

// guiderProfile writes PHD2's whole config, with one profile. Each key
// is the one PHD2 reads (OpenPHDGuiding/phd2, src/):
//
//   - ConfigVersion is CURRENT_CONFIG_VERSION in phdconfig.h. With no
//     version, PhdConfig treats the config as new, and PHD2 opens its
//     modal first-light wizard (phdconfig.cpp, phd.cpp).
//   - currentProfile selects profile 1 (PhdConfig::InitializeProfile).
//   - /camera/LastMenuChoice and /scope/LastMenuChoice select the gear
//     (GearDialog::Initialize). The INDI names are the ones that
//     camera.cpp and scope.cpp list: "INDI Camera [<device>]" and
//     "INDI Mount [<device>]". "On-camera" sends the pulses through the
//     camera's ST-4 port (Scope::MountList).
//   - /indi/INDIhost, INDIport, INDIcam, INDIcam_ccd, and INDImount
//     name the server and the devices (cam_indi.cpp, scope_indi.cpp).
//     A device name left at its default makes PHD2 open its modal
//     setup dialog when it connects.
//   - /frame/focalLength is the guide tube's focal length, an integer
//     in millimeters (MyFrame::LoadProfileSettings). PHD2 computes the
//     pixel scale from it and the camera's pixel size.
//   - AutoLoadDarks and AutoLoadDefectMap are off: no dark library
//     exists in a new $HOME.
//
// The profile leaves /camera/pixelsize out, so PHD2 reads the pixel
// size from the driver. A stored size that differs from the driver's
// by 1% opens the modal "Camera Change Warning" (gear_dialog.cpp).
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

// guiderPod answers the ConfigMap, the pod, and the Service of one
// Guider, whose PHD2 is a client of the server ref.
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
		// A native sidecar starts before PHD2, and the kubelet starts
		// PHD2 only when the startup probe passes, so PHD2 never starts
		// before the Wayland socket exists.
		RestartPolicy:   "Always",
		Args:            []string{"--backend=headless", "--config=" + westonConfigDir + "/" + westonKey, "--socket=" + waylandSocket},
		Env:             []envVar{{Name: "XDG_RUNTIME_DIR", Value: waylandDir}, {Name: "WAYLAND_DISPLAY", Value: waylandSocket}},
		SecurityContext: restricted(),
		VolumeMounts: []volumeMount{
			runtime,
			{Name: "files", MountPath: westonConfigDir, ReadOnly: true},
			{Name: "weston", MountPath: westonRunDir},
		},
		// wayland-info connects to the socket and lists the globals,
		// and fails while weston is not serving it.
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
		// The operator opens its connection to the event server when the
		// pod is Ready. PHD2 sends a new client its catch-up events, and
		// a probe that closes at once costs it nothing more.
		ReadinessProbe: &probe{TCPSocket: &tcpSocket{Port: "events"}, PeriodSeconds: 10},
	}
	p := &pod{
		APIVersion: "v1", Kind: "Pod",
		Metadata: meta{Name: name, Namespace: namespace, Labels: podLabels, OwnerReferences: owners},
		Spec: podSpec{
			RestartPolicy: "Always",
			// PHD2 and weston run as process 1 of their containers, and
			// neither handles SIGTERM, so a stop waits out the grace
			// period. PHD2 holds no state to save: the profile comes
			// from the ConfigMap at each start.
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
			// PHD2 reads each guide frame from the server, about once a
			// second, so it runs on the server's node (placement.go).
			Affinity: besideServer(ref),
		},
	}
	return files, stampedWith(p, files.Data[profileKey]), serviceFor(namespace, name, podLabels, owners, "events", phd2.Port), nil
}

// guiderEndpoint answers the address of a guider's event server.
func guiderEndpoint(namespace, guider string) observatory.Endpoint {
	name := generatedName(observatory.GuiderKind, guider)
	return observatory.Endpoint{Service: name, Host: serviceHost(name, namespace), Port: phd2.Port}
}
