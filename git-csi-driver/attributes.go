package main

// attributes.go reads a volume's attributes and refuses what the driver
// cannot serve, before any git runs.

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The keys the kubelet adds to the volume context itself. podInfoOnMount
// on the CSIDriver object turns them on.
const (
	ephemeralKey      = "csi.storage.k8s.io/ephemeral"
	podNameKey        = "csi.storage.k8s.io/pod.name"
	podNamespaceKey   = "csi.storage.k8s.io/pod.namespace"
	podUIDKey         = "csi.storage.k8s.io/pod.uid"
	serviceAccountKey = "csi.storage.k8s.io/serviceAccount.name"
)

// offlinePolicy is what a volume does when the fetch at publish fails.
type offlinePolicy string

const (
	offlineRefuse     offlinePolicy = "refuse"
	offlineAllowStale offlinePolicy = "allowStale"
)

// The defaults for a read-only volume.
const (
	defaultRef  = "main"
	defaultPull = 5 * time.Minute
)

// pullMode is which of the three things pull says.
type pullMode int

const (
	pullNever pullMode = iota
	pullOnDemand
	pullTimed
)

// pullPolicy is what pull says. pullNever is the zero value, so a
// volume with no policy follows nothing. every is the interval, and it
// counts under pullTimed alone.
type pullPolicy struct {
	mode  pullMode
	every time.Duration
}

// pullEvery is the policy of a volume that names a duration.
func pullEvery(interval time.Duration) pullPolicy {
	return pullPolicy{mode: pullTimed, every: interval}
}

// follows reports whether the volume joins its repository's loop,
// where a timer or a demand reaches it. A volume with pull never joins
// none.
func (p pullPolicy) follows() bool {
	return p.mode != pullNever
}

// timer is the interval the loop's timer takes, and false when nothing
// but a demand pulls the volume.
func (p pullPolicy) timer() (time.Duration, bool) {
	return p.every, p.mode == pullTimed
}

// podReference is the pod the kubelet named, where an Event goes.
type podReference struct {
	name      string
	namespace string
	uid       string
}

// attributes is one read-only volume as its attributes describe it.
type attributes struct {
	url       string
	ref       string
	pull      pullPolicy
	depth     int
	offline   offlinePolicy
	ephemeral bool
	pod       podReference
	// webhookSecret names the Secret in the claim's namespace that the
	// controller verifies a forge's push against before it marks this
	// volume.
	webhookSecret string
}

// parseAttributes reads the volume context. It refuses an unknown
// attribute and a malformed value with InvalidArgument and the
// attribute's name, so the pod's events say what to fix.
func parseAttributes(request *csi.NodePublishVolumeRequest) (*attributes, error) {
	parsed, err := parseVolumeContext(request.GetVolumeContext())
	if err != nil {
		return nil, err
	}
	if parsed.ephemeral && !request.GetReadonly() {
		return nil, status.Error(codes.InvalidArgument,
			"readOnly: an inline volume of this driver has to be read-only")
	}
	// A webhook marks a PersistentVolume, and an inline volume is
	// written into the pod spec, so no push ever reaches one.
	if parsed.ephemeral && parsed.webhookSecret != "" {
		return nil, status.Error(codes.InvalidArgument,
			"webhookSecret: a webhook marks a PersistentVolume, "+
				"and an inline volume is none")
	}
	// A demand is an annotation on a PersistentVolume, and an inline
	// volume has none, so nothing could ever demand it.
	if parsed.ephemeral && parsed.pull.mode == pullOnDemand {
		return nil, status.Error(codes.InvalidArgument,
			"pull: on-demand names a demand on a PersistentVolume, "+
				"and an inline volume has none")
	}
	return parsed, nil
}

// readOnlyAttributes are the attributes a read-only volume alone
// accepts. A writeable volume follows its ref at stage and never after.
var readOnlyAttributes = []string{"pull", "depth", "offline", "webhookSecret"}

// parseStageAttributes reads a persistent volume's attributes.
//
// A writeable stage refuses the read-only attributes. A read-only claim
// takes the same attributes an inline volume takes.
func parseStageAttributes(kind volumeKind, context map[string]string) (*attributes, error) {
	if kind != writeableVolume {
		return parseVolumeContext(context)
	}
	for _, key := range readOnlyAttributes {
		if _, found := context[key]; found {
			return nil, status.Errorf(codes.InvalidArgument,
				"%s: a writeable volume follows its ref at stage alone", key)
		}
	}
	return parseVolumeContext(context)
}

// parseVolumeContext reads the attributes both a stage call and a
// publish call carry.
func parseVolumeContext(context map[string]string) (*attributes, error) {
	parsed := &attributes{ref: defaultRef, pull: pullEvery(defaultPull), offline: offlineRefuse}

	for key, value := range context {
		switch key {
		case "url":
			parsed.url = value
		case "ref":
			parsed.ref = value
		case "pull":
			pull, err := parsePull(value)
			if err != nil {
				return nil, err
			}
			parsed.pull = pull
		case "depth":
			depth, err := parseDepth(value)
			if err != nil {
				return nil, err
			}
			parsed.depth = depth
		case "webhookSecret":
			parsed.webhookSecret = value
		case "offline":
			switch offlinePolicy(value) {
			case offlineRefuse, offlineAllowStale:
				parsed.offline = offlinePolicy(value)
			default:
				return nil, status.Errorf(codes.InvalidArgument,
					"offline: %q is not refuse or allowStale", value)
			}
		case ephemeralKey:
			parsed.ephemeral = value == "true"
		case podNameKey:
			parsed.pod.name = value
		case podNamespaceKey:
			parsed.pod.namespace = value
		case podUIDKey:
			parsed.pod.uid = value
		case serviceAccountKey:
		default:
			return nil, status.Errorf(codes.InvalidArgument, "%s: unknown attribute", key)
		}
	}

	if parsed.url == "" {
		return nil, status.Error(codes.InvalidArgument, "url: an attribute the volume must set")
	}
	if err := checkURL(parsed.url); err != nil {
		return nil, err
	}
	if parsed.ref == "" {
		return nil, status.Error(codes.InvalidArgument, "ref: an empty ref names nothing")
	}
	return parsed, nil
}

// checkURL refuses two URL shapes that would run a command. git accepts
// options after the repository, so a URL that starts with a dash is an
// option. A <transport>::<address> URL names a helper program git runs.
// The driver fetches as root on the node and the URL comes from a pod
// spec, so both are refused before git sees them.
func checkURL(url string) error {
	if strings.HasPrefix(url, "-") {
		return status.Errorf(codes.InvalidArgument, "url: %q reads as an option to git", url)
	}
	if transportHelper.MatchString(url) {
		return status.Errorf(codes.InvalidArgument,
			"url: %q names a transport helper, and the driver runs none", url)
	}
	return nil
}

// transportHelper matches git's <transport>::<address> form. No https,
// ssh, git, or file URL has two colons after its first word.
var transportHelper = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+.\-]*::`)

// parsePull reads pull. never pulls nothing after the publish, on a
// timer or on a demand. on-demand pulls on a demand alone. A duration
// pulls on the timer and on a demand.
func parsePull(value string) (pullPolicy, error) {
	switch value {
	case "never":
		return pullPolicy{mode: pullNever}, nil
	case "on-demand":
		return pullPolicy{mode: pullOnDemand}, nil
	}
	pull, err := time.ParseDuration(value)
	if err != nil {
		return pullPolicy{}, status.Errorf(codes.InvalidArgument,
			"pull: %q is not a duration, on-demand, or never", value)
	}
	if pull <= 0 {
		return pullPolicy{}, status.Errorf(codes.InvalidArgument,
			"pull: %q is not longer than zero", value)
	}
	return pullEvery(pull), nil
}

// parseDepth reads depth. Zero is a full clone.
func parseDepth(value string) (int, error) {
	depth, err := strconv.Atoi(value)
	if err != nil || depth < 0 {
		return 0, status.Errorf(codes.InvalidArgument,
			"depth: %q is not a whole number of commits", value)
	}
	return depth, nil
}
