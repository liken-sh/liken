package main

// policy.go parses the parameters of a VolumeAttributesClass into the
// rules that an armed volume commits and pushes under.

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	quiesceParameter     = "push.quiesce"
	maxLatencyParameter  = "push.maxLatency"
	maxFileSizeParameter = "commit.maxFileSize"
	authorParameter      = "commit.author"
	ignoreParameter      = "ignore"
	metadataParameter    = "metadata"
)

// The defaults of the parameters that a class leaves out. The default
// of push.quiesce is defaultQuiesce in watch.go, beside the watch that
// uses it. shortestQuiesce is the lowest push.quiesce that a class may
// set.
const (
	shortestQuiesce    = 5 * time.Second
	defaultMaxLatency  = 5 * time.Minute
	defaultMaxFileSize = 1 << 20
	defaultAuthorName  = "git-csi-driver"
	defaultAuthorEmail = "git-csi-driver@liken.sh"
)

// parameterNames lists every parameter that a class may set, for the
// message that refuses an unknown one.
var parameterNames = []string{
	quiesceParameter, maxLatencyParameter, maxFileSizeParameter,
	authorParameter, ignoreParameter, metadataParameter,
}

// neverLatency is the value of push.maxLatency that turns off the
// maximum.
const neverLatency = "never"

// policy is a parsed class. A volume that no class arms has a nil
// policy.
type policy struct {
	quiesce     time.Duration
	maxLatency  time.Duration
	maxFileSize int64
	authorName  string
	authorEmail string
	ignore      []string
	metadata    bool
}

// parsePolicy applies a class's parameters over the defaults. It
// returns InvalidArgument with the parameter's name for an unknown
// parameter or a malformed value. It reads the parameters in sorted
// order, so when several are bad the error names the same one every
// time.
func parsePolicy(parameters map[string]string) (*policy, error) {
	resolved := defaultPolicy()

	_, latencySet := parameters[maxLatencyParameter]
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		value := parameters[name]
		var err error
		switch name {
		case quiesceParameter:
			resolved.quiesce, err = parseQuiesce(value)
		case maxLatencyParameter:
			resolved.maxLatency, err = parseMaxLatency(value)
		case maxFileSizeParameter:
			resolved.maxFileSize, err = parseMaxFileSize(value)
		case authorParameter:
			resolved.authorName, resolved.authorEmail, err = parseAuthor(value)
		case ignoreParameter:
			resolved.ignore = splitList(value)
		case metadataParameter:
			resolved.metadata, err = parseMetadataFlag(value)
		default:
			err = status.Errorf(codes.InvalidArgument, "%s: unknown parameter, want one of %s", name, strings.Join(parameterNames, ", "))
		}
		if err != nil {
			return nil, err
		}
	}

	// A maximum shorter than the quiesce is refused. When a class sets
	// only push.quiesce, the default maximum rises to the quiesce, so a
	// long quiesce alone is not refused.
	if !latencySet && resolved.maxLatency < resolved.quiesce {
		resolved.maxLatency = resolved.quiesce
	}
	if resolved.maxLatency != 0 && resolved.maxLatency < resolved.quiesce {
		return nil, status.Errorf(codes.InvalidArgument, "%s: %s is shorter than %s %s",
			maxLatencyParameter, resolved.maxLatency, quiesceParameter, resolved.quiesce)
	}
	return resolved, nil
}

// defaultPolicy returns a policy with every parameter at its default.
// A volume that no class arms writes no commit of its own. When a stage
// or a push rebases the commits that it already has, the rebased
// commits take the default author from this policy.
func defaultPolicy() *policy {
	return &policy{
		quiesce:     defaultQuiesce,
		maxLatency:  defaultMaxLatency,
		maxFileSize: defaultMaxFileSize,
		authorName:  defaultAuthorName,
		authorEmail: defaultAuthorEmail,
		metadata:    true,
	}
}

// parseQuiesce reads push.quiesce. It refuses a value under
// shortestQuiesce, because a shorter wait can commit a file that the
// application is still writing.
func parseQuiesce(value string) (time.Duration, error) {
	quiesce, err := time.ParseDuration(value)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument,
			"%s: %q is not a duration", quiesceParameter, value)
	}
	if quiesce < shortestQuiesce {
		return 0, status.Errorf(codes.InvalidArgument,
			"%s: %s is shorter than %s", quiesceParameter, quiesce, shortestQuiesce)
	}
	return quiesce, nil
}

// parseMaxLatency returns zero for never, which means no maximum, so
// only the quiesce starts a push.
func parseMaxLatency(value string) (time.Duration, error) {
	if value == neverLatency {
		return 0, nil
	}
	latency, err := time.ParseDuration(value)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument,
			"%s: %q is not a duration or %s", maxLatencyParameter, value, neverLatency)
	}
	return latency, nil
}

// parseMaxFileSize reads commit.maxFileSize as a Kubernetes quantity,
// so 1Mi means the same as it does in a resource request. Zero means no
// limit.
func parseMaxFileSize(value string) (int64, error) {
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument,
			"%s: %q is not a quantity", maxFileSizeParameter, value)
	}
	size := quantity.Value()
	if size < 0 {
		return 0, status.Errorf(codes.InvalidArgument,
			"%s: %q is smaller than zero", maxFileSizeParameter, value)
	}
	return size, nil
}

// authorForm matches the Name <email> form of a git identity.
var authorForm = regexp.MustCompile(`^(\S.*?)\s+<([^<>\s]+)>$`)

// parseAuthor splits commit.author into the name and the email address
// of the commit's author and committer.
func parseAuthor(value string) (string, string, error) {
	found := authorForm.FindStringSubmatch(value)
	if found == nil {
		return "", "", status.Errorf(codes.InvalidArgument,
			"%s: %q is not Name <email>", authorParameter, value)
	}
	return found[1], found[2], nil
}

// parseMetadataFlag accepts only true and false, and refuses the other
// forms that strconv.ParseBool accepts, such as 1 and yes.
func parseMetadataFlag(value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, status.Errorf(codes.InvalidArgument,
		"%s: %q is not true or false", metadataParameter, value)
}

// author returns the environment variables that set git's author and
// committer, so a commit needs no git configuration file.
func (p *policy) author() []string {
	return []string{
		"GIT_AUTHOR_NAME=" + p.authorName,
		"GIT_AUTHOR_EMAIL=" + p.authorEmail,
		"GIT_COMMITTER_NAME=" + p.authorName,
		"GIT_COMMITTER_EMAIL=" + p.authorEmail,
	}
}
