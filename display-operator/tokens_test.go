package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// reviewAPI is a fake API server for TokenReview and
// SubjectAccessReview. It returns the configured JSON for each path
// and records each request body.
type reviewAPI struct {
	mu        sync.Mutex
	responses map[string]string
	status    map[string]int
	bodies    map[string][]map[string]any
}

func newReviewAPI() *reviewAPI {
	return &reviewAPI{
		responses: map[string]string{},
		status:    map[string]int{},
		bodies:    map[string][]map[string]any{},
	}
}

func (a *reviewAPI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	raw, _ := io.ReadAll(req.Body)
	body := map[string]any{}
	_ = json.Unmarshal(raw, &body)

	a.mu.Lock()
	a.bodies[req.URL.Path] = append(a.bodies[req.URL.Path], body)
	response, status := a.responses[req.URL.Path], a.status[req.URL.Path]
	a.mu.Unlock()

	if status == 0 {
		status = http.StatusCreated
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, response)
}

func (a *reviewAPI) sent(t *testing.T, path string) []map[string]any {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bodies[path]
}

// reviewClient returns a client with no credentials directory, so a
// test needs no token file on disk.
func reviewClient(t *testing.T, api *reviewAPI) *apiclient.Client {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return apiclient.New(server.URL, server.Client(), "")
}

// fieldAt returns the value at a path of keys in a decoded JSON body.
func fieldAt(t *testing.T, body map[string]any, path ...string) any {
	t.Helper()
	var current any = body
	for _, step := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("field %q: %v is not an object", step, current)
		}
		current, ok = object[step]
		if !ok {
			t.Fatalf("field %q is absent from %v", step, object)
		}
	}
	return current
}

const authenticatedResponse = `{
	"apiVersion": "authentication.k8s.io/v1",
	"kind": "TokenReview",
	"status": {
		"authenticated": true,
		"audiences": ["display-api"],
		"user": {
			"username": "system:serviceaccount:liken-system:display-viewer",
			"uid": "6b3f0e1a-9c2d-4f11-8a77-1c4e5d2b3a90",
			"groups": ["system:serviceaccounts", "system:authenticated"],
			"extra": {"authentication.kubernetes.io/pod-name": ["viewer-7c9d"]}
		}
	}
}`

func TestTokenReviewSendsTheAudience(t *testing.T) {
	api := newReviewAPI()
	api.responses[tokenReviewsPath] = authenticatedResponse

	who, f := reviewToken(reviewClient(t, api), "a.b.c", apiAudience)
	if f != nil {
		t.Fatalf("reviewing the token: %v", f)
	}

	sent := api.sent(t, tokenReviewsPath)
	if len(sent) != 1 {
		t.Fatalf("sent %d reviews, want 1", len(sent))
	}
	if got := fieldAt(t, sent[0], "apiVersion"); got != authenticationAPIVersion {
		t.Errorf("apiVersion is %v, want %v", got, authenticationAPIVersion)
	}
	if got := fieldAt(t, sent[0], "kind"); got != "TokenReview" {
		t.Errorf("kind is %v, want TokenReview", got)
	}
	if got := fieldAt(t, sent[0], "spec", "token"); got != "a.b.c" {
		t.Errorf("spec.token is %v, want a.b.c", got)
	}
	if got := fieldAt(t, sent[0], "spec", "audiences"); !reflect.DeepEqual(got, []any{apiAudience}) {
		t.Errorf("spec.audiences is %v, want [%s]", got, apiAudience)
	}
	if who.Username != "system:serviceaccount:liken-system:display-viewer" {
		t.Errorf("username is %q, want the display-viewer service account", who.Username)
	}
	if who.UID != "6b3f0e1a-9c2d-4f11-8a77-1c4e5d2b3a90" {
		t.Errorf("uid is %q, want the one the review returned", who.UID)
	}
	if !reflect.DeepEqual(who.Groups, []string{"system:serviceaccounts", "system:authenticated"}) {
		t.Errorf("groups are %v, want the two the review returned", who.Groups)
	}
	if !reflect.DeepEqual(who.Extra, map[string][]string{"authentication.kubernetes.io/pod-name": {"viewer-7c9d"}}) {
		t.Errorf("extra is %v, want the pod-name entry the review returned", who.Extra)
	}
}

func TestTokenReviewRefusals(t *testing.T) {
	const jose = "[invalid bearer token, square/go-jose/jwt: validation failed, token is expired (exp)]"
	cases := []struct {
		name     string
		response string
		detail   string
	}{
		{
			name:     "the wrong audience",
			response: `{"status": {"authenticated": true, "audiences": ["https://kubernetes.default.svc"]}}`,
			detail:   apiAudience,
		},
		{
			name:     "unauthenticated with the review's error",
			response: `{"status": {"authenticated": false, "error": "` + jose + `"}}`,
			detail:   jose,
		},
		{
			name:     "unauthenticated with no error",
			response: `{"status": {"authenticated": false}}`,
			detail:   apiAudience,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			api := newReviewAPI()
			api.responses[tokenReviewsPath] = test.response

			who, f := reviewToken(reviewClient(t, api), "a.b.c", apiAudience)
			if who != nil {
				t.Fatalf("the token was accepted as %v, want it refused", who)
			}
			if f.status != http.StatusUnauthorized {
				t.Errorf("status is %d, want 401", f.status)
			}
			if !strings.Contains(f.detail, test.detail) {
				t.Errorf("detail is %q, want it to contain %q", f.detail, test.detail)
			}
		})
	}
}

func TestTokenReviewRefusalReturnsTheReviewErrorUnchanged(t *testing.T) {
	const reviewError = "invalid bearer token, service account token has been invalidated"
	api := newReviewAPI()
	api.responses[tokenReviewsPath] = `{"status": {"authenticated": false, "error": "` + reviewError + `"}}`

	_, f := reviewToken(reviewClient(t, api), "a.b.c", apiAudience)
	if f.detail != reviewError {
		t.Errorf("detail is %q, want %q", f.detail, reviewError)
	}
}

func TestTokenReviewReturnsTheAPIServersFailure(t *testing.T) {
	const message = "tokenreviews.authentication.k8s.io is forbidden: User cannot create resource"
	api := newReviewAPI()
	api.status[tokenReviewsPath] = http.StatusForbidden
	api.responses[tokenReviewsPath] = `{"message": "` + message + `"}`

	_, f := reviewToken(reviewClient(t, api), "a.b.c", apiAudience)
	if f.status != http.StatusServiceUnavailable {
		t.Fatalf("status is %d, want 503", f.status)
	}
	if f.kind != problemUpstreamFailed {
		t.Errorf("kind is %q, want %q", f.kind, problemUpstreamFailed)
	}
	if !strings.Contains(f.detail, message) {
		t.Errorf("detail is %q, want it to include the API server's message", f.detail)
	}
}

func TestTokenExpiryReadsTheExpClaim(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  time.Time
	}{
		{"an exp claim", signedToken(`{"exp":1788000000}`), time.Unix(1788000000, 0).UTC()},
		{"no exp claim", signedToken(`{"sub":"viewer"}`), time.Time{}},
		{"a payload that is not JSON", signedToken(`not json`), time.Time{}},
		{"a payload that is not base64", "header.!!!.signature", time.Time{}},
		{"not a JWT at all", "opaque-token", time.Time{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := tokenExpiry(test.token); !got.Equal(test.want) {
				t.Errorf("expiry is %v, want %v", got, test.want)
			}
		})
	}
}

func signedToken(payload string) string {
	segment := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`)) + "." + segment + ".signature"
}

func TestSubjectAccessReviewSendsTheSubject(t *testing.T) {
	api := newReviewAPI()
	api.responses[accessReviewsPath] = `{"status": {"allowed": true, "reason": "RBAC: allowed by ClusterRoleBinding \"viewers\""}}`

	who := &caller{
		Username: "system:serviceaccount:liken-system:display-viewer",
		UID:      "6b3f0e1a-9c2d-4f11-8a77-1c4e5d2b3a90",
		Groups:   []string{"system:serviceaccounts", "system:authenticated"},
		Extra:    map[string][]string{"authentication.kubernetes.io/pod-name": {"viewer-7c9d"}},
	}
	allowed, reason, f := authorizeSubject(reviewClient(t, api), who,
		"get", DisplayGroup, displaysPlural, screenAspect, "", "HDMI-A-1")
	if f != nil {
		t.Fatalf("reviewing the access: %v", f)
	}
	if !allowed {
		t.Error("the access was denied, want it allowed")
	}
	if reason != `RBAC: allowed by ClusterRoleBinding "viewers"` {
		t.Errorf("reason is %q, want the RBAC reason for the viewers binding", reason)
	}

	sent := api.sent(t, accessReviewsPath)
	if len(sent) != 1 {
		t.Fatalf("sent %d reviews, want 1", len(sent))
	}
	fields := []struct {
		name string
		path []string
		want any
	}{
		{"apiVersion", []string{"apiVersion"}, authorizationAPIVersion},
		{"kind", []string{"kind"}, "SubjectAccessReview"},
		{"user", []string{"spec", "user"}, "system:serviceaccount:liken-system:display-viewer"},
		{"uid", []string{"spec", "uid"}, "6b3f0e1a-9c2d-4f11-8a77-1c4e5d2b3a90"},
		{"groups", []string{"spec", "groups"}, []any{"system:serviceaccounts", "system:authenticated"}},
		{"extra", []string{"spec", "extra"}, map[string]any{"authentication.kubernetes.io/pod-name": []any{"viewer-7c9d"}}},
		{"namespace", []string{"spec", "resourceAttributes", "namespace"}, ""},
		{"verb", []string{"spec", "resourceAttributes", "verb"}, "get"},
		{"group", []string{"spec", "resourceAttributes", "group"}, "display.liken.sh"},
		{"resource", []string{"spec", "resourceAttributes", "resource"}, "displays"},
		{"subresource", []string{"spec", "resourceAttributes", "subresource"}, "screen"},
		{"name", []string{"spec", "resourceAttributes", "name"}, "HDMI-A-1"},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			if got := fieldAt(t, sent[0], field.path...); !reflect.DeepEqual(got, field.want) {
				t.Errorf("%s is %#v, want %#v", field.name, got, field.want)
			}
		})
	}
}

func TestSubjectAccessReviewResults(t *testing.T) {
	cases := []struct {
		name     string
		response string
		allowed  bool
		reason   string
	}{
		{
			name:     "allowed",
			response: `{"status": {"allowed": true, "reason": "RBAC: allowed by ClusterRole \"display-capture-viewer\""}}`,
			allowed:  true,
			reason:   `RBAC: allowed by ClusterRole "display-capture-viewer"`,
		},
		{
			name:     "denied with the API server's reason",
			response: `{"status": {"allowed": false, "reason": "no RBAC policy matched"}}`,
			reason:   "no RBAC policy matched",
		},
		{
			name:     "an evaluation error",
			response: `{"status": {"allowed": false, "evaluationError": "clusterrole.rbac.authorization.k8s.io \"gone\" not found"}}`,
			reason:   `clusterrole.rbac.authorization.k8s.io "gone" not found`,
		},
		{
			name: "a reason and an evaluation error",
			response: `{"status": {"allowed": false, "reason": "no RBAC policy matched",
				"evaluationError": "clusterrole.rbac.authorization.k8s.io \"gone\" not found"}}`,
			reason: `no RBAC policy matched; clusterrole.rbac.authorization.k8s.io "gone" not found`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			api := newReviewAPI()
			api.responses[accessReviewsPath] = test.response

			allowed, reason, f := authorizeSubject(reviewClient(t, api), &caller{Username: "viewer"},
				"get", DisplayGroup, displaysPlural, screenAspect, "", "HDMI-A-1")
			if f != nil {
				t.Fatalf("reviewing the access: %v", f)
			}
			if allowed != test.allowed {
				t.Errorf("allowed is %v, want %v", allowed, test.allowed)
			}
			if reason != test.reason {
				t.Errorf("reason is %q, want %q", reason, test.reason)
			}
		})
	}
}

func TestSubjectAccessReviewReturnsTheAPIServersFailure(t *testing.T) {
	const message = "the server could not find the requested resource"
	api := newReviewAPI()
	api.status[accessReviewsPath] = http.StatusInternalServerError
	api.responses[accessReviewsPath] = `{"message": "` + message + `"}`

	_, _, f := authorizeSubject(reviewClient(t, api), &caller{Username: "viewer"},
		"get", DisplayGroup, displaysPlural, screenAspect, "", "HDMI-A-1")
	if f.status != http.StatusServiceUnavailable {
		t.Fatalf("status is %d, want 503", f.status)
	}
	if !strings.Contains(f.detail, message) {
		t.Errorf("detail is %q, want it to include the API server's message", f.detail)
	}
}

// countingReview returns a fixed result and counts its calls, so a
// test can tell a cache hit from a miss.
type countingReview struct {
	mu    sync.Mutex
	calls int
	who   *caller
	deny  *fault
}

func (r *countingReview) review(token string) (*caller, *fault) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.who, r.deny
}

func (r *countingReview) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestTokenCacheReturnsTheCachedCaller(t *testing.T) {
	reviews := &countingReview{who: &caller{Username: "viewer"}}
	cache := newTokenCache(reviews.review)
	clock := time.Date(2026, 9, 16, 21, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return clock }

	first, _ := cache.lookup("a.b.c")
	second, _ := cache.lookup("a.b.c")
	if first != second {
		t.Errorf("the cache returned two different callers, want the same caller")
	}
	if reviews.count() != 1 {
		t.Errorf("sent %d reviews, want 1", reviews.count())
	}
}

func TestTokenCacheExpiresOnTheClock(t *testing.T) {
	cases := []struct {
		name    string
		expires time.Duration
		elapsed time.Duration
		reviews int
	}{
		{"inside the minute", 0, 59 * time.Second, 1},
		{"past the minute", 0, 61 * time.Second, 2},
		{"inside the token's own expiry", 10 * time.Second, 9 * time.Second, 1},
		{"past the token's own expiry", 10 * time.Second, 11 * time.Second, 2},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			start := time.Date(2026, 9, 16, 21, 0, 0, 0, time.UTC)
			who := &caller{Username: "viewer"}
			if test.expires != 0 {
				who.Expires = start.Add(test.expires)
			}
			reviews := &countingReview{who: who}
			cache := newTokenCache(reviews.review)
			clock := start
			cache.now = func() time.Time { return clock }

			cache.lookup("a.b.c")
			clock = start.Add(test.elapsed)
			cache.lookup("a.b.c")

			if reviews.count() != test.reviews {
				t.Errorf("sent %d reviews, want %d", reviews.count(), test.reviews)
			}
		})
	}
}

func TestTokenCacheDoesNotCacheARefusal(t *testing.T) {
	reviews := &countingReview{deny: unauthenticated("invalid bearer token")}
	cache := newTokenCache(reviews.review)

	cache.lookup("a.b.c")
	cache.lookup("a.b.c")
	if reviews.count() != 2 {
		t.Errorf("sent %d reviews, want 2", reviews.count())
	}
}

func TestTokenCacheIsSafeForConcurrentUse(t *testing.T) {
	reviews := &countingReview{who: &caller{Username: "viewer"}}
	cache := newTokenCache(reviews.review)

	var callers sync.WaitGroup
	for range 32 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			cache.lookup("a.b.c")
		}()
	}
	callers.Wait()

	if _, f := cache.lookup("a.b.c"); f != nil {
		t.Errorf("the cache refused a token it had cached: %v", f)
	}
}

func TestServesAccount(t *testing.T) {
	cases := []struct {
		name     string
		username string
		serves   bool
	}{
		{"the API's own account", "system:serviceaccount:liken-system:display-api", true},
		{"another namespace", "system:serviceaccount:default:display-api", false},
		{"another account", "system:serviceaccount:liken-system:display-viewer", false},
		{"a person", "viewer@example.com", false},
		{"a group name", "system:serviceaccounts:liken-system", false},
		{"nothing at all", "", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := isServiceAccount(test.username, "liken-system", "display-api"); got != test.serves {
				t.Errorf("servesAccount(%q) is %v, want %v", test.username, got, test.serves)
			}
		})
	}
}
