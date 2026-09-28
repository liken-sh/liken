package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// accessServer stands in for the API server's SubjectAccessReview
// endpoint.
type accessServer struct {
	sent   accessReview
	answer accessReviewStatus
	server *httptest.Server
}

func newAccessServer(t *testing.T, answer accessReviewStatus) *accessServer {
	t.Helper()
	fake := &accessServer{answer: answer}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != accessReviewPath {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&fake.sent); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		answered := fake.sent
		answered.Status = fake.answer
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&answered)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *accessServer) authorizer() *authorizer {
	return newAuthorizer(NewClient(f.server.URL, f.server.Client(), ""))
}

// listener is the caller a TokenReview reported, with every field a
// SubjectAccessReview copies.
var listener = caller{
	Username: "system:serviceaccount:liken-system:listener",
	UID:      "1f0e3dad-9990-4f65-8b2c-77b3f2a27c81",
	Groups: []string{
		"system:serviceaccounts",
		"system:serviceaccounts:liken-system",
		"system:authenticated",
	},
	Extra: map[string][]string{
		"authentication.kubernetes.io/credential-id": {"JTI=0f1b2c3d"},
	},
}

func TestATapAuthorizesOnTheSubresourceWithTheName(t *testing.T) {
	fake := newAccessServer(t, accessReviewStatus{Allowed: true})
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.wav")
	allowed, _, err := fake.authorizer().authorize(listener, route, name)
	if err != nil || !allowed {
		t.Fatalf("the review answered %v (%v)", allowed, err)
	}

	attributes := fake.sent.Spec.ResourceAttributes
	if attributes == nil {
		t.Fatal("the review carried no resource attributes")
	}
	if attributes.Verb != "get" {
		t.Errorf("the verb is %q, want get", attributes.Verb)
	}
	if attributes.Group != "audio.liken.sh" {
		t.Errorf("the group is %q", attributes.Group)
	}
	if attributes.Resource != "sinks" || attributes.Subresource != "audio" {
		t.Errorf("the check is on %s/%s, want sinks/audio",
			attributes.Resource, attributes.Subresource)
	}
	if attributes.Name != "kitchen" {
		t.Errorf("the name is %q, want kitchen", attributes.Name)
	}
	// Both kinds are cluster-scoped, so the namespace is empty.
	if attributes.Namespace != "" {
		t.Errorf("the namespace is %q, and both kinds are cluster-scoped", attributes.Namespace)
	}
}

func TestTheReviewCarriesEveryFieldTheTokenReviewReported(t *testing.T) {
	fake := newAccessServer(t, accessReviewStatus{Allowed: true})
	route, name, _ := matchRoute("/v1/audio/sources/desk/audio.opus")
	if _, _, err := fake.authorizer().authorize(listener, route, name); err != nil {
		t.Fatal(err)
	}
	if fake.sent.Spec.User != listener.Username {
		t.Errorf("the user is %q", fake.sent.Spec.User)
	}
	if fake.sent.Spec.UID != listener.UID {
		t.Errorf("the uid is %q", fake.sent.Spec.UID)
	}
	if !slices.Equal(fake.sent.Spec.Groups, listener.Groups) {
		t.Errorf("the groups are %v", fake.sent.Spec.Groups)
	}
	if len(fake.sent.Spec.Extra) != 1 ||
		!slices.Equal(fake.sent.Spec.Extra["authentication.kubernetes.io/credential-id"],
			listener.Extra["authentication.kubernetes.io/credential-id"]) {
		t.Errorf("the extra is %v", fake.sent.Spec.Extra)
	}
	if fake.sent.Spec.ResourceAttributes.Resource != "sources" {
		t.Errorf("the resource is %q, want sources", fake.sent.Spec.ResourceAttributes.Resource)
	}
}

func TestAnInfoRouteAuthorizesOnTheOrdinaryResource(t *testing.T) {
	fake := newAccessServer(t, accessReviewStatus{Allowed: true})
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen")
	if _, _, err := fake.authorizer().authorize(listener, route, name); err != nil {
		t.Fatal(err)
	}
	attributes := fake.sent.Spec.ResourceAttributes
	if attributes.Resource != "sinks" || attributes.Subresource != "" {
		t.Errorf("the check is on %s/%s, want sinks with no subresource",
			attributes.Resource, attributes.Subresource)
	}
}

func TestTheTwoDocumentsNeedNoAuthorization(t *testing.T) {
	fake := newAccessServer(t, accessReviewStatus{Denied: true, Reason: "no"})
	for _, path := range []string{"/v1/audio", "/v1/audio/openapi.json"} {
		route, name, _ := matchRoute(path)
		allowed, _, err := fake.authorizer().authorize(listener, route, name)
		if err != nil || !allowed {
			t.Errorf("%s was refused: %v (%v)", path, allowed, err)
		}
	}
	if fake.sent.Kind != "" {
		t.Error("a document route asked the API server to authorize it")
	}
}

func TestARefusalCarriesTheAuthorizersOwnWords(t *testing.T) {
	fake := newAccessServer(t, accessReviewStatus{
		Denied: true,
		Reason: `no RBAC policy matched; clusterrole "audio-capture-viewer" not bound`,
	})
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.wav")
	allowed, reason, err := fake.authorizer().authorize(listener, route, name)
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("a denial was read as a grant")
	}
	if !strings.Contains(reason, "audio-capture-viewer") {
		t.Errorf("the reason is %q, and it must carry the authorizer's own words", reason)
	}
}

func TestAReviewThatIsNeitherAllowedNorDeniedIsARefusal(t *testing.T) {
	// The API server answers allowed false with denied false when no
	// authorizer had an opinion, which is a refusal.
	fake := newAccessServer(t, accessReviewStatus{})
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.wav")
	allowed, reason, err := fake.authorizer().authorize(listener, route, name)
	if err != nil || allowed {
		t.Fatalf("the review answered %v (%v)", allowed, err)
	}
	if reason == "" {
		t.Error("a refusal with no reason said nothing")
	}
}

func TestAnEvaluationErrorIsAFailureAndNotARefusal(t *testing.T) {
	fake := newAccessServer(t, accessReviewStatus{
		EvaluationError: "role.rbac.authorization.k8s.io \"reader\" not found",
	})
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.wav")
	_, _, err := fake.authorizer().authorize(listener, route, name)
	if err == nil {
		t.Fatal("an evaluation error was read as a plain refusal")
	}
	if !strings.Contains(err.Error(), "\"reader\" not found") {
		t.Errorf("the failure is %q, and it must carry the API server's own words", err)
	}
}

func TestTheScopeNamesTheGrantACallerWouldNeed(t *testing.T) {
	cases := map[string]string{
		"/v1/audio/sinks/kitchen/audio.wav": "sinks/audio",
		"/v1/audio/sinks/kitchen/audio":     "sinks/audio",
		"/v1/audio/sources/desk/audio.opus": "sources/audio",
		"/v1/audio/sinks/kitchen":           "sinks",
	}
	for path, want := range cases {
		route, _, _ := matchRoute(path)
		if got := scopeOf(route); got != want {
			t.Errorf("%s needs %q, want %q", path, got, want)
		}
	}
}

func TestTheChallengeIsRFC6750(t *testing.T) {
	if got := challenge(); got != `Bearer realm="audio-api"` {
		t.Errorf("a request with no token is challenged with %q", got)
	}
	got := invalidTokenChallenge(`[invalid bearer token, token is expired]`)
	want := `Bearer realm="audio-api", error="invalid_token", ` +
		`error_description="[invalid bearer token, token is expired]"`
	if got != want {
		t.Errorf("a refused token is challenged with %q, want %q", got, want)
	}
	got = insufficientScopeChallenge("sinks/audio")
	want = `Bearer realm="audio-api", error="insufficient_scope", scope="sinks/audio"`
	if got != want {
		t.Errorf("a refused grant is challenged with %q, want %q", got, want)
	}
}

func TestAChallengeIsOneFieldLineWhateverTheWordsAre(t *testing.T) {
	got := invalidTokenChallenge("a \"quoted\" word\r\nand a second line")
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("the challenge is more than one field line: %q", got)
	}
	if strings.Count(got, `"`)%2 != 0 {
		t.Errorf("the challenge has an unbalanced quote: %q", got)
	}
}
