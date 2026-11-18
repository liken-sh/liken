package main

// The operator fetches an https:// or http:// picture in its own
// process. So the fetch has a timeout, a limit on the body size, and the
// image limits in thumbnail.go, because a slow or hostile server must
// not stall or exhaust the operator.

import (
	"context"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"net/url"
	"time"
)

// fetchTimeout limits one fetch, from the dial to the last byte of the
// body. A pass reads the Persons one at a time, so each server that
// never responds delays the pass by up to this long.
const fetchTimeout = 15 * time.Second

// httpSource fetches a picture over HTTP. Tests replace the transport
// of its client with an in-memory server.
type httpSource struct {
	client *http.Client
}

func newHTTPSource(transport http.RoundTripper) httpSource {
	return httpSource{client: &http.Client{Transport: transport, Timeout: fetchTimeout}}
}

func (httpSource) successReason() string { return reasonFetched }

// read sends a conditional GET when the status records a version of
// the picture, so an unchanged picture costs one 304 response.
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
		return reading{}, failure(reasonFetchFailed, fmt.Errorf("%s responded with %s", ref.Redacted(), response.Status))
	}

	// Read one byte more than the limit, so that makeThumbnail can reject
	// a body that is larger than the limit.
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPictureBytes+1))
	if err != nil {
		return reading{}, failure(reasonFetchFailed, err)
	}
	thumbnail, err := makeThumbnail(body, backdrop)
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
