package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
)

// maxDownload is far larger than the compressed LVFS catalog. The scan
// refuses a larger body, from a wrong URL or a hostile server, so that
// it does not fill the pod's memory.
const maxDownload = 256 << 20

// get fails on any status other than 200, so that an error page at
// the end of a redirect is not read as a catalog.
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
		return nil, fmt.Errorf("%s returned %s", address, resp.Status)
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

// siblingURL returns the URL of a file in the directory of the
// unversioned catalog URL, keeping the query. The jcat gives only the
// name of the versioned catalog file, and the server keeps that file in
// the same directory.
func siblingURL(catalogURL, name string) (string, error) {
	u, err := url.Parse(catalogURL)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(path.Dir(u.Path), name)
	return u.String(), nil
}
