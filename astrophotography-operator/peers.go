package main

// Requests from the operator to its solver and worker pods. Both serve
// JSON over plain HTTP inside the cluster, and report a failure as a
// non-2xx status with a line of text, which peer returns in its error.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type body struct {
	contentType string
	data        []byte
}

func jsonBody(value any) *body {
	data, _ := json.Marshal(value)
	return &body{contentType: "application/json", data: data}
}

// starList is the body of a solve request: the .axy file, unchanged.
func starList(axy []byte) *body {
	return &body{contentType: "application/octet-stream", data: axy}
}

// peer sends one request to a pod and decodes the JSON response into
// out. Pass a nil out for a request whose response has no body, such
// as the delete of a centering frame.
func (o *operator) peer(ctx context.Context, method, url string, b *body, out any) error {
	var reader io.Reader
	if b != nil {
		reader = bytes.NewReader(b.data)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	if b != nil {
		request.Header.Set("Content-Type", b.contentType)
	}
	response, err := o.peerClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode/100 != 2 {
		text, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("%s %s returned %s: %s", method, url, response.Status, strings.TrimSpace(string(text)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}
