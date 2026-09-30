package main

// A baker pod reads a picture from an NFS export or a claim, which only
// the kubelet can mount. The pod runs this program's own image as
// `people-operator bake <path> <colour>` (bake.go), with the source
// mounted read-only, and writes its result as one line of JSON to its
// log. It mounts no service account token, so the program that decodes
// the picture holds no credential for the API server.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// The labels and annotations of a baker pod. The label selects the
// pods the operator watches. The annotations record which Person the
// pod bakes for and what the bake answers, because a label value holds
// at most 63 characters and a Person's name and source can be longer.
const (
	bakerLabel          = "people.liken.sh/baker"
	bakerLabelValue     = "avatar"
	bakerPersonKey      = "people.liken.sh/person"
	bakerSourceKey      = "people.liken.sh/source"
	bakerCheckedKey     = "people.liken.sh/checked"
	bakerContainer      = "bake"
	bakerMountPath      = "/source"
	bakerVolume         = "source"
	bakerPodMaxNameSize = 253
)

// bakerMount is where a baker pod finds its picture: the namespace the
// pod runs in, the volume it mounts, and the file's path under the
// mount.
type bakerMount struct {
	namespace string
	volume    volume
	file      string
}

// mounts holds the schemes a baker pod reads, and how each one mounts
// its picture. operatorNamespace is where an nfs:// pod runs, because
// an NFS volume needs nothing from a namespace. A claim's pod runs in
// the claim's own namespace, because a pod mounts only a claim in its
// own namespace.
var mounts = map[string]func(ref *url.URL, operatorNamespace string) (bakerMount, error){
	schemeNFS:   nfsMount,
	schemeClaim: claimMount,
}

// nfsMount mounts the directory that holds the file, so the pod can
// read that directory and nothing above it.
func nfsMount(ref *url.URL, namespace string) (bakerMount, error) {
	file := path.Clean("/" + ref.Path)
	if ref.Host == "" || file == "/" {
		return bakerMount{}, fmt.Errorf("the URI %s names no server and file; write nfs://<server>/<path>", ref.Redacted())
	}
	return bakerMount{
		namespace: namespace,
		volume: volume{
			Name: bakerVolume,
			NFS:  &nfsVolume{Server: ref.Host, Path: path.Dir(file), ReadOnly: true},
		},
		file: path.Join(bakerMountPath, path.Base(file)),
	}, nil
}

// claimMount reads claim://<namespace>/<claim>/<path>. A Person is
// cluster-scoped, so the URI names the claim's namespace first. The
// claim mounts at its root, because the claim already bounds what the
// pod can read.
func claimMount(ref *url.URL, _ string) (bakerMount, error) {
	claim, file, _ := strings.Cut(strings.TrimPrefix(ref.Path, "/"), "/")
	file = path.Clean("/" + file)
	if ref.Host == "" || claim == "" || file == "/" {
		return bakerMount{}, fmt.Errorf("the URI %s names no namespace, claim, and file; write claim://<namespace>/<claim>/<path>", ref.Redacted())
	}
	return bakerMount{
		namespace: ref.Host,
		volume: volume{
			Name:                  bakerVolume,
			PersistentVolumeClaim: &claimVolume{ClaimName: claim, ReadOnly: true},
		},
		file: path.Join(bakerMountPath, file),
	}, nil
}

// bakerPodName answers the one name of a Person's baker pod. One name
// for each Person means a second create answers 409, so a pass that
// runs before the watch shows the first pod starts no second one. A
// name over the limit on a pod's name ends in a hash of the whole
// name, so two long names stay apart.
func bakerPodName(person string) string {
	name := "avatar-" + person
	if len(name) <= bakerPodMaxNameSize {
		return name
	}
	sum := sha256.Sum256([]byte(person))
	suffix := "-" + hex.EncodeToString(sum[:8])
	return strings.TrimRight(name[:bakerPodMaxNameSize-len(suffix)], "-.") + suffix
}

// buildBakerPod answers the pod that bakes one Person's picture.
func buildBakerPod(p *person, mount bakerMount, image string) pod {
	no, yes := false, true
	return pod{
		APIVersion: "v1",
		Kind:       "Pod",
		Metadata: podMeta{
			Name:      bakerPodName(p.Metadata.Name),
			Namespace: mount.namespace,
			Labels: map[string]string{
				bakerLabel:                     bakerLabelValue,
				"app.kubernetes.io/managed-by": "people-operator",
			},
			Annotations: map[string]string{
				bakerPersonKey:  p.Metadata.Name,
				bakerSourceKey:  p.Spec.Avatar,
				bakerCheckedKey: p.checkRequest(),
			},
		},
		Spec: podSpec{
			// The pod's end is the bake's end, and the operator reads
			// the result of a pod that failed as well.
			RestartPolicy:                "Never",
			AutomountServiceAccountToken: &no,
			EnableServiceLinks:           &no,
			Containers: []container{{
				Name:         bakerContainer,
				Image:        image,
				Args:         []string{"bake", mount.file, colourArgument(personColour(p.Metadata.Name))},
				VolumeMounts: []volumeMount{{Name: bakerVolume, MountPath: bakerMountPath, ReadOnly: true}},
				SecurityContext: &securityContext{
					AllowPrivilegeEscalation: &no,
					ReadOnlyRootFilesystem:   &yes,
					// The image runs as root. DAC_OVERRIDE lets root
					// read a picture whatever its owner and mode on a
					// claim, and the mount is read-only, so the pod
					// still writes nothing. An NFS server maps root to
					// its anonymous user unless the export says
					// otherwise, so a picture on NFS must be readable
					// by that user. Pod Security's baseline level
					// allows this capability.
					Capabilities: &capabilities{Drop: []string{"ALL"}, Add: []string{"DAC_OVERRIDE"}},
				},
				Resources: &resources{
					Requests: map[string]string{"cpu": "10m", "memory": "32Mi"},
					Limits:   map[string]string{"memory": strconv.Itoa(pictureMemoryMiB) + "Mi"},
				},
			}},
			Volumes: []volume{mount.volume},
		},
	}
}

// pictureMemoryMiB is the memory limit of a process that decodes one
// picture. The largest image the limits allow is 8192 by 8192 pixels,
// which decodes to 256 MiB at four bytes a pixel, and the thumbnail and
// the Go runtime need the rest.
const pictureMemoryMiB = 384
