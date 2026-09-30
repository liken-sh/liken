package main

// The operator fetches an https:// or an http:// picture itself. The
// URL comes from a cluster-scoped Person, which only the cluster's
// owner can write, and the fetch is still bounded: a timeout, a limit
// on the body, and the limits on the image in thumbnail.go.

import (
	"context"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"net/url"
	"time"
)

// fetchTimeout bounds one fetch, from the dial to the last byte of the
// body. A pass reads each Person in turn, so a server that never
// answers holds the pass for this long and no longer.
const fetchTimeout = 15 * time.Second

// httpSource fetches a picture over HTTP. Its client carries the
// transport, which a test replaces with an in-memory server.
type httpSource struct {
	client *http.Client
}

func newHTTPSource(transport http.RoundTripper) httpSource {
	return httpSource{client: &http.Client{Transport: transport, Timeout: fetchTimeout}}
}

func (httpSource) successReason() string { return reasonFetched }

// read sends a conditional GET when the status records a version of
// the picture, so a picture that has not changed costs one small
// request and a 304.
func (s httpSource) read(ctx context.Context, ref *url.URL, known pictureVersion, backdrop color.Color) (reading, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.String(), nil)
	if err != nil {
		return reading{}, failure(reasonFetchFailed, err)
	}
	if known.ETag != "" {
		request.Header.Set("If-None-Match", known.ETag)
	}
	if known.LastModified != "" {
		request.Header.Set("If-Modified-Since", known.LastModified)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return reading{}, failure(reasonFetchFailed, err)
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		return reading{version: known, unchanged: true}, nil
	default:
		return reading{}, failure(reasonFetchFailed, fmt.Errorf("%s answered %s", ref.Redacted(), response.Status))
	}

	// One byte past the limit tells a file at the limit from a larger
	// one, and bakeThumbnail refuses the larger one.
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPictureBytes+1))
	if err != nil {
		return reading{}, failure(reasonFetchFailed, err)
	}
	thumbnail, err := bakeThumbnail(body, backdrop)
	if err != nil {
		return reading{}, failure(reasonFetchFailed, err)
	}
	return reading{
		thumbnail: thumbnail,
		version: pictureVersion{
			ETag:         response.Header.Get("ETag"),
			LastModified: response.Header.Get("Last-Modified"),
		},
	}, nil
}
