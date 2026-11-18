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

// The keys that the kubelet adds to the volume context when the
// CSIDriver object sets podInfoOnMount.
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

type pullMode int

const (
	pullNever pullMode = iota
	pullOnDemand
	pullTimed
)

// pullPolicy is the parsed pull attribute. every is the timer interval,
// and it is set only when mode is pullTimed. A volume with no pull
// attribute gets pullEvery(defaultPull), not the zero value.
type pullPolicy struct {
	mode  pullMode
	every time.Duration
}

func pullEvery(interval time.Duration) pullPolicy {
	return pullPolicy{mode: pullTimed, every: interval}
}

// follows reports whether the volume takes part in its repository's
// pull loop, which a timer or a demand drives. A volume with pull set
// to never does not.
func (p pullPolicy) follows() bool {
	return p.mode != pullNever
}

// timer returns the interval of the loop's timer, and false when the
// volume has no timer and only a demand pulls it.
func (p pullPolicy) timer() (time.Duration, bool) {
	return p.every, p.mode == pullTimed
}

// podReference is the pod that mounts the volume, as the kubelet
// reports it. The driver posts Events about the volume on this pod.
type podReference struct {
	name      string
	namespace string
	uid       string
}

// attributes is the parsed volume context of one volume.
type attributes struct {
	url       string
	ref       string
	pull      pullPolicy
	depth     int
	offline   offlinePolicy
	ephemeral bool
	pod       podReference
	// webhookSecret is the name of a Secret in the claim's namespace. The
	// controller verifies a forge's push against it before it demands a
	// pull on this volume.
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
			"readOnly: an inline volume must be read-only")
	}
	// The webhook and a demand both work by annotating the volume's
	// PersistentVolume. An inline volume has no PersistentVolume, so
	// neither can reach it.
	if parsed.ephemeral && parsed.webhookSecret != "" {
		return nil, status.Error(codes.InvalidArgument,
			"webhookSecret: an inline volume has no PersistentVolume for a webhook to mark")
	}
	if parsed.ephemeral && parsed.pull.mode == pullOnDemand {
		return nil, status.Error(codes.InvalidArgument,
			"pull: on-demand pulls when a demand annotates the PersistentVolume, "+
				"and an inline volume has none")
	}
	return parsed, nil
}

// readOnlyAttributes are the attributes that only a read-only volume
// accepts. A writeable volume checks out its ref once, at stage, and the
// driver never pulls it after that.
var readOnlyAttributes = []string{"pull", "depth", "offline", "webhookSecret"}

// parseStageAttributes reads the attributes of a persistent volume. A
// writeable volume refuses the read-only attributes. A read-only claim
// accepts the same attributes as an inline volume.
func parseStageAttributes(kind volumeKind, context map[string]string) (*attributes, error) {
	if kind != writeableVolume {
		return parseVolumeContext(context)
	}
	for _, key := range readOnlyAttributes {
		if _, found := context[key]; found {
			return nil, status.Errorf(codes.InvalidArgument,
				"%s: only a read-only volume accepts this attribute", key)
		}
	}
	return parseVolumeContext(context)
}

// parseVolumeContext reads the attributes that both the stage call and
// the publish call carry.
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
			// The kubelet sends it. The driver accepts it and does not use it.
		default:
			return nil, status.Errorf(codes.InvalidArgument, "%s: unknown attribute, want one of url, ref, pull, depth, offline, webhookSecret", key)
		}
	}

	if parsed.url == "" {
		return nil, status.Error(codes.InvalidArgument, "url: the volume sets no url")
	}
	if err := checkURL(parsed.url); err != nil {
		return nil, err
	}
	if parsed.ref == "" {
		return nil, status.Error(codes.InvalidArgument, "ref: the value is empty")
	}
	return parsed, nil
}

// checkURL refuses two kinds of URL that make git run something other
// than a fetch. git reads a URL that starts with a dash as an option. A
// <transport>::<address> URL makes git run a helper program for that
// transport. The driver fetches as root on the node and the URL comes
// from a pod spec, so both are refused before git runs.
func checkURL(url string) error {
	if strings.HasPrefix(url, "-") {
		return status.Errorf(codes.InvalidArgument, "url: %q starts with a dash, so git reads it as an option", url)
	}
	if transportHelper.MatchString(url) {
		return status.Errorf(codes.InvalidArgument,
			"url: %q has the form <transport>::<address>, which makes git run a transport helper, and the driver runs none", url)
	}
	return nil
}

// transportHelper matches a word followed by two colons, which is git's
// <transport>::<address> form. No https, ssh, git, or file URL has
// that.
var transportHelper = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+.\-]*::`)

// parsePull reads the pull attribute. never means no pull after
// publish. on-demand means a pull only when a demand arrives. A
// duration means a pull on that timer and on a demand.
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
			"pull: %q must be longer than zero", value)
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
