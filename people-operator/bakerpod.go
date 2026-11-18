package main

// A baker pod reads a picture from an NFS export or a claim, which only
// the kubelet can mount. The pod runs this program's own image as
// `people-operator bake <path> <colour>` (bake.go), with the source
// mounted read-only, and writes its result as one line of JSON to its
// log. It mounts no service account token, so the code that decodes
// the picture has no credentials for the API server.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// The label selects the baker pods that the operator watches. The
// Person, the avatar source, and the check request of a bake are
// annotations, because a label value is limited to 63 characters and a
// Person's name or source can be longer.
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

// bakerMount describes how a baker pod reaches its picture: the
// namespace the pod runs in, the volume it mounts, and the path of the
// file inside the container.
type bakerMount struct {
	namespace string
	volume    volume
	file      string
}

// mounts maps each URI scheme that a baker pod reads to the function
// that builds its mount. An nfs:// pod runs in operatorNamespace. A
// claim:// pod runs in the claim's namespace, because a pod can mount
// only a claim in its own namespace.
var mounts = map[string]func(ref *url.URL, operatorNamespace string) (bakerMount, error){
	schemeNFS:   nfsMount,
	schemeClaim: claimMount,
}

// nfsMount mounts the directory that contains the file, so the pod can
// read that directory and nothing above it.
func nfsMount(ref *url.URL, namespace string) (bakerMount, error) {
	file := path.Clean("/" + ref.Path)
	if ref.Host == "" || file == "/" {
		return bakerMount{}, fmt.Errorf("the URI %s has no server or file, so write it as nfs://<server>/<path>", ref.Redacted())
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

// claimMount parses claim://<namespace>/<claim>/<path>. A Person is
// cluster-scoped, so the URI must include the claim's namespace. The
// pod mounts the whole claim, because the claim already limits what the
// pod can read.
func claimMount(ref *url.URL, _ string) (bakerMount, error) {
	claim, file, _ := strings.Cut(strings.TrimPrefix(ref.Path, "/"), "/")
	file = path.Clean("/" + file)
	if ref.Host == "" || claim == "" || file == "/" {
		return bakerMount{}, fmt.Errorf("the URI %s has no namespace, claim, or file, so write it as claim://<namespace>/<claim>/<path>", ref.Redacted())
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

// bakerPodName returns the name of a Person's baker pod. The name
// depends only on the Person's name. So when a pass runs before the
// informer shows the pod that an earlier pass created, the create gets
// a 409 and no second pod starts. Kubernetes limits a pod name to 253
// characters, so a longer name is cut and ends with a hash of the
// Person's name, which keeps two long names distinct.
func bakerPodName(person string) string {
	name := "avatar-" + person
	if len(name) <= bakerPodMaxNameSize {
		return name
	}
	sum := sha256.Sum256([]byte(person))
	suffix := "-" + hex.EncodeToString(sum[:8])
	return strings.TrimRight(name[:bakerPodMaxNameSize-len(suffix)], "-.") + suffix
}

// buildBakerPod returns the pod that bakes one Person's picture.
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
			// The operator reads the log of a failed pod as well as a
			// successful one, so the pod is never restarted.
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
					// read a picture on a claim whatever its owner and
					// mode, and the read-only mount still stops writes.
					// An NFS server maps root to its anonymous user
					// unless the export sets no_root_squash, so a
					// picture on NFS must be readable by that user. The
					// Pod Security baseline level allows DAC_OVERRIDE.
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

// pictureMemoryMiB is the memory limit of a baker pod. An image of
// maxPictureSide on each side decodes to 256 MiB at four bytes a pixel.
// The rest is for the thumbnail and the Go runtime.
const pictureMemoryMiB = 384
