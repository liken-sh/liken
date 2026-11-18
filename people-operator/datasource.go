package main

// A data: URI contains the picture, so the operator decodes it from the
// Person with no fetch. RFC 2397 defines the form:
// data:[<media type>][;base64],<data>. The operator ignores the media
// type because the image decoder detects the format from the bytes.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image/color"
	"net/url"
	"strings"
)

type dataSource struct{}

func (dataSource) successReason() string { return reasonInline }

// A data: URI has no version, so read never returns unchanged. The
// picture is part of spec.avatar, so a new picture changes the Person's
// generation, which starts a read.
func (dataSource) read(_ context.Context, ref *url.URL, _ pictureVersion, backdrop color.Color) (reading, error) {
	picture, err := dataBytes(ref.Opaque)
	if err != nil {
		return reading{}, failure(reasonDecodeFailed, err)
	}
	thumbnail, err := makeThumbnail(picture, backdrop)
	if err != nil {
		return reading{}, failure(reasonDecodeFailed, err)
	}
	return reading{thumbnail: thumbnail}, nil
}

// dataBytes decodes the opaque part of a data: URI, which is everything
// after "data:". Without the ";base64" suffix the data is
// percent-encoded.
func dataBytes(opaque string) ([]byte, error) {
	header, payload, found := strings.Cut(opaque, ",")
	if !found {
		return nil, errors.New("the data: URI has no comma before its data")
	}
	if strings.HasSuffix(header, ";base64") {
		picture, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, fmt.Errorf("decoding the base64 of the data: URI: %w", err)
		}
		return picture, nil
	}
	picture, err := url.PathUnescape(payload)
	if err != nil {
		return nil, fmt.Errorf("decoding the percent-encoding of the data: URI: %w", err)
	}
	return []byte(picture), nil
}
