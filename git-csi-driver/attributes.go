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
	pull      time.Duration
	depth     int
	offline   offlinePolicy
	ephemeral bool
	pod       podReference
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
	return parsed, nil
}

// readOnlyAttributes are the attributes a read-only volume alone
// accepts. A writeable volume follows its ref at stage and never after.
var readOnlyAttributes = []string{"pull", "depth", "offline"}

// parseStageAttributes reads a persistent volume's attributes and
// refuses the read-only ones.
func parseStageAttributes(context map[string]string) (*attributes, error) {
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
	parsed := &attributes{ref: defaultRef, pull: defaultPull, offline: offlineRefuse}

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

// parsePull reads pull. never is zero: no fetch after the publish.
func parsePull(value string) (time.Duration, error) {
	if value == "never" {
		return 0, nil
	}
	pull, err := time.ParseDuration(value)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument,
			"pull: %q is not a duration or never", value)
	}
	if pull <= 0 {
		return 0, status.Errorf(codes.InvalidArgument,
			"pull: %q is not longer than zero", value)
	}
	return pull, nil
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
