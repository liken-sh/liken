package main

// handle.go validates the volume handles the driver accepts. A handle
// is one path element under the store, so a handle the filesystem
// would read as a path, or as a step out of the store, is refused.

import (
	"regexp"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// handleLimit is the longest DNS-1123 subdomain name, in characters.
const handleLimit = 253

// handleShape is the shape of a DNS-1123 subdomain name: segments of
// lower-case letters, digits, and -, each starting and ending with a
// letter or a digit, joined by dots. It holds no / and no upper-case
// letter, and no segment is empty.
var handleShape = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// checkHandle returns nil when the handle is a name the driver can put
// under the store, and an InvalidArgument status that says why when it
// is not.
func checkHandle(handle string) error {
	switch {
	case handle == "":
		return status.Error(codes.InvalidArgument, "volume_id: the call names no volume")
	case len(handle) > handleLimit:
		return status.Errorf(codes.InvalidArgument,
			"volume_id: %q is %d characters, and a handle takes at most %d",
			handle, len(handle), handleLimit)
	case !handleShape.MatchString(handle):
		return status.Errorf(codes.InvalidArgument,
			"volume_id: %q is not a DNS-1123 subdomain name", handle)
	}
	return nil
}
