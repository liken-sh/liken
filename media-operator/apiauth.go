package main

// The API sends each check to the Kubernetes API server. A TokenReview
// returns the user that a bearer token belongs to, and a
// SubjectAccessReview returns whether that user may get the Player
// subresource of the request. The API server has the token signing
// keys, the ServiceAccount state, and the RBAC rules, so a check made in
// this process could disagree with the cluster. The TokenReview asks
// for the audience `media-api`, so the API refuses a pod's default
// token, which is minted for the API server's own audience.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// apiAudience is the audience that a token must have. authenticate
// refuses a token without it, even when the API server authenticates the
// token, and the route responds with 401.
const apiAudience = "media-api"

// Each review is a create request, and the API server returns the
// object with its status filled in.
const (
	tokenReviewsPath         = "/apis/authentication.k8s.io/v1/tokenreviews"
	subjectAccessReviewsPath = "/apis/authorization.k8s.io/v1/subjectaccessreviews"
)

// verdictWindow is the longest time that a positive verdict is cached.
// A revoked token or a deleted RoleBinding stops working within this
// time. A client that captures one Player every few seconds causes one
// TokenReview and one SubjectAccessReview in each window, not one of
// each for every request.
const verdictWindow = 60 * time.Second

// captureSubject is the caller of a request, from a TokenReview or from
// a verified client certificate. The SubjectAccessReview copies every
// exported field, because RBAC binds roles to groups and to extra
// attributes as well as to user names. A certificate sets only the user
// and the groups.
//
// A token and a certificate both set key and expires, so authorize
// caches by subject for either kind of credential. key is a hash of the
// credential, never the credential itself, and nothing logs or reports
// it. expires is the time when cached verdicts for the credential expire.
type captureSubject struct {
	User   string
	Groups []string
	UID    string
	Extra  map[string][]string

	key     string
	expires time.Time
}

// unauthenticatedError is the reason that authenticate refused a token.
// The route sends it to the client as the error_description of the
// WWW-Authenticate header (RFC 6750 section 3).
type unauthenticatedError struct{ Message string }

func (e unauthenticatedError) Error() string { return e.Message }

// TokenReview is the authentication.k8s.io object that the API server
// fills in for a token. authenticate reads status.authenticated,
// status.audiences, status.error, and status.user.
type TokenReview struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Spec       TokenReviewSpec   `json:"spec"`
	Status     TokenReviewStatus `json:"status,omitempty"`
}

type TokenReviewSpec struct {
	Token     string   `json:"token"`
	Audiences []string `json:"audiences,omitempty"`
}

type TokenReviewStatus struct {
	Authenticated bool            `json:"authenticated"`
	Audiences     []string        `json:"audiences,omitempty"`
	Error         string          `json:"error,omitempty"`
	User          TokenReviewUser `json:"user"`
}

type TokenReviewUser struct {
	Username string              `json:"username,omitempty"`
	UID      string              `json:"uid,omitempty"`
	Groups   []string            `json:"groups,omitempty"`
	Extra    map[string][]string `json:"extra,omitempty"`
}

// SubjectAccessReview asks the API server whether a user may perform a
// verb on a resource in a namespace.
type SubjectAccessReview struct {
	APIVersion string                    `json:"apiVersion"`
	Kind       string                    `json:"kind"`
	Spec       SubjectAccessReviewSpec   `json:"spec"`
	Status     SubjectAccessReviewStatus `json:"status,omitempty"`
}

type SubjectAccessReviewSpec struct {
	ResourceAttributes ResourceAttributes  `json:"resourceAttributes"`
	User               string              `json:"user,omitempty"`
	Groups             []string            `json:"groups,omitempty"`
	UID                string              `json:"uid,omitempty"`
	Extra              map[string][]string `json:"extra,omitempty"`
}

type ResourceAttributes struct {
	Namespace   string `json:"namespace,omitempty"`
	Verb        string `json:"verb"`
	Group       string `json:"group,omitempty"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource,omitempty"`
	Name        string `json:"name,omitempty"`
}

type SubjectAccessReviewStatus struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

// authorizer sends the reviews to the API server and caches positive
// verdicts. The media-api process creates one authorizer, so every
// request shares its cache.
type authorizer struct {
	client *apiclient.Client
	// A test replaces now to move the clock past verdictWindow.
	now   func() time.Time
	cache *verdictCache
}

func newAuthorizer(client *apiclient.Client) *authorizer {
	return &authorizer{
		client: client,
		now:    time.Now,
		cache: &verdictCache{
			subjects: map[string]cachedSubject{},
			grants:   map[string]time.Time{},
		},
	}
}

// authenticate returns the caller that a token belongs to, from the
// cache or from a TokenReview. status.authenticated alone does not prove
// the audience. An authenticator that does not support audiences ignores
// spec.audiences and can return authenticated with an empty
// status.audiences. So authenticate refuses the token unless
// status.audiences has apiAudience.
func (a *authorizer) authenticate(token string) (captureSubject, error) {
	key := tokenKey(token)
	if subject, ok := a.cache.subject(key, a.now()); ok {
		return subject, nil
	}

	body, err := json.Marshal(&TokenReview{
		APIVersion: "authentication.k8s.io/v1",
		Kind:       "TokenReview",
		Spec:       TokenReviewSpec{Token: token, Audiences: []string{apiAudience}},
	})
	if err != nil {
		return captureSubject{}, err
	}
	reviewed := &TokenReview{}
	if err := a.client.RequestJSON(http.MethodPost, tokenReviewsPath, body, reviewed); err != nil {
		return captureSubject{}, fmt.Errorf("reviewing the token: %w", err)
	}

	if !reviewed.Status.Authenticated {
		return captureSubject{}, unauthenticatedError{
			Message: reviewMessage(reviewed.Status.Error, "the token review did not authenticate the token"),
		}
	}
	if !slices.Contains(reviewed.Status.Audiences, apiAudience) {
		return captureSubject{}, unauthenticatedError{
			Message: reviewMessage(reviewed.Status.Error, `the audiences of the token do not include "`+apiAudience+`"`),
		}
	}

	subject := captureSubject{
		User:    reviewed.Status.User.Username,
		Groups:  reviewed.Status.User.Groups,
		UID:     reviewed.Status.User.UID,
		Extra:   reviewed.Status.User.Extra,
		key:     key,
		expires: a.verdictUntil(token),
	}
	a.cache.storeSubject(key, subject, subject.expires)
	return subject, nil
}

// reviewMessage returns the API server's error text for a refused token,
// or fallback when the API server gave none.
func reviewMessage(refusal, fallback string) string {
	if refusal != "" {
		return refusal
	}
	return fallback
}

// authorize returns whether the subject may get a subresource of one
// Player, or the Player itself when subresource is empty. The review
// names the Player, because a Role can grant access to specific Players
// with resourceNames. A denial returns false and a nil error. An error
// means that the review request failed.
func (a *authorizer) authorize(subject captureSubject, namespace, name, subresource string) (bool, error) {
	key := grantKey(subject.key, namespace, name, subresource)
	if a.cache.granted(key, a.now()) {
		return true, nil
	}

	body, err := json.Marshal(&SubjectAccessReview{
		APIVersion: "authorization.k8s.io/v1",
		Kind:       "SubjectAccessReview",
		Spec: SubjectAccessReviewSpec{
			ResourceAttributes: ResourceAttributes{
				Namespace:   namespace,
				Verb:        "get",
				Group:       "media.liken.sh",
				Resource:    "players",
				Subresource: subresource,
				Name:        name,
			},
			User:   subject.User,
			Groups: subject.Groups,
			UID:    subject.UID,
			Extra:  subject.Extra,
		},
	})
	if err != nil {
		return false, err
	}
	reviewed := &SubjectAccessReview{}
	if err := a.client.RequestJSON(http.MethodPost, subjectAccessReviewsPath, body, reviewed); err != nil {
		return false, fmt.Errorf("reviewing the subject's access: %w", err)
	}

	// A denial is not cached, so a new grant takes effect on the next
	// request, at the cost of one review for each refused request.
	if !reviewed.Status.Allowed {
		return false, nil
	}
	a.cache.storeGrant(key, subject.expires)
	return true, nil
}

// verdictUntil returns when the cached verdicts for this token expire.
func (a *authorizer) verdictUntil(token string) time.Time {
	expiry, _ := tokenExpiry(token)
	return verdictExpiry(a.now(), expiry)
}

// verdictExpiry returns when cached verdicts expire, for a token and for
// a certificate: at the end of verdictWindow, or at the credential's
// expiry when that is earlier. A zero expiry means that the expiry is
// not known, so the verdict lasts the full window.
func verdictExpiry(now, expiry time.Time) time.Time {
	until := now.Add(verdictWindow)
	if !expiry.IsZero() && expiry.Before(until) {
		return expiry
	}
	return until
}

// tokenKey returns the SHA-256 of the token as hex, so the cache never
// stores the token.
func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// grantKey is the cache key for one subject's access to one Player
// subresource. It includes the Player, because a Role with
// resourceNames can allow one Player and deny another in the same
// namespace. credential is the subject's key field.
func grantKey(credential, namespace, name, subresource string) string {
	return credential + "/" + namespace + "/" + name + "/" + subresource
}

// tokenExpiry returns the exp claim of a JWT without verifying the
// token, because the TokenReview already did. When the token cannot be
// decoded, it returns false and the verdict lasts the full window.
func tokenExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	claims, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, false
	}
	expiry := struct {
		Expires int64 `json:"exp"`
	}{}
	if err := json.Unmarshal(claims, &expiry); err != nil || expiry.Expires == 0 {
		return time.Time{}, false
	}
	return time.Unix(expiry.Expires, 0), true
}

// verdictCache stores positive verdicts. Request goroutines use it
// concurrently, so a mutex guards it.
type verdictCache struct {
	mutex    sync.Mutex
	subjects map[string]cachedSubject
	grants   map[string]time.Time
}

type cachedSubject struct {
	subject captureSubject
	until   time.Time
}

func (c *verdictCache) subject(key string, now time.Time) (captureSubject, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	cached, ok := c.subjects[key]
	if !ok || !now.Before(cached.until) {
		return captureSubject{}, false
	}
	return cached.subject, true
}

func (c *verdictCache) storeSubject(key string, subject captureSubject, until time.Time) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.subjects[key] = cachedSubject{subject: subject, until: until}
}

func (c *verdictCache) granted(key string, now time.Time) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	until, ok := c.grants[key]
	return ok && now.Before(until)
}

func (c *verdictCache) storeGrant(key string, until time.Time) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.grants[key] = until
}
