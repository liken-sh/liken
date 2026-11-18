package main

// handle.go validates volume handles. The driver uses a handle as the
// name of a directory under the store. A handle must be a DNS-1123
// subdomain name, the rule for the name of a PersistentVolume, so that
// a handle can be the same as its PersistentVolume's name. The rule
// also refuses a slash, ".", and "..", which would put the directory
// outside the store.

import (
	"regexp"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// handleLimit is the longest DNS-1123 subdomain name, in characters.
const handleLimit = 253

// handleShape matches a DNS-1123 subdomain name: dot-separated segments
// of lower-case letters, digits, and dashes, where each segment starts
// and ends with a letter or digit.
var handleShape = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// checkHandle returns nil when the handle is safe to use as a directory
// name under the store, and an InvalidArgument status otherwise.
func checkHandle(handle string) error {
	switch {
	case handle == "":
		return status.Error(codes.InvalidArgument, "volume_id is empty")
	case len(handle) > handleLimit:
		return status.Errorf(codes.InvalidArgument,
			"volume_id: %q is %d characters, and the limit is %d",
			handle, len(handle), handleLimit)
	case !handleShape.MatchString(handle):
		return status.Errorf(codes.InvalidArgument,
			"volume_id: %q is not a DNS-1123 subdomain name", handle)
	}
	return nil
}
