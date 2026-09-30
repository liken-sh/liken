package main

// A data: URI holds the picture in the Person itself, so the operator
// decodes it with no fetch. RFC 2397 gives the form:
// data:[<media type>][;base64],<data>. The media type is not read,
// because the decoder tells the format from the bytes.

import (
	"context"
	"encoding/base64"
	"fmt"
	"image/color"
	"net/url"
	"strings"
)

type dataSource struct{}

func (dataSource) successReason() string { return reasonInline }

// read decodes the picture. A data: URI has no version, and the
// Person's own generation changes when it does, so the read never
// answers unchanged.
func (dataSource) read(_ context.Context, ref *url.URL, _ pictureVersion, backdrop color.Color) (reading, error) {
	picture, err := dataBytes(ref.Opaque)
	if err != nil {
		return reading{}, failure(reasonDecodeFailed, err)
	}
	thumbnail, err := bakeThumbnail(picture, backdrop)
	if err != nil {
		return reading{}, failure(reasonDecodeFailed, err)
	}
	return reading{thumbnail: thumbnail}, nil
}

// dataBytes answers the bytes of a data: URI's opaque part: everything
// after "data:". The base64 form is the usual one for a picture. The
// other form is percent-encoded text.
func dataBytes(opaque string) ([]byte, error) {
	header, payload, found := strings.Cut(opaque, ",")
	if !found {
		return nil, fmt.Errorf("the data: URI has no comma before its data")
	}
	if strings.HasSuffix(header, ";base64") {
		picture, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, fmt.Errorf("the data: URI's base64 does not decode: %w", err)
		}
		return picture, nil
	}
	picture, err := url.PathUnescape(payload)
	if err != nil {
		return nil, fmt.Errorf("the data: URI's percent-encoding does not decode: %w", err)
	}
	return []byte(picture), nil
}
