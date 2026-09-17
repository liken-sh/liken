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
)

// This fixture is an API server that answers the two review paths
// with real JSON and keeps every body it read, so a drill reads what
// the API sent rather than how it sent it.
type reviewAPI struct {
	mu      sync.Mutex
	answers map[string]string
	status  map[string]int
	bodies  map[string][]map[string]any
}

func newReviewAPI() *reviewAPI {
	return &reviewAPI{
		answers: map[string]string{},
		status:  map[string]int{},
		bodies:  map[string][]map[string]any{},
	}
}

func (a *reviewAPI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	raw, _ := io.ReadAll(req.Body)
	body := map[string]any{}
	_ = json.Unmarshal(raw, &body)

	a.mu.Lock()
	a.bodies[req.URL.Path] = append(a.bodies[req.URL.Path], body)
	answer, status := a.answers[req.URL.Path], a.status[req.URL.Path]
	a.mu.Unlock()

	if status == 0 {
		status = http.StatusCreated
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, answer)
}

func (a *reviewAPI) sent(t *testing.T, path string) []map[string]any {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bodies[path]
}

// An empty credentials directory is a client that reads no token
// file, so a drill needs none on disk.
func reviewClient(t *testing.T, api *reviewAPI) *Client {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return NewClient(server.URL, server.Client(), "")
}

// fieldAt reads one field out of a decoded JSON body by its path,
// the way the review drills read the bodies the API sent.
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

const authenticatedAnswer = `{
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

func TestTokenReviewCarriesTheAudience(t *testing.T) {
	api := newReviewAPI()
	api.answers[tokenReviewsPath] = authenticatedAnswer

	who, f := reviewToken(reviewClient(t, api), "a.b.c", apiAudience)
	if f != nil {
		t.Fatalf("review refused the token: %v", f)
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
		t.Errorf("username is %q", who.Username)
	}
	if who.UID != "6b3f0e1a-9c2d-4f11-8a77-1c4e5d2b3a90" {
		t.Errorf("uid is %q", who.UID)
	}
	if !reflect.DeepEqual(who.Groups, []string{"system:serviceaccounts", "system:authenticated"}) {
		t.Errorf("groups are %v", who.Groups)
	}
	if !reflect.DeepEqual(who.Extra, map[string][]string{"authentication.kubernetes.io/pod-name": {"viewer-7c9d"}}) {
		t.Errorf("extra is %v", who.Extra)
	}
}

func TestTokenReviewRefusals(t *testing.T) {
	const jose = "[invalid bearer token, square/go-jose/jwt: validation failed, token is expired (exp)]"
	cases := []struct {
		name   string
		answer string
		detail string
	}{
		{
			name:   "the wrong audience",
			answer: `{"status": {"authenticated": true, "audiences": ["https://kubernetes.default.svc"]}}`,
			detail: apiAudience,
		},
		{
			name:   "unauthenticated with the review's own words",
			answer: `{"status": {"authenticated": false, "error": "` + jose + `"}}`,
			detail: jose,
		},
		{
			name:   "authenticated for nothing at all",
			answer: `{"status": {"authenticated": false}}`,
			detail: apiAudience,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			api := newReviewAPI()
			api.answers[tokenReviewsPath] = test.answer

			who, f := reviewToken(reviewClient(t, api), "a.b.c", apiAudience)
			if who != nil {
				t.Fatalf("review admitted %v", who)
			}
			if f.status != http.StatusUnauthorized {
				t.Errorf("status is %d, want 401", f.status)
			}
			if !strings.Contains(f.detail, test.detail) {
				t.Errorf("detail is %q, want it to carry %q", f.detail, test.detail)
			}
		})
	}
}

func TestTokenReviewRefusalCarriesTheReviewsWordsVerbatim(t *testing.T) {
	const words = "invalid bearer token, service account token has been invalidated"
	api := newReviewAPI()
	api.answers[tokenReviewsPath] = `{"status": {"authenticated": false, "error": "` + words + `"}}`

	_, f := reviewToken(reviewClient(t, api), "a.b.c", apiAudience)
	if f.detail != words {
		t.Errorf("detail is %q, want %q", f.detail, words)
	}
}

func TestTokenReviewCarriesTheAPIServersFailure(t *testing.T) {
	const message = "tokenreviews.authentication.k8s.io is forbidden: User cannot create resource"
	api := newReviewAPI()
	api.status[tokenReviewsPath] = http.StatusForbidden
	api.answers[tokenReviewsPath] = `{"message": "` + message + `"}`

	_, f := reviewToken(reviewClient(t, api), "a.b.c", apiAudience)
	if f.status != http.StatusServiceUnavailable {
		t.Fatalf("status is %d, want 503", f.status)
	}
	if f.kind != problemUpstreamFailed {
		t.Errorf("kind is %q, want %q", f.kind, problemUpstreamFailed)
	}
	if !strings.Contains(f.detail, message) {
		t.Errorf("detail is %q, want it to carry the API server's message", f.detail)
	}
}

// A token's payload segment is read for exp alone, and a payload
// that does not decode leaves the expiry zero.
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

// The review carries the four subject fields from the TokenReview
// and the empty namespace a cluster-scoped Display has.
func TestSubjectAccessReviewCarriesTheSubject(t *testing.T) {
	api := newReviewAPI()
	api.answers[accessReviewsPath] = `{"status": {"allowed": true, "reason": "RBAC: allowed by ClusterRoleBinding \"viewers\""}}`

	who := &reviewedToken{
		Username: "system:serviceaccount:liken-system:display-viewer",
		UID:      "6b3f0e1a-9c2d-4f11-8a77-1c4e5d2b3a90",
		Groups:   []string{"system:serviceaccounts", "system:authenticated"},
		Extra:    map[string][]string{"authentication.kubernetes.io/pod-name": {"viewer-7c9d"}},
	}
	allowed, reason, f := authorizeSubject(reviewClient(t, api), who,
		"get", DisplayGroup, displaysPlural, screenAspect, "", "HDMI-A-1")
	if f != nil {
		t.Fatalf("review failed: %v", f)
	}
	if !allowed {
		t.Error("the review said no")
	}
	if reason != `RBAC: allowed by ClusterRoleBinding "viewers"` {
		t.Errorf("reason is %q", reason)
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

func TestSubjectAccessReviewVerdicts(t *testing.T) {
	cases := []struct {
		name    string
		answer  string
		allowed bool
		reason  string
	}{
		{
			name:    "allowed",
			answer:  `{"status": {"allowed": true, "reason": "RBAC: allowed by ClusterRole \"display-capture-viewer\""}}`,
			allowed: true,
			reason:  `RBAC: allowed by ClusterRole "display-capture-viewer"`,
		},
		{
			name:   "denied with the API server's words",
			answer: `{"status": {"allowed": false, "reason": "no RBAC policy matched"}}`,
			reason: "no RBAC policy matched",
		},
		{
			name:   "an evaluation error",
			answer: `{"status": {"allowed": false, "evaluationError": "clusterrole.rbac.authorization.k8s.io \"gone\" not found"}}`,
			reason: `clusterrole.rbac.authorization.k8s.io "gone" not found`,
		},
		{
			name: "a reason and an evaluation error",
			answer: `{"status": {"allowed": false, "reason": "no RBAC policy matched",
				"evaluationError": "clusterrole.rbac.authorization.k8s.io \"gone\" not found"}}`,
			reason: `no RBAC policy matched; clusterrole.rbac.authorization.k8s.io "gone" not found`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			api := newReviewAPI()
			api.answers[accessReviewsPath] = test.answer

			allowed, reason, f := authorizeSubject(reviewClient(t, api), &reviewedToken{Username: "viewer"},
				"get", DisplayGroup, displaysPlural, screenAspect, "", "HDMI-A-1")
			if f != nil {
				t.Fatalf("review failed: %v", f)
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

func TestSubjectAccessReviewCarriesTheAPIServersFailure(t *testing.T) {
	const message = "the server could not find the requested resource"
	api := newReviewAPI()
	api.status[accessReviewsPath] = http.StatusInternalServerError
	api.answers[accessReviewsPath] = `{"message": "` + message + `"}`

	_, _, f := authorizeSubject(reviewClient(t, api), &reviewedToken{Username: "viewer"},
		"get", DisplayGroup, displaysPlural, screenAspect, "", "HDMI-A-1")
	if f.status != http.StatusServiceUnavailable {
		t.Fatalf("status is %d, want 503", f.status)
	}
	if !strings.Contains(f.detail, message) {
		t.Errorf("detail is %q, want it to carry the API server's message", f.detail)
	}
}

// The review a cache drill holds answers a fixed verdict and counts
// how often it was asked, which is what a cache hit and a cache miss
// look like from outside.
type countingReview struct {
	mu    sync.Mutex
	asked int
	who   *reviewedToken
	deny  *fault
}

func (r *countingReview) review(token string) (*reviewedToken, *fault) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked++
	return r.who, r.deny
}

func (r *countingReview) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.asked
}

func TestTokenCacheAnswersFromMemory(t *testing.T) {
	reviews := &countingReview{who: &reviewedToken{Username: "viewer"}}
	cache := newTokenCache(reviews.review)
	clock := time.Date(2026, 9, 16, 21, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return clock }

	first, _ := cache.verdict("a.b.c")
	second, _ := cache.verdict("a.b.c")
	if first != second {
		t.Errorf("the cache answered two different verdicts")
	}
	if reviews.count() != 1 {
		t.Errorf("asked the API server %d times, want 1", reviews.count())
	}
}

func TestTokenCacheExpiresOnTheClock(t *testing.T) {
	cases := []struct {
		name    string
		expires time.Duration
		elapsed time.Duration
		asked   int
	}{
		{"inside the minute", 0, 59 * time.Second, 1},
		{"past the minute", 0, 61 * time.Second, 2},
		{"inside the token's own expiry", 10 * time.Second, 9 * time.Second, 1},
		{"past the token's own expiry", 10 * time.Second, 11 * time.Second, 2},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			start := time.Date(2026, 9, 16, 21, 0, 0, 0, time.UTC)
			who := &reviewedToken{Username: "viewer"}
			if test.expires != 0 {
				who.Expires = start.Add(test.expires)
			}
			reviews := &countingReview{who: who}
			cache := newTokenCache(reviews.review)
			clock := start
			cache.now = func() time.Time { return clock }

			cache.verdict("a.b.c")
			clock = start.Add(test.elapsed)
			cache.verdict("a.b.c")

			if reviews.count() != test.asked {
				t.Errorf("asked the API server %d times, want %d", reviews.count(), test.asked)
			}
		})
	}
}

func TestTokenCacheNeverHoldsADenial(t *testing.T) {
	reviews := &countingReview{deny: unauthenticated("invalid bearer token")}
	cache := newTokenCache(reviews.review)

	cache.verdict("a.b.c")
	cache.verdict("a.b.c")
	if reviews.count() != 2 {
		t.Errorf("asked the API server %d times, want 2", reviews.count())
	}
}

func TestTokenCacheIsSafeForConcurrentUse(t *testing.T) {
	reviews := &countingReview{who: &reviewedToken{Username: "viewer"}}
	cache := newTokenCache(reviews.review)

	var callers sync.WaitGroup
	for range 32 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			cache.verdict("a.b.c")
		}()
	}
	callers.Wait()

	if _, f := cache.verdict("a.b.c"); f != nil {
		t.Errorf("the cache refused a token it holds: %v", f)
	}
}

func TestReviewedUsernameServesAccount(t *testing.T) {
	cases := []struct {
		name     string
		username string
		serves   bool
	}{
		{"the API's own account", "system:serviceaccount:liken-system:display-api", true},
		{"another namespace", "system:serviceaccount:default:display-api", false},
		{"another account", "system:serviceaccount:liken-system:display-viewer", false},
		{"a person", "chris@liken.sh", false},
		{"a group that reads like one", "system:serviceaccounts:liken-system", false},
		{"nothing at all", "", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := servesAccount(test.username, "liken-system", "display-api"); got != test.serves {
				t.Errorf("servesAccount(%q) is %v, want %v", test.username, got, test.serves)
			}
		})
	}
}
