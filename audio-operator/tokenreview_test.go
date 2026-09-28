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
)

// reviewServer stands in for the API server's TokenReview endpoint. It
// records what it was sent and answers what the test told it to.
type reviewServer struct {
	sent   tokenReview
	answer tokenReviewStatus
	server *httptest.Server
}

func newReviewServer(t *testing.T, answer tokenReviewStatus) *reviewServer {
	t.Helper()
	fake := &reviewServer{answer: answer}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != tokenReviewPath {
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

func (f *reviewServer) reviewer(audience string) *reviewer {
	return newReviewer(NewClient(f.server.URL, f.server.Client(), ""), audience)
}

func TestAReviewCarriesTheAudienceAndReadsTheAnswersOwn(t *testing.T) {
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
		t.Errorf("the review carried the token %q", fake.sent.Spec.Token)
	}
	if len(fake.sent.Spec.Audiences) != 1 || fake.sent.Spec.Audiences[0] != captureAudience {
		t.Errorf("the review asked for the audiences %v", fake.sent.Spec.Audiences)
	}
	if who.Username != captureCaller {
		t.Errorf("the caller is %q", who.Username)
	}
	if who.UID == "" || len(who.Groups) != 2 || len(who.Extra) != 1 {
		t.Errorf("the caller is %+v, and every field a SubjectAccessReview copies must survive", who)
	}
}

func TestATokenMintedForAnotherAudienceIsRefused(t *testing.T) {
	// The API server answers authenticated with the token's own
	// audiences, so the answer is what decides, not the ask.
	fake := newReviewServer(t, tokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{"https://kubernetes.default.svc"},
		User:          tokenUserInfo{Username: "system:serviceaccount:default:listener"},
	})
	_, err := fake.reviewer(apiAudience).review("a.b.c")
	if !errors.Is(err, ErrTokenDenied) {
		t.Fatalf("a token for another audience was accepted: %v", err)
	}
}

func TestARefusalCarriesTheReviewsOwnWords(t *testing.T) {
	fake := newReviewServer(t, tokenReviewStatus{
		Authenticated: false,
		Error:         `[invalid bearer token, token is expired by 3m12s]`,
	})
	_, err := fake.reviewer(apiAudience).review("a.b.c")
	if !errors.Is(err, ErrTokenDenied) {
		t.Fatalf("a refused token was accepted: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "token is expired by 3m12s") {
		t.Errorf("the refusal is %q, and it must carry the review's own words", got)
	}
}

func TestARefusalWithNoReasonStillSaysSomething(t *testing.T) {
	fake := newReviewServer(t, tokenReviewStatus{Authenticated: false})
	_, err := fake.reviewer(apiAudience).review("a.b.c")
	if err == nil || err.Error() == "" {
		t.Fatalf("a refusal with no reason answered %v", err)
	}
}

func TestAPositiveVerdictIsKeptAndADenialIsNot(t *testing.T) {
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
		t.Errorf("the API server was asked %d times, want 1", review.reviews())
	}

	fake.answer = tokenReviewStatus{Authenticated: false, Error: "invalid bearer token"}
	denied := fake.reviewer(apiAudience)
	for range 3 {
		if _, err := denied.review("d.e.f"); err == nil {
			t.Fatal("a denial was accepted")
		}
	}
	if denied.reviews() != 3 {
		t.Errorf("a denial was asked %d times, want 3: a denial is never cached", denied.reviews())
	}
}

func TestAVerdictLeavesTheCacheWhenItsMinuteIsUp(t *testing.T) {
	fake := newReviewServer(t, tokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{apiAudience},
		User:          tokenUserInfo{Username: "system:serviceaccount:liken-system:listener"},
	})
	review := fake.reviewer(apiAudience)
	at := time.Date(2026, 9, 16, 21, 0, 0, 0, time.UTC)
	review.now = func() time.Time { return at }

	if _, err := review.review("a.b.c"); err != nil {
		t.Fatal(err)
	}
	at = at.Add(59 * time.Second)
	if _, err := review.review("a.b.c"); err != nil {
		t.Fatal(err)
	}
	if review.reviews() != 1 {
		t.Errorf("the verdict was not kept for its minute: %d reviews", review.reviews())
	}
	at = at.Add(2 * time.Second)
	if _, err := review.review("a.b.c"); err != nil {
		t.Fatal(err)
	}
	if review.reviews() != 2 {
		t.Errorf("the verdict outlived its minute: %d reviews", review.reviews())
	}
}

func TestAVerdictNeverOutlivesItsOwnToken(t *testing.T) {
	fake := newReviewServer(t, tokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{apiAudience},
		User:          tokenUserInfo{Username: "system:serviceaccount:liken-system:listener"},
	})
	review := fake.reviewer(apiAudience)
	at := time.Date(2026, 9, 16, 21, 0, 0, 0, time.UTC)
	review.now = func() time.Time { return at }

	// A token that expires in ten seconds is kept for ten, not sixty.
	token := tokenExpiringAt(at.Add(10 * time.Second))
	if _, err := review.review(token); err != nil {
		t.Fatal(err)
	}
	at = at.Add(11 * time.Second)
	if _, err := review.review(token); err != nil {
		t.Fatal(err)
	}
	if review.reviews() != 2 {
		t.Errorf("a verdict outlived its token: %d reviews", review.reviews())
	}
}

func TestTheCacheKeyIsTheDigestAndNotTheToken(t *testing.T) {
	key := tokenKey("a.b.c")
	if len(key) != 64 {
		t.Errorf("the key is %d characters, want a 64-character SHA-256", len(key))
	}
	if strings.Contains(key, "a.b.c") {
		t.Error("the key holds the token")
	}
	if tokenKey("a.b.c") != key {
		t.Error("the key is not stable")
	}
	if tokenKey("a.b.d") == key {
		t.Error("two tokens share one key")
	}
}

func TestABearerHeaderIsReadAndNothingElseIs(t *testing.T) {
	cases := map[string]string{
		"Bearer a.b.c": "a.b.c",
		"bearer a.b.c": "a.b.c",
		"BEARER a.b.c": "a.b.c",
	}
	for header, want := range cases {
		token, found := bearerToken(header)
		if !found || token != want {
			t.Errorf("%q read as %q (%v)", header, token, found)
		}
	}
	for _, header := range []string{"", "Basic dXNlcjpwYXNz", "Bearer", "Bearer ", "a.b.c"} {
		if _, found := bearerToken(header); found {
			t.Errorf("%q was read as a bearer token", header)
		}
	}
}

// tokenExpiringAt builds a token shaped like a ServiceAccount token,
// with only the claim the cache reads. The signature is not checked
// here, because the TokenReview above already checked it.
func tokenExpiringAt(at time.Time) string {
	payload, _ := json.Marshal(map[string]any{"exp": at.Unix()})
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
