package main

// Pods and Services of an observatory. Each device runs in its own pod
// as `socat TCP-LISTEN:7625,reuseaddr EXEC:<driver>,pipes`, behind its
// own Service. Each INDI server pod runs indiserver with one link to
// indi-shim for each device it serves, and the shim connects to that
// device's Service. The server pod reads its device list from an
// annotation, so a running server can gain or lose devices
// (serverdrivers.go).
//
// The pods are bare pods, with no controller above them. The kubelet
// restarts a container that exits, which a driver needs: socat exits
// when the shim's connection ends, and the restarted socat serves the
// next connection. When a pod is deleted or evicted, the operator
// creates it again on the next event of its pod watch.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/drivers"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// Paths inside a server's pod. HOME is /tmp in the INDI images, so INDI
// writes its configuration there, and /tmp is an emptyDir because the
// root filesystem is read-only.
const (
	linksDir    = "/run/indi/drivers"
	devicesDir  = "/etc/indi/devices"
	driversFile = devicesDir + "/drivers"
	tmpDir      = "/tmp"
	fifoPath    = tmpDir + "/indiserver.fifo"
)

// maxRestarts is indiserver's -r option, the largest value that its
// atoi parse accepts. indiserver restarts an exited driver at most -r
// times and never resets the count. Each exit of a shim uses one
// restart, so the limit must be too large to reach.
const maxRestarts = "2147483647"

const (
	outboxVolume = "outbox"
	outboxDir    = "/outbox"
)

// claimName is the name, inside the pod, of a device's ResourceClaim.
//
// The pod gets its claim from a ResourceClaimTemplate, so the claim
// belongs to the pod and is deleted with it. When a person moves a
// device's cable to another port or machine, `liken` taints the
// unplugged device and Kubernetes evicts the pod. The operator creates
// the pod again, and the new claim is allocated on the device where it
// is now. A standalone claim outlives its pods and keeps the old
// allocation, so each new pod would fail on a device that is gone.
const claimName = "device"

// restricted is the security context of every container. With the
// read-only root filesystem, a container writes only to its emptyDir
// volumes.
func restricted() *securityContext {
	return &securityContext{
		RunAsNonRoot:             true,
		RunAsUser:                1000,
		RunAsGroup:               1000,
		AllowPrivilegeEscalation: false,
		ReadOnlyRootFilesystem:   true,
		Capabilities:             &capabilities{Drop: []string{"ALL"}},
	}
}

func owner(apiVersion string, kind observatory.Kind, name, uid string) ownerReference {
	return ownerReference{APIVersion: apiVersion, Kind: kind.Name, Name: name, UID: uid, Controller: true}
}

func boolPointer(b bool) *bool    { return &b }
func int64Pointer(i int64) *int64 { return &i }

// devicePod returns the pod and the Service of one device on one
// server, and a ResourceClaimTemplate when the device has spec.claim.
// guides is true for a Guider's camera (placement.go). outbox is the
// claim to mount at /outbox, or "" for none (outbox.go).
func devicePod(namespace string, server serverRef, d *device, guides bool, outbox string) (*pod, *service, *claimTemplate, error) {
	name, err := objectName(d.kind, d.name())
	if err != nil {
		return nil, nil, nil, err
	}
	image, err := drivers.Resolve(d.object.Spec.Driver)
	if err != nil {
		return nil, nil, nil, err
	}
	owners := []ownerReference{owner(observatory.APIVersion, d.kind, d.name(), d.object.Metadata.UID)}
	podLabels := labels(name, roleDevice, server.String(), d.kind, d.name())
	driver := container{
		Name:    "driver",
		Image:   image,
		Command: []string{"/usr/bin/socat"},
		// With pipes, the driver sends each BLOB inline. Over a Unix
		// socket it would send each BLOB as a file descriptor, which
		// socat cannot pass on, and the driver would crash on its first
		// frame.
		Args:            []string{"TCP-LISTEN:" + strconv.Itoa(devicePort) + ",reuseaddr", "EXEC:" + d.object.Spec.Driver.Name + ",pipes"},
		Ports:           []containerPort{{Name: "driver", ContainerPort: devicePort}},
		SecurityContext: restricted(),
		VolumeMounts:    []volumeMount{{Name: "tmp", MountPath: tmpDir}},
	}
	p := &pod{
		APIVersion: "v1", Kind: "Pod",
		Metadata: meta{Name: name, Namespace: namespace, Labels: podLabels, OwnerReferences: owners},
		Spec: podSpec{
			RestartPolicy: "Always",
			// The namespace has a Service for every device, and the
			// driver reads none of the environment variables that
			// Kubernetes would add for each Service.
			EnableServiceLinks: boolPointer(false),
			// A driver talks only to the INDI server and its hardware,
			// so it needs no API server credentials.
			AutomountServiceAccountToken: boolPointer(false),
			Containers:                   []container{driver},
			Volumes:                      []volume{{Name: "tmp", EmptyDir: &emptyDir{}}},
			Affinity:                     placement(server, d, guides),
		},
	}
	if outbox != "" {
		mountOutbox(p, outbox)
	}
	var template *claimTemplate
	if len(d.object.Spec.Claim) > 0 {
		var spec any
		if err := json.Unmarshal(d.object.Spec.Claim, &spec); err != nil {
			return nil, nil, nil, fmt.Errorf("reading spec.claim of the %s %s: %w", d.kind.Name, d.name(), err)
		}
		template = &claimTemplate{
			APIVersion: "resource.k8s.io/v1", Kind: "ResourceClaimTemplate",
			Metadata: meta{Name: name, Namespace: namespace, Labels: podLabels, OwnerReferences: owners},
			Spec:     claimTemplateSpec{Spec: spec},
		}
		p.Spec.ResourceClaims = []podClaim{{Name: claimName, ResourceClaimTemplateName: name}}
		p.Spec.Containers[0].Resources = &resources{Claims: []resourceName{{Name: claimName}}}
	}
	return stamped(p), serviceFor(namespace, name, podLabels, owners, "driver", devicePort), template, nil
}

// serverPod returns the pod and the Service of one INDI server.
func serverPod(namespace string, server serverRef, ownerUID string, devices []*device) (*pod, *service, error) {
	name, err := objectName(server.kind, server.name)
	if err != nil {
		return nil, nil, err
	}
	list, err := driverList(devices)
	if err != nil {
		return nil, nil, err
	}
	owners := []ownerReference{owner(observatory.APIVersion, server.kind, server.name, ownerUID)}
	podLabels := labels(name, roleServer, name, server.kind, server.name)
	p := &pod{
		APIVersion: "v1", Kind: "Pod",
		Metadata: meta{
			Name: name, Namespace: namespace, Labels: podLabels, OwnerReferences: owners,
			Annotations: map[string]string{annotationDrivers: list},
		},
		Spec: podSpec{
			RestartPolicy: "Always",
			// The shim is process 1 and stops indiserver on SIGTERM.
			// The server keeps no state, so a long grace period would
			// only make a stop hang longer when every device is
			// offline.
			TerminationGracePeriodSeconds: int64Pointer(1),
			EnableServiceLinks:            boolPointer(false),
			AutomountServiceAccountToken:  boolPointer(false),
			Containers: []container{{
				Name:  "indiserver",
				Image: drivers.ServerImage(),
				// The shim starts indiserver reading commands from the
				// fifo, creates a link for each device in driversFile, and
				// writes a start or stop command to the fifo when the file
				// changes.
				Command: []string{
					"/indi-shim", "serve", driversFile, linksDir, fifoPath,
					"/usr/bin/indiserver", "-v", "-r", maxRestarts,
				},
				Ports:           []containerPort{{Name: "indi", ContainerPort: serverPort}},
				SecurityContext: restricted(),
				VolumeMounts: []volumeMount{
					{Name: "drivers", MountPath: linksDir},
					{Name: "devices", MountPath: devicesDir, ReadOnly: true},
					{Name: "tmp", MountPath: tmpDir},
				},
				// The operator connects to INDI when the pod is Ready, and
				// a probe's connection costs indiserver nothing. A device
				// pod has no probe, because each connection to socat starts
				// the driver, and the probe would then end it.
				ReadinessProbe: &probe{TCPSocket: &tcpSocket{Port: "indi"}, PeriodSeconds: 10},
			}},
			Volumes: []volume{
				{Name: "drivers", EmptyDir: &emptyDir{}},
				// The device list is a downward API volume because the
				// kubelet rewrites it on each update of the pod, within
				// about a second of an annotation change. A ConfigMap
				// volume changes only at the kubelet's next sync of the
				// pod, up to about a minute later.
				{Name: "devices", DownwardAPI: &downwardAPISource{Items: []downwardAPIFile{{
					Path: "drivers", FieldRef: fieldSource{FieldPath: "metadata.annotations['" + annotationDrivers + "']"},
				}}}},
				{Name: "tmp", EmptyDir: &emptyDir{}},
			},
		},
	}
	return stamped(p), serviceFor(namespace, name, podLabels, owners, "indi", serverPort), nil
}

// driverList returns the value of annotationDrivers for a server: the
// Service address of each device that runs its own driver, one per
// line, in the order of devices. A device that another device's driver
// serves has no pod of its own, so it has no line.
func driverList(devices []*device) (string, error) {
	var b strings.Builder
	for _, d := range driverDevices(devices) {
		object, err := objectName(d.kind, d.name())
		if err != nil {
			return "", fmt.Errorf("%s %s: %w", d.kind.Name, d.name(), err)
		}
		b.WriteString(object + ":" + strconv.Itoa(devicePort) + "\n")
	}
	return b.String(), nil
}

func serviceFor(namespace, name string, objectLabels map[string]string, owners []ownerReference, port string, number int32) *service {
	return &service{
		APIVersion: "v1", Kind: "Service",
		Metadata: meta{Name: name, Namespace: namespace, Labels: objectLabels, OwnerReferences: owners},
		Spec: serviceSpec{
			Selector: map[string]string{labelName: name},
			Ports:    []servicePort{{Name: port, Port: number, TargetPort: port}},
		},
	}
}

// stamped records a digest of the pod's spec in an annotation. The
// operator compares it with the digest on the running pod and replaces
// a pod whose spec changed, because Kubernetes refuses most changes to
// a running pod's spec.
func stamped(p *pod) *pod { return stampedWith(p, "") }

// stampedWith records a digest of the pod's spec and of a file that the
// pod reads at start, so a change to the file replaces the pod too. The
// digest excludes the metadata, so a change to an annotation such as
// annotationDrivers does not replace the pod.
func stampedWith(p *pod, file string) *pod {
	body, _ := json.Marshal(p.Spec)
	sum := sha256.Sum256(append(body, file...))
	if p.Metadata.Annotations == nil {
		p.Metadata.Annotations = map[string]string{}
	}
	p.Metadata.Annotations[annotationSpec] = hex.EncodeToString(sum[:8])
	return p
}

// current reports whether a running pod has the spec digest and the
// server label of the pod that the operator builds now. A device pod's
// spec does not name its server, so only the label shows that the pod
// was created for another server, as can happen when a person removes a
// reservation's finalizer. Such a pod is replaced, because the other
// server's cleanup of its stray device pods would delete it later.
func current(running, built *pod) bool {
	return running.Metadata.Annotations[annotationSpec] == built.Metadata.Annotations[annotationSpec] &&
		running.Metadata.Labels[labelServer] == built.Metadata.Labels[labelServer]
}
