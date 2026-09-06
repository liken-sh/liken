package main

// Policy.go reads a VolumeAttributesClass's parameters into the
// rules an armed volume commits and pushes under.

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/api/resource"
)

// The parameter names.
const (
	quiesceParameter     = "push.quiesce"
	maxLatencyParameter  = "push.maxLatency"
	maxFileSizeParameter = "commit.maxFileSize"
	authorParameter      = "commit.author"
	ignoreParameter      = "ignore"
	metadataParameter    = "metadata"
)

// Every parameter has a default, so an empty class arms a volume with
// the defaults.
const (
	shortestQuiesce    = 5 * time.Second
	defaultMaxLatency  = 5 * time.Minute
	defaultMaxFileSize = 1 << 20
	defaultAuthorName  = "git-csi-driver"
	defaultAuthorEmail = "git-csi-driver@liken.sh"
)

// neverLatency is the one value of push.maxLatency that is not a
// duration. It resolves to no maximum.
const neverLatency = "never"

// policy is a class as the driver holds it. A nil policy is an unarmed
// volume.
type policy struct {
	quiesce     time.Duration
	maxLatency  time.Duration
	maxFileSize int64
	authorName  string
	authorEmail string
	ignore      []string
	metadata    bool
}

// parsePolicy resolves a class's parameters with the defaults. It
// refuses an unknown parameter and a malformed value with
// InvalidArgument and the parameter's name. The parameters are read in
// sorted order, so the first bad one is the same on every pass.
func parsePolicy(parameters map[string]string) (*policy, error) {
	resolved := &policy{
		quiesce:     defaultQuiesce,
		maxLatency:  defaultMaxLatency,
		maxFileSize: defaultMaxFileSize,
		authorName:  defaultAuthorName,
		authorEmail: defaultAuthorEmail,
		metadata:    true,
	}

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
			resolved.ignore = parseIgnore(value)
		case metadataParameter:
			resolved.metadata, err = parseMetadataFlag(value)
		default:
			err = status.Errorf(codes.InvalidArgument, "%s: unknown parameter", name)
		}
		if err != nil {
			return nil, err
		}
	}

	// A class that sets push.quiesce alone means a quiet tree pushes
	// after that long, so the default push.maxLatency stretches to it. A
	// class that sets both has to agree with itself, and that check
	// comes last, because it needs both values read.
	if !latencySet && resolved.maxLatency < resolved.quiesce {
		resolved.maxLatency = resolved.quiesce
	}
	if resolved.maxLatency != 0 && resolved.maxLatency < resolved.quiesce {
		return nil, status.Errorf(codes.InvalidArgument, "%s: %s is shorter than %s %s",
			maxLatencyParameter, resolved.maxLatency, quiesceParameter, resolved.quiesce)
	}
	return resolved, nil
}

// parseQuiesce reads push.quiesce. It has a floor, because a shorter
// rest commits a write the application has not finished.
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

// parseMaxLatency reads push.maxLatency. never reads as zero: no push
// the quiesce did not ask for.
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

// parseMaxFileSize reads commit.maxFileSize as the quantity Kubernetes
// itself writes, so 1Mi means what it means everywhere else. Zero is
// no limit.
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

// authorForm is the one shape git accepts for an identity.
var authorForm = regexp.MustCompile(`^(\S.*?)\s+<([^<>\s]+)>$`)

// parseAuthor splits commit.author into the name and the address
// every commit carries.
func parseAuthor(value string) (string, string, error) {
	found := authorForm.FindStringSubmatch(value)
	if found == nil {
		return "", "", status.Errorf(codes.InvalidArgument,
			"%s: %q is not Name <email>", authorParameter, value)
	}
	return found[1], found[2], nil
}

// parseIgnore splits the comma-separated list. An empty piece is not a
// pattern, because an empty pattern excludes nothing.
func parseIgnore(value string) []string {
	patterns := []string{}
	for _, pattern := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(pattern); trimmed != "" {
			patterns = append(patterns, trimmed)
		}
	}
	return patterns
}

// parseMetadataFlag reads metadata. The two words are its whole domain,
// so 1 and yes are refused where a person meant true.
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

// author is the environment git reads the author and the committer
// from, so no configuration file has to carry them.
func (p *policy) author() []string {
	return []string{
		"GIT_AUTHOR_NAME=" + p.authorName,
		"GIT_AUTHOR_EMAIL=" + p.authorEmail,
		"GIT_COMMITTER_NAME=" + p.authorName,
		"GIT_COMMITTER_EMAIL=" + p.authorEmail,
	}
}
