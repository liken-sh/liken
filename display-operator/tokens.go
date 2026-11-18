package main

// Authentication and authorization of API callers through the
// Kubernetes API server: a TokenReview finds who the caller is, and a
// SubjectAccessReview checks what it may do.
//
// Callers send Kubernetes tokens, or client certificates
// (clientcerts.go), and not a secret that this operator issues. RBAC
// already has rules for those users and groups, so a grant on
// displays/screen is an ordinary ClusterRole, and the operator has no
// credential of its own to issue, rotate, or leak.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The API reviews callers' tokens for apiAudience. The capture sidecar
// reviews the API's projected token for captureAudience. So a token
// minted for one server is refused by the other.
const (
	apiAudience     = "display-api"
	captureAudience = "display-capture"
)

// The API server returns a review's result in the response to the
// create and stores nothing, so the operator needs only the create
// verb on each resource.
const (
	authenticationAPIVersion = "authentication.k8s.io/v1"
	authorizationAPIVersion  = "authorization.k8s.io/v1"
	tokenReviewsPath         = "/apis/" + authenticationAPIVersion + "/tokenreviews"
	accessReviewsPath        = "/apis/" + authorizationAPIVersion + "/subjectaccessreviews"
)

// caller is the identity behind a request. A TokenReview fills in
// every field, and a verified client certificate fills in only
// Username and Groups. Expires is the token's exp claim, and only
// limits how long the cache keeps the entry.
type caller struct {
	Username string
	UID      string
	Groups   []string
	Extra    map[string][]string
	Expires  time.Time
}

// tokenReview has only the TokenReview fields this program uses.
type tokenReview struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Spec       tokenReviewSpec   `json:"spec"`
	Status     tokenReviewStatus `json:"status,omitempty"`
}

type tokenReviewSpec struct {
	Token     string   `json:"token"`
	Audiences []string `json:"audiences,omitempty"`
}

// Audiences lists which of the requested audiences the token is valid
// for.
type tokenReviewStatus struct {
	Authenticated bool       `json:"authenticated"`
	User          reviewUser `json:"user"`
	Audiences     []string   `json:"audiences,omitempty"`
	Error         string     `json:"error,omitempty"`
}

type reviewUser struct {
	Username string              `json:"username,omitempty"`
	UID      string              `json:"uid,omitempty"`
	Groups   []string            `json:"groups,omitempty"`
	Extra    map[string][]string `json:"extra,omitempty"`
}

// reviewToken sends a TokenReview for one audience and returns the
// caller.
//
// status.authenticated alone does not prove the audience. An
// authenticator that does not support audiences ignores
// spec.audiences and can return authenticated with an empty
// status.audiences. So reviewToken refuses the token unless
// status.audiences has the requested audience. That also refuses a
// pod's default service account token, whose audience is the API
// server.
func reviewToken(c *apiclient.Client, token, audience string) (*caller, *fault) {
	body, err := json.Marshal(tokenReview{
		APIVersion: authenticationAPIVersion,
		Kind:       "TokenReview",
		Spec:       tokenReviewSpec{Token: token, Audiences: []string{audience}},
	})
	if err != nil {
		return nil, unavailable(problemUpstreamFailed, err.Error())
	}

	result := &tokenReview{}
	if err := c.RequestJSON(http.MethodPost, tokenReviewsPath, body, result); err != nil {
		return nil, unavailable(problemUpstreamFailed, err.Error())
	}

	status := result.Status
	if !status.Authenticated || !slices.Contains(status.Audiences, audience) {
		return nil, unauthenticated(refusedDetail(status, audience))
	}
	return &caller{
		Username: status.User.Username,
		UID:      status.User.UID,
		Groups:   status.User.Groups,
		Extra:    status.User.Extra,
		Expires:  tokenExpiry(token),
	}, nil
}

// refusedDetail returns the review's error message, or names the
// missing audience when a token was authenticated without it and the
// review has no message.
func refusedDetail(status tokenReviewStatus, audience string) string {
	if status.Error != "" {
		return status.Error
	}
	return "the TokenReview did not return the audience " + audience + " in status.audiences"
}

// tokenExpiry returns the token's exp claim, or the zero time when the
// token has none. It does not verify the signature, because the
// TokenReview already did.
func tokenExpiry(token string) time.Time {
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return time.Time{}
	}
	claims := struct {
		Expires int64 `json:"exp"`
	}{}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Expires == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Expires, 0).UTC()
}

// tokenCache caches successful TokenReview results by token hash, so
// a busy client does not send a TokenReview for each request.
type tokenCache struct {
	review func(token string) (*caller, *fault)
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	who   *caller
	until time.Time
}

// tokenCacheTTL is the longest the cache keeps an entry, so a deleted
// ServiceAccount stops working within a minute. The
// SubjectAccessReview is not cached, so it runs on every request.
const tokenCacheTTL = 60 * time.Second

func newTokenCache(review func(token string) (*caller, *fault)) *tokenCache {
	return &tokenCache{review: review, now: time.Now, entries: map[string]cacheEntry{}}
}

// lookup returns the caller for a token from the cache, or from a
// new review on a miss. A failure is not cached, so a temporary API
// server error does not keep refusing the token.
func (t *tokenCache) lookup(token string) (*caller, *fault) {
	key, now := tokenKey(token), t.now()

	t.mu.Lock()
	entry, ok := t.entries[key]
	t.mu.Unlock()
	if ok && now.Before(entry.until) {
		return entry.who, nil
	}

	who, f := t.review(token)
	if f != nil {
		return nil, f
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	// Deleting expired entries on each insert keeps tokens that are used
	// once from growing the map without limit.
	for stale, entry := range t.entries {
		if !now.Before(entry.until) {
			delete(t.entries, stale)
		}
	}
	t.entries[key] = cacheEntry{who: who, until: entryExpiry(who, now)}
	return who, nil
}

// entryExpiry returns the earlier of tokenCacheTTL from now and the
// token's own expiry.
func entryExpiry(who *caller, now time.Time) time.Time {
	limit := now.Add(tokenCacheTTL)
	if !who.Expires.IsZero() && who.Expires.Before(limit) {
		return who.Expires
	}
	return limit
}

// tokenKey hashes the token, so the cache never stores the token
// itself.
func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// subjectAccessReview has only the SubjectAccessReview fields this
// program uses.
type subjectAccessReview struct {
	APIVersion string                    `json:"apiVersion"`
	Kind       string                    `json:"kind"`
	Spec       subjectAccessReviewSpec   `json:"spec"`
	Status     subjectAccessReviewStatus `json:"status,omitempty"`
}

// User, Groups, UID, and Extra name the caller of the request, so the
// API server checks that caller's permissions and not the operator's.
type subjectAccessReviewSpec struct {
	ResourceAttributes resourceAttributes  `json:"resourceAttributes"`
	User               string              `json:"user,omitempty"`
	Groups             []string            `json:"groups,omitempty"`
	UID                string              `json:"uid,omitempty"`
	Extra              map[string][]string `json:"extra,omitempty"`
}

// Namespace has no omitempty, because a Display is cluster-scoped and
// the review must send an explicit empty namespace.
type resourceAttributes struct {
	Namespace   string `json:"namespace"`
	Verb        string `json:"verb"`
	Group       string `json:"group"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource,omitempty"`
	Name        string `json:"name,omitempty"`
}

type subjectAccessReviewStatus struct {
	Allowed         bool   `json:"allowed"`
	Denied          bool   `json:"denied,omitempty"`
	Reason          string `json:"reason,omitempty"`
	EvaluationError string `json:"evaluationError,omitempty"`
}

// authorizeSubject sends a SubjectAccessReview for a caller, a verb,
// and a resource or subresource.
//
// RBAC matches a subresource as a string, so a ClusterRole can grant
// displays/screen although the CRD defines no screen subresource.
func authorizeSubject(c *apiclient.Client, who *caller,
	verb, group, resource, subresource, namespace, name string) (allowed bool, reason string, f *fault) {
	body, err := json.Marshal(subjectAccessReview{
		APIVersion: authorizationAPIVersion,
		Kind:       "SubjectAccessReview",
		Spec: subjectAccessReviewSpec{
			ResourceAttributes: resourceAttributes{
				Namespace:   namespace,
				Verb:        verb,
				Group:       group,
				Resource:    resource,
				Subresource: subresource,
				Name:        name,
			},
			User:   who.Username,
			Groups: who.Groups,
			UID:    who.UID,
			Extra:  who.Extra,
		},
	})
	if err != nil {
		return false, "", unavailable(problemUpstreamFailed, err.Error())
	}

	result := &subjectAccessReview{}
	if err := c.RequestJSON(http.MethodPost, accessReviewsPath, body, result); err != nil {
		return false, "", unavailable(problemUpstreamFailed, err.Error())
	}
	return result.Status.Allowed, accessReason(result.Status), nil
}

// accessReason joins the review's reason and its evaluation error.
func accessReason(status subjectAccessReviewStatus) string {
	if status.EvaluationError == "" {
		return status.Reason
	}
	if status.Reason == "" {
		return status.EvaluationError
	}
	return status.Reason + "; " + status.EvaluationError
}

// isServiceAccount reports whether username is the API server's username
// for the named ServiceAccount.
func isServiceAccount(username, namespace, name string) bool {
	return username == "system:serviceaccount:"+namespace+":"+name
}
