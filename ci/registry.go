package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// A Registry reads and probes the packages of one organization on an
// OCI registry through the distribution API. For ghcr.io, the base is
// https://ghcr.io and the owner is liken-sh.
type Registry struct {
	Base   string
	Owner  string
	Client *http.Client
	// Token is the credential for the token service. Anonymous reads
	// need none, because the packages are public. A write probe needs
	// a token that may push, such as the workflow's GITHUB_TOKEN.
	Token string
}

func (r Registry) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return http.DefaultClient
}

// bearer asks the registry's token service for a token with the
// scope. The service grants the scopes that the credential allows and
// drops the rest without an error, so a granted token does not prove
// a push is allowed. Only the request that uses it can prove that.
func (r Registry) bearer(pkg, actions string) (string, error) {
	u := fmt.Sprintf("%s/token?service=%s&scope=%s", r.Base, url.QueryEscape(strings.TrimPrefix(strings.TrimPrefix(r.Base, "https://"), "http://")),
		url.QueryEscape(fmt.Sprintf("repository:%s/%s:%s", r.Owner, pkg, actions)))
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	if r.Token != "" {
		req.SetBasicAuth("token", r.Token)
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", tokenRefused{pkg: pkg, status: resp.Status, code: resp.StatusCode}
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Token, nil
}

// tokenRefused is the token service's refusal of a scope.
type tokenRefused struct {
	pkg, status string
	code        int
}

func (e tokenRefused) Error() string {
	return fmt.Sprintf("the token service answered %s for %s", e.status, e.pkg)
}

// Tags lists every tag of the package. A package that does not exist
// has no tags. ghcr's token service refuses an anonymous pull of a
// package that does not exist, the same as a private one, and every
// package of the project is public, so a refusal there means the
// package does not exist yet.
func (r Registry) Tags(pkg string) ([]string, error) {
	token, err := r.bearer(pkg, "pull")
	if refused, ok := err.(tokenRefused); ok && r.Token == "" &&
		(refused.code == http.StatusForbidden || refused.code == http.StatusUnauthorized || refused.code == http.StatusNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tags []string
	next := fmt.Sprintf("%s/v2/%s/%s/tags/list?n=1000", r.Base, r.Owner, pkg)
	for next != "" {
		req, err := http.NewRequest(http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := r.client().Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("listing the tags of %s: %s", pkg, resp.Status)
		}
		var page struct {
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		tags = append(tags, page.Tags...)
		next = nextPage(r.Base, resp.Header.Get("Link"))
	}
	return tags, nil
}

// nextPage reads the next page's address from a Link header, in the
// form `</v2/...?last=x&n=1000>; rel="next"`.
func nextPage(base, link string) string {
	target, rest, ok := strings.Cut(link, ";")
	if !ok || !strings.Contains(rest, `rel="next"`) {
		return ""
	}
	target = strings.Trim(strings.TrimSpace(target), "<>")
	if strings.HasPrefix(target, "/") {
		return base + target
	}
	return target
}

// CanPush proves that the credential may push to the package. It
// starts a blob upload, which the registry allows only for a
// credential that may write, and then cancels the upload, so the probe
// leaves nothing in the package. A package that does not exist yet
// answers the same way: the first push creates it.
func (r Registry) CanPush(pkg string) error {
	token, err := r.bearer(pkg, "pull,push")
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v2/%s/%s/blobs/uploads/", r.Base, r.Owner, pkg), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := r.client().Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("the registry refused an upload to %s: %s", pkg, resp.Status)
	}
	if location := resp.Header.Get("Location"); location != "" {
		if strings.HasPrefix(location, "/") {
			location = r.Base + location
		}
		cancel, err := http.NewRequest(http.MethodDelete, location, nil)
		if err == nil {
			cancel.Header.Set("Authorization", "Bearer "+token)
			if resp, err := r.client().Do(cancel); err == nil {
				resp.Body.Close()
			}
		}
	}
	return nil
}
