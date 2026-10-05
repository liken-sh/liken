package main

// The pods and Services of the topology of plan 03. Each device runs in
// its own pod as `socat TCP-LISTEN:7625,reuseaddr EXEC:<driver>,pipes`,
// behind a Service of its own. Each INDI server runs indiserver with
// one link to indi-shim for each device it serves, and the shim dials
// the device's Service. The manifests in topology/ are the same shape
// written by hand, and pods_test.go holds the two together.
//
// The pods are bare pods, as media-operator's are. The kubelet restarts
// a container that exits, which is what a driver needs: socat exits
// when the shim's connection ends, and the next start serves the next
// connection. A pod that is deleted or evicted is created again by the
// operator, from the next event of the pod watch.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/liken-sh/liken/observatory-operator/drivers"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The paths inside a server's pod: the shim's links, and /tmp, where
// INDI writes its configuration because HOME is /tmp in the images.
const (
	linksDir = "/run/indi/drivers"
	tmpDir   = "/tmp"
)

// maxRestarts is the restart count of indiserver, the largest that its
// atoi parse holds. indiserver restarts a driver that exits at most -r
// times, and the count never resets. Each exit of a shim spends one,
// and a shim whose device pod is down exits once a minute, so this
// count lasts about 4,000 years. Plan 03 measured it.
const maxRestarts = "2147483647"

// claimName is the pod-local name of a device's ResourceClaim.
const claimName = "device"

// restricted is the security context of every container: user 1000,
// as plan 06's contract for a driver's image states, on a read-only
// root filesystem with no capabilities.
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

// devicePod answers the pod, the Service, and, for a device with
// spec.claim, the ResourceClaim of one device on one server.
func devicePod(namespace string, server serverRef, d *device) (*pod, *service, *resourceClaim, error) {
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
		// pipes gives the driver a pipe for stdout. On a Unix socket, a
		// driver sends each BLOB as a file descriptor, which socat
		// cannot carry, and the driver crashes on its first frame.
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
			// The namespace holds a Service for every device, and the
			// driver reads none of the variables that Kubernetes would
			// set for each one.
			EnableServiceLinks: boolPointer(false),
			// A driver speaks to the INDI server and to its hardware,
			// never to the API server.
			AutomountServiceAccountToken: boolPointer(false),
			Containers:                   []container{driver},
			Volumes:                      []volume{{Name: "tmp", EmptyDir: &emptyDir{}}},
		},
	}
	var claim *resourceClaim
	if len(d.object.Spec.Claim) > 0 {
		var spec any
		if err := json.Unmarshal(d.object.Spec.Claim, &spec); err != nil {
			return nil, nil, nil, fmt.Errorf("spec.claim of the %s %s: %w", d.kind.Name, d.name(), err)
		}
		claim = &resourceClaim{
			APIVersion: "resource.k8s.io/v1", Kind: "ResourceClaim",
			Metadata: meta{Name: name, Namespace: namespace, Labels: podLabels, OwnerReferences: owners},
			Spec:     spec,
		}
		p.Spec.ResourceClaims = []podClaim{{Name: claimName, ResourceClaimName: name}}
		p.Spec.Containers[0].Resources = &resources{Claims: []resourceName{{Name: claimName}}}
	}
	return stamped(p), serviceFor(namespace, name, podLabels, owners, "driver", devicePort), claim, nil
}

// serverPod answers the pod and the Service of one INDI server, with a
// link to the shim for each device it serves.
func serverPod(namespace string, server serverRef, ownerUID string, devices []*device) (*pod, *service, error) {
	name := server.String()
	if len(name) > 63 || !serviceName.MatchString(name) {
		return nil, nil, fmt.Errorf("the %s %s needs the Service name %q, which is not a DNS label of 63 characters or fewer", server.kind.Name, server.name, name)
	}
	var targets, links []string
	for _, d := range devices {
		object, err := objectName(d.kind, d.name())
		if err != nil {
			return nil, nil, err
		}
		target := object + ":" + strconv.Itoa(devicePort)
		targets = append(targets, target)
		links = append(links, linksDir+"/"+target)
	}
	owners := []ownerReference{owner(observatory.APIVersion, server.kind, server.name, ownerUID)}
	podLabels := labels(name, roleServer, name, server.kind, server.name)
	image := drivers.ServerImage()
	drivesMount := volumeMount{Name: "drivers", MountPath: linksDir}
	p := &pod{
		APIVersion: "v1", Kind: "Pod",
		Metadata: meta{Name: name, Namespace: namespace, Labels: podLabels, OwnerReferences: owners},
		Spec: podSpec{
			RestartPolicy: "Always",
			// indiserver runs as process 1 and installs no handler for
			// SIGTERM, and the kernel ignores a signal that process 1
			// does not handle. So a stop would wait out the whole grace
			// period before the kubelet kills it, 31 s on a test
			// cluster, with every device offline. The server holds no
			// state that a graceful stop would save.
			TerminationGracePeriodSeconds: int64Pointer(1),
			EnableServiceLinks:            boolPointer(false),
			AutomountServiceAccountToken:  boolPointer(false),
			// The image has no shell to make the links, so the shim
			// makes them in an init container.
			InitContainers: []container{{
				Name:            "links",
				Image:           image,
				Command:         append([]string{"/usr/bin/indi-shim", "link", linksDir}, targets...),
				SecurityContext: restricted(),
				VolumeMounts:    []volumeMount{drivesMount},
			}},
			Containers: []container{{
				Name:            "indiserver",
				Image:           image,
				Args:            append([]string{"-v", "-r", maxRestarts}, links...),
				Ports:           []containerPort{{Name: "indi", ContainerPort: serverPort}},
				SecurityContext: restricted(),
				VolumeMounts:    []volumeMount{drivesMount, {Name: "tmp", MountPath: tmpDir}},
				// The operator opens its INDI connection when the pod is
				// Ready, and a client that connects to indiserver and
				// closes costs indiserver nothing. A device pod has no
				// probe: socat serves one connection, so a probe's
				// connection would start the driver and end it.
				ReadinessProbe: &probe{TCPSocket: &tcpSocket{Port: "indi"}, PeriodSeconds: 10},
			}},
			Volumes: []volume{{Name: "drivers", EmptyDir: &emptyDir{}}, {Name: "tmp", EmptyDir: &emptyDir{}}},
		},
	}
	return stamped(p), serviceFor(namespace, name, podLabels, owners, "indi", serverPort), nil
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
// operator compares the digest of the pod it builds with the digest on
// the pod that runs, and replaces a pod whose spec changed, because
// Kubernetes refuses most changes to a running pod's spec.
func stamped(p *pod) *pod {
	body, _ := json.Marshal(p.Spec)
	sum := sha256.Sum256(body)
	p.Metadata.Annotations = map[string]string{annotationSpec: hex.EncodeToString(sum[:8])}
	return p
}

// current reports whether a running pod has the spec of the pod the
// operator builds now.
func current(running, built *pod) bool {
	return running.Metadata.Annotations[annotationSpec] == built.Metadata.Annotations[annotationSpec]
}
