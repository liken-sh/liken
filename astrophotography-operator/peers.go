package main

// The operator's requests to its own pods, the solvers and the
// workers, through workerClient. Each speaks plain HTTP with JSON on
// port 8080 inside the cluster, and answers a failure with a status
// other than 2xx and a line of text, which the error carries.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// body is a request's body and its content type.
type body struct {
	contentType string
	data        []byte
}

func jsonBody(value any) *body {
	data, _ := json.Marshal(value)
	return &body{contentType: "application/json", data: data}
}

// starList is the body of a solve: the .axy as it is.
func starList(axy []byte) *body {
	return &body{contentType: "application/octet-stream", data: axy}
}

// peer sends one request to a pod and decodes the JSON answer into
// out. A nil out reads no answer, for a request such as the delete of a
// centering frame, which the worker answers with no body.
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
	response, err := o.workerClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode/100 != 2 {
		text, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("%s %s: %s: %s", method, url, response.Status, strings.TrimSpace(string(text)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}
