package main

// The scheme of a Person's spec.avatar selects how the operator reads
// the picture. The operator reads https://, http://, and data: in its
// own process. nfs:// and claim:// need a mounted volume, which only
// the kubelet can create, so a baker pod reads them (bakerpod.go). A
// new scheme goes in the operator's sources map or in bakerMountFor
// (bakers.go).

import (
	"context"
	"errors"
	"image/color"
	"net/url"
)

// pictureVersion identifies one revision of a picture at its source, so
// a later read can tell whether it changed. An HTTP source uses the
// ETag and Last-Modified headers. A file uses its modification time
// and size.
type pictureVersion struct {
	ETag         string
	LastModified string
	Size         int64
}

// reading is the result of reading a source: a new thumbnail, or
// unchanged when the picture still has the version in status.avatar.
type reading struct {
	thumbnail string
	version   pictureVersion
	unchanged bool
}

// source reads the pictures of one URI scheme in the operator's own
// process.
type source interface {
	// read returns the thumbnail of the picture at ref. known is the
	// version in status.avatar for the same source, or the zero version
	// when there is none. backdrop fills transparent pixels.
	read(ctx context.Context, ref *url.URL, known pictureVersion, backdrop color.Color) (reading, error)

	// successReason is the AvatarReady reason after a successful read.
	successReason() string
}

// sourceError is a failed read together with the AvatarReady reason to
// report for it.
type sourceError struct {
	reason string
	err    error
}

func (e *sourceError) Error() string { return e.err.Error() }

// failure wraps err with a reason. An error that wraps errDecode gets
// DecodeFailed instead, so the condition tells a bad picture apart from
// an unreachable source.
func failure(reason string, err error) error {
	if errors.Is(err, errDecode) {
		reason = reasonDecodeFailed
	}
	return &sourceError{reason: reason, err: err}
}

// reasonOf returns the AvatarReady reason for err, and FetchFailed
// when err is not a sourceError.
func reasonOf(err error) string {
	if failed, ok := errors.AsType[*sourceError](err); ok {
		return failed.reason
	}
	return reasonFetchFailed
}

// rechecked reports whether the recheck every recheckInterval reads ref
// again. A data: URI is part of the Person, so it changes only with an
// edit to the Person.
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

// supportedSchemes is the list in the UnsupportedScheme message.
const supportedSchemes = "https://, http://, data:, nfs://, and claim://"
