package main

// A Person's spec.avatar names its picture by URI, and the scheme
// decides how the operator reads it. The operator reads https://,
// http://, and data: in its own process. A baker pod reads nfs:// and
// claim://, because only the kubelet mounts an NFS export or a claim
// (bakerpod.go). A new scheme is one entry in one of the two tables.

import (
	"context"
	"errors"
	"image/color"
	"net/url"
)

// pictureVersion identifies one copy of a picture at its source, so a
// later read can tell whether the picture changed. An HTTP source
// answers an ETag and a Last-Modified header. A file answers its
// modification time and its size.
type pictureVersion struct {
	ETag         string
	LastModified string
	Size         int64
}

// reading is what a source answers: a new thumbnail, or unchanged when
// the picture is the version the status already records.
type reading struct {
	thumbnail string
	version   pictureVersion
	unchanged bool
}

// source reads one scheme's picture in the operator's own process.
type source interface {
	// read answers the thumbnail of the picture that ref names. known
	// is the version that status.avatar records for ref, and is zero
	// when the status records none. backdrop fills the transparent
	// parts of the picture.
	read(ctx context.Context, ref *url.URL, known pictureVersion, backdrop color.Color) (reading, error)

	// successReason is the reason AvatarReady reports after a read
	// that worked.
	successReason() string
}

// The reasons AvatarReady reports.
const (
	reasonFetched           = "Fetched"
	reasonInline            = "Inline"
	reasonBaked             = "Baked"
	reasonInitials          = "Initials"
	reasonFetchFailed       = "FetchFailed"
	reasonBakeFailed        = "BakeFailed"
	reasonDecodeFailed      = "DecodeFailed"
	reasonUnsupportedScheme = "UnsupportedScheme"
)

// sourceError is a read that failed, with the reason AvatarReady
// reports for it.
type sourceError struct {
	reason string
	err    error
}

func (e *sourceError) Error() string { return e.err.Error() }

// failure wraps an error in the reason it reports. An error that
// errDecode marks reports DecodeFailed, whatever the source, so the
// condition separates a picture that did not decode from a source that
// did not answer.
func failure(reason string, err error) error {
	if errors.Is(err, errDecode) {
		reason = reasonDecodeFailed
	}
	return &sourceError{reason: reason, err: err}
}

// reasonOf answers the reason of a failed read.
func reasonOf(err error) string {
	if failed, ok := errors.AsType[*sourceError](err); ok {
		return failed.reason
	}
	return reasonFetchFailed
}

// Only the picture of a data: source is in the Person itself. Every
// other source can change with no edit to the Person, so the slow
// check reads it again (watch.go).
func rechecked(ref *url.URL) bool {
	return ref.Scheme != schemeData
}

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
	schemeData  = "data"
	schemeNFS   = "nfs"
	schemeClaim = "claim"
)

// supportedSchemes is the list an UnsupportedScheme condition names.
const supportedSchemes = "https://, http://, data:, nfs://, and claim://"
