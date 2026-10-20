package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
)

// maxDownload bounds a catalog download. The compressed LVFS catalog is
// a few megabytes, so a body past this size is a mistake or an attack,
// and the scan refuses it instead of filling the pod's memory.
const maxDownload = 256 << 20

// get downloads one URL. Any status but 200 is an error, because a
// redirect that the client followed to an error page must not become a
// catalog.
func get(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", address, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxDownload {
		return nil, fmt.Errorf("%s is larger than %d bytes", address, maxDownload)
	}
	return body, nil
}

// siblingURL names a file in the directory of the unversioned catalog
// URL. The jcat gives the versioned file name, and the versioned file
// sits next to the unversioned one on the server.
func siblingURL(catalogURL, name string) (string, error) {
	u, err := url.Parse(catalogURL)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(path.Dir(u.Path), name)
	return u.String(), nil
}
