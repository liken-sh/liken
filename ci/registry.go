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

// manifestTypes are the manifest formats that Labels reads: an image
// index, which buildx pushes for more than one platform or with an
// attestation, and a single image manifest.
const manifestTypes = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

// Labels reads the labels of the image at the tag. When the tag names
// an index, the labels come from its linux/amd64 image, or from its
// first image when it holds no linux/amd64 one. buildx writes an
// attestation into an index as an image of the platform unknown/unknown,
// so that entry never counts. A tag or a package that does not exist
// has no labels and no error, the same as Tags.
func (r Registry) Labels(pkg, tag string) (map[string]string, error) {
	token, err := r.bearer(pkg, "pull")
	if refused, ok := err.(tokenRefused); ok && r.Token == "" &&
		(refused.code == http.StatusForbidden || refused.code == http.StatusUnauthorized || refused.code == http.StatusNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	get := func(path, accept string, out any) (bool, error) {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v2/%s/%s/%s", r.Base, r.Owner, pkg, path), nil)
		if err != nil {
			return false, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		resp, err := r.client().Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("reading %s of %s: %s", path, pkg, resp.Status)
		}
		return true, json.NewDecoder(resp.Body).Decode(out)
	}
	type platform struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	}
	var index struct {
		Manifests []struct {
			Digest   string   `json:"digest"`
			Platform platform `json:"platform"`
		} `json:"manifests"`
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	found, err := get("manifests/"+tag, manifestTypes, &index)
	if err != nil || !found {
		return nil, err
	}
	config := index.Config.Digest
	if len(index.Manifests) > 0 {
		digest := ""
		for _, m := range index.Manifests {
			if m.Platform.OS == "unknown" || m.Platform.OS == "" {
				continue
			}
			if digest == "" || m.Platform == (platform{"linux", "amd64"}) {
				digest = m.Digest
			}
		}
		if digest == "" {
			return nil, fmt.Errorf("%s:%s is an index with no image in it", pkg, tag)
		}
		var manifest struct {
			Config struct {
				Digest string `json:"digest"`
			} `json:"config"`
		}
		if found, err := get("manifests/"+digest, manifestTypes, &manifest); err != nil || !found {
			return nil, fmt.Errorf("reading %s of %s:%s: %v", digest, pkg, tag, orNotFound(err))
		}
		config = manifest.Config.Digest
	}
	var blob struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if found, err := get("blobs/"+config, "", &blob); err != nil || !found {
		return nil, fmt.Errorf("reading the config of %s:%s: %v", pkg, tag, orNotFound(err))
	}
	if blob.Config.Labels == nil {
		return map[string]string{}, nil
	}
	return blob.Config.Labels, nil
}

// orNotFound names a missing object that the registry's index or
// manifest points at, where get reports it as no error.
func orNotFound(err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("the registry has no such object")
}
