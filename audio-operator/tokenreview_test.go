package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// reviewServer is a fake of the API server's TokenReview endpoint. It
// records the review it receives and responds with status.
type reviewServer struct {
	sent   tokenReview
	status tokenReviewStatus
	server *httptest.Server
}

func newReviewServer(t *testing.T, status tokenReviewStatus) *reviewServer {
	t.Helper()
	fake := &reviewServer{status: status}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != tokenReviewPath {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&fake.sent); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response := fake.sent
		response.Status = fake.status
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&response)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *reviewServer) reviewer(audience string) *reviewer {
	return newReviewer(apiclient.New(f.server.URL, f.server.Client(), ""), audience)
}

func TestAReviewSendsTheAudienceAndReturnsTheReviewedUser(t *testing.T) {
	fake := newReviewServer(t, tokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{captureAudience},
		User: tokenUserInfo{
			Username: captureCaller,
			UID:      "1f0e3dad-9990-4f65-8b2c-77b3f2a27c81",
			Groups:   []string{"system:serviceaccounts", "system:serviceaccounts:liken-system"},
			Extra:    map[string][]string{"authentication.kubernetes.io/pod-name": {"audio-api-0"}},
		},
	})
	who, err := fake.reviewer(captureAudience).review("a.b.c")
	if err != nil {
		t.Fatalf("reviewing: %v", err)
	}
	if fake.sent.Spec.Token != "a.b.c" {
		t.Errorf("the review carried the token %q, want a.b.c", fake.sent.Spec.Token)
	}
	if len(fake.sent.Spec.Audiences) != 1 || fake.sent.Spec.Audiences[0] != captureAudience {
		t.Errorf("the review requested the audiences %v, want only %s", fake.sent.Spec.Audiences, captureAudience)
	}
	if who.Username != captureCaller {
		t.Errorf("the caller is %q, want %s", who.Username, captureCaller)
	}
	if who.UID == "" || len(who.Groups) != 2 || len(who.Extra) != 1 {
		t.Errorf("the caller is %+v, want the UID, both groups, and the extra field that a SubjectAccessReview copies", who)
	}
}

func TestATokenMintedForAnotherAudienceIsRefused(t *testing.T) {
	// A real API server returns in status.audiences only the audiences
	// that spec.audiences and the token share, so for this token the
	// list is empty. The fake returns another audience, and review
	// refuses both lists because neither has audio-api.
	fake := newReviewServer(t, tokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{"https://kubernetes.default.svc"},
		User:          tokenUserInfo{Username: "system:serviceaccount:default:listener"},
	})
	_, err := fake.reviewer(apiAudience).review("a.b.c")
	if !errors.Is(err, ErrTokenDenied) {
		t.Fatalf("the review of a token for another audience returned %v, want ErrTokenDenied", err)
	}
}

func TestARefusalIncludesTheReviewsErrorText(t *testing.T) {
	fake := newReviewServer(t, tokenReviewStatus{
		Authenticated: false,
		Error:         `[invalid bearer token, token is expired by 3m12s]`,
	})
	_, err := fake.reviewer(apiAudience).review("a.b.c")
	if !errors.Is(err, ErrTokenDenied) {
		t.Fatalf("the review of a refused token returned %v, want ErrTokenDenied", err)
	}
	if got := err.Error(); !strings.Contains(got, "token is expired by 3m12s") {
		t.Errorf("the refusal is %q, want the review's own text", got)
	}
}

func TestARefusalWithNoReasonHasErrorText(t *testing.T) {
	fake := newReviewServer(t, tokenReviewStatus{Authenticated: false})
	_, err := fake.reviewer(apiAudience).review("a.b.c")
	if err == nil || err.Error() == "" {
		t.Fatalf("a refusal with no reason returned %v, want an error with text", err)
	}
}

func TestAnAcceptedTokenIsCachedAndARefusedOneIsNot(t *testing.T) {
	fake := newReviewServer(t, tokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{apiAudience},
		User:          tokenUserInfo{Username: "system:serviceaccount:liken-system:listener"},
	})
	review := fake.reviewer(apiAudience)
	for range 5 {
		if _, err := review.review("a.b.c"); err != nil {
			t.Fatalf("reviewing: %v", err)
		}
	}
	if review.reviews() != 1 {
		t.Errorf("the API server received %d reviews, want 1", review.reviews())
	}

	fake.status = tokenReviewStatus{Authenticated: false, Error: "invalid bearer token"}
	denied := fake.reviewer(apiAudience)
	for range 3 {
		if _, err := denied.review("d.e.f"); err == nil {
			t.Fatal("the review of a denied token returned no error, want the denial")
		}
	}
	if denied.reviews() != 3 {
		t.Errorf("a denied token was reviewed %d times, want 3, because a denial is never cached", denied.reviews())
	}
}

func TestACachedTokenIsReviewedAgainAfterTokenCacheMax(t *testing.T) {
	fake := newReviewServer(t, tokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{apiAudience},
		User:          tokenUserInfo{Username: "system:serviceaccount:liken-system:listener"},
	})
	review := fake.reviewer(apiAudience)
	at := time.Date(2026, 9, 16, 21, 0, 0, 0, time.UTC)
	review.now = func() time.Time { return at }

	if _, err := review.review("a.b.c"); err != nil {
		t.Fatalf("reviewing the token: %v", err)
	}
	at = at.Add(59 * time.Second)
	if _, err := review.review("a.b.c"); err != nil {
		t.Fatalf("reviewing the token 59 seconds later: %v", err)
	}
	if review.reviews() != 1 {
		t.Errorf("after 59 seconds the reviewer sent %d reviews, want 1", review.reviews())
	}
	at = at.Add(2 * time.Second)
	if _, err := review.review("a.b.c"); err != nil {
		t.Fatalf("reviewing the token 61 seconds later: %v", err)
	}
	if review.reviews() != 2 {
		t.Errorf("after 61 seconds the reviewer sent %d reviews, want 2", review.reviews())
	}
}

func TestACachedTokenIsReviewedAgainAfterItsExpClaim(t *testing.T) {
	fake := newReviewServer(t, tokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{apiAudience},
		User:          tokenUserInfo{Username: "system:serviceaccount:liken-system:listener"},
	})
	review := fake.reviewer(apiAudience)
	at := time.Date(2026, 9, 16, 21, 0, 0, 0, time.UTC)
	review.now = func() time.Time { return at }

	// The exp claim is ten seconds away, which is sooner than
	// tokenCacheMax.
	token := tokenExpiringAt(at.Add(10 * time.Second))
	if _, err := review.review(token); err != nil {
		t.Fatalf("reviewing the token: %v", err)
	}
	at = at.Add(11 * time.Second)
	if _, err := review.review(token); err != nil {
		t.Fatalf("reviewing the token after it expired: %v", err)
	}
	if review.reviews() != 2 {
		t.Errorf("after the token expired the reviewer sent %d reviews, want 2", review.reviews())
	}
}

func TestTheCacheKeyIsTheDigestAndNotTheToken(t *testing.T) {
	key := tokenKey("a.b.c")
	if len(key) != 64 {
		t.Errorf("the key is %d characters, want a 64-character SHA-256", len(key))
	}
	if strings.Contains(key, "a.b.c") {
		t.Error("the key contains the token, want only its digest")
	}
	if tokenKey("a.b.c") != key {
		t.Error("two keys of the same token differ, want them equal")
	}
	if tokenKey("a.b.d") == key {
		t.Error("two different tokens have the same key, want different keys")
	}
}

func TestBearerTokenReadsOnlyTheBearerScheme(t *testing.T) {
	cases := map[string]string{
		"Bearer a.b.c": "a.b.c",
		"bearer a.b.c": "a.b.c",
		"BEARER a.b.c": "a.b.c",
	}
	for header, want := range cases {
		token, found := bearerToken(header)
		if !found || token != want {
			t.Errorf("bearerToken(%q) = %q, %v, want %q, true", header, token, found, want)
		}
	}
	for _, header := range []string{"", "Basic dXNlcjpwYXNz", "Bearer", "Bearer ", "a.b.c"} {
		if _, found := bearerToken(header); found {
			t.Errorf("bearerToken(%q) found a token, want none", header)
		}
	}
}

// tokenExpiringAt builds a JWT whose payload has only the exp claim. Its
// signature is not valid, and tokenExpiry does not check it.
func tokenExpiringAt(at time.Time) string {
	payload, _ := json.Marshal(map[string]any{"exp": at.Unix()})
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
