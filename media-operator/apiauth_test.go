package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// reviewServer is a fake API server for the review paths. It returns
// the results that a test sets and records every review it receives, so
// a test can check what the authorizer sent.
type reviewServer struct {
	tokenStatus   TokenReviewStatus
	allowed       bool
	tokenReviews  []TokenReview
	accessReviews []SubjectAccessReview
}

func (s *reviewServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case tokenReviewsPath:
		review := TokenReview{}
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.tokenReviews = append(s.tokenReviews, review)
		review.Status = s.tokenStatus
		_ = json.NewEncoder(w).Encode(review)
	case subjectAccessReviewsPath:
		review := SubjectAccessReview{}
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.accessReviews = append(s.accessReviews, review)
		review.Status = SubjectAccessReviewStatus{Allowed: s.allowed, Reason: "the test server said so"}
		_ = json.NewEncoder(w).Encode(review)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// acceptedStatus is the status a real API server returns for a
// ServiceAccount token minted for this API's audience.
func acceptedStatus() TokenReviewStatus {
	return TokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{apiAudience},
		User: TokenReviewUser{
			Username: "system:serviceaccount:media:viewer",
			UID:      "8a1b",
			Groups:   []string{"system:serviceaccounts", "system:authenticated"},
			Extra:    map[string][]string{"authentication.kubernetes.io/pod-name": {"media-api-0"}},
		},
	}
}

func newTestAuthorizer(t *testing.T, reviews *reviewServer) *authorizer {
	t.Helper()
	return newAuthorizer(testAPIClient(t, reviews))
}

func TestAPIAuthReviewsTheTokenForThisAPIsAudience(t *testing.T) {
	reviews := &reviewServer{tokenStatus: acceptedStatus()}
	auth := newTestAuthorizer(t, reviews)

	subject, err := auth.authenticate("a-minted-token")

	mustSucceed(t, err)
	mustMatch(t, subject.User, "system:serviceaccount:media:viewer")
	mustMatch(t, subject.UID, "8a1b")
	mustMatchAll(t, subject.Groups, []string{"system:serviceaccounts", "system:authenticated"})
	mustMatchAll(t, subject.Extra["authentication.kubernetes.io/pod-name"], []string{"media-api-0"})
	mustMatch(t, len(reviews.tokenReviews), 1)
	mustMatch(t, reviews.tokenReviews[0].Spec.Token, "a-minted-token")
	mustMatchAll(t, reviews.tokenReviews[0].Spec.Audiences, []string{apiAudience})
}

func TestAPIAuthRefusesATokenTheReviewDidNotAccept(t *testing.T) {
	cases := []struct {
		name   string
		status TokenReviewStatus
		words  string
	}{
		{
			name:   "the review refuses it with an error message",
			status: TokenReviewStatus{Error: "[invalid bearer token, token is expired]"},
			words:  "[invalid bearer token, token is expired]",
		},
		{
			name:   "the review refuses it with no error message",
			status: TokenReviewStatus{},
			words:  "the token review did not authenticate the token",
		},
		{
			name:   "the review returns no audiences",
			status: TokenReviewStatus{Authenticated: true},
			words:  `the audiences of the token do not include "media-api"`,
		},
		{
			name:   "the review returns another API's audience",
			status: TokenReviewStatus{Authenticated: true, Audiences: []string{"https://kubernetes.default.svc"}},
			words:  `the audiences of the token do not include "media-api"`,
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			auth := newTestAuthorizer(t, &reviewServer{tokenStatus: each.status})

			_, err := auth.authenticate("a-minted-token")

			mustFail(t, err)
			refused := unauthenticatedError{}
			if !errors.As(err, &refused) {
				t.Fatalf("authenticate returned %v, want an unauthenticatedError", err)
			}
			mustMatch(t, refused.Message, each.words)
			mustMatch(t, err.Error(), each.words)
		})
	}
}

// namedSubject returns a subject with a key and an expiry, as
// authenticate would, for a test that calls authorize without
// authenticating first.
func namedSubject(user string) captureSubject {
	return captureSubject{User: user, key: tokenKey(user), expires: time.Now().Add(verdictWindow)}
}

func TestAPIAuthReturnsTheAPIServersErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		call func(*authorizer) error
	}{
		{name: "the token review fails", call: func(a *authorizer) error {
			_, err := a.authenticate("a-minted-token")
			return err
		}},
		{name: "the access review fails", call: func(a *authorizer) error {
			_, err := a.authorize(namedSubject("alice"), "media", "studio", "media")
			return err
		}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			auth := newAuthorizer(testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `tokenreviews.authentication.k8s.io is forbidden: User "system:serviceaccount:liken-system:media-api" cannot create`)
			})))

			err := each.call(auth)

			mustFail(t, err)
			refused := unauthenticatedError{}
			if errors.As(err, &refused) {
				t.Fatalf("authenticate returned the unauthenticatedError %v, want an ordinary error", err)
			}
			if !strings.Contains(err.Error(), "is forbidden: User") {
				t.Errorf("authenticate returned %v, want the API server's error message", err)
			}
			if strings.Contains(err.Error(), "a-minted-token") {
				t.Errorf("authenticate returned %v, want no token in the error", err)
			}
		})
	}
}

func TestAPIAuthReviewsGetOnPlayersMedia(t *testing.T) {
	reviews := &reviewServer{tokenStatus: acceptedStatus(), allowed: true}
	auth := newTestAuthorizer(t, reviews)
	subject, err := auth.authenticate("a-minted-token")
	mustSucceed(t, err)

	allowed, err := auth.authorize(subject, "media", "studio", "media")

	mustSucceed(t, err)
	mustMatch(t, allowed, true)
	mustMatch(t, len(reviews.accessReviews), 1)
	asked := reviews.accessReviews[0].Spec
	mustMatch(t, asked.User, "system:serviceaccount:media:viewer")
	mustMatch(t, asked.UID, "8a1b")
	mustMatchAll(t, asked.Groups, []string{"system:serviceaccounts", "system:authenticated"})
	mustMatchAll(t, asked.Extra["authentication.kubernetes.io/pod-name"], []string{"media-api-0"})
	mustMatch(t, asked.ResourceAttributes.Group, "media.liken.sh")
	mustMatch(t, asked.ResourceAttributes.Resource, "players")
	mustMatch(t, asked.ResourceAttributes.Subresource, "media")
	mustMatch(t, asked.ResourceAttributes.Verb, "get")
	mustMatch(t, asked.ResourceAttributes.Namespace, "media")
	mustMatch(t, asked.ResourceAttributes.Name, "studio")
}

func TestAPIAuthReviewsThePlayerItselfWithNoSubresource(t *testing.T) {
	reviews := &reviewServer{tokenStatus: acceptedStatus(), allowed: true}
	auth := newTestAuthorizer(t, reviews)

	allowed, err := auth.authorize(namedSubject("alice"), "media", "studio", "")

	mustSucceed(t, err)
	mustMatch(t, allowed, true)
	mustMatch(t, reviews.accessReviews[0].Spec.ResourceAttributes.Subresource, "")
	mustMatch(t, reviews.accessReviews[0].Spec.ResourceAttributes.Resource, "players")
}

func TestAPIAuthCachesAPositiveVerdictPerPlayerAndSubresource(t *testing.T) {
	reviews := &reviewServer{tokenStatus: acceptedStatus(), allowed: true}
	auth := newTestAuthorizer(t, reviews)

	subject, err := auth.authenticate("a-minted-token")
	mustSucceed(t, err)
	_, err = auth.authenticate("a-minted-token")
	mustSucceed(t, err)
	for _, ask := range []struct{ namespace, name, subresource string }{
		{namespace: "media", name: "studio", subresource: "media"},
		{namespace: "media", name: "studio", subresource: "media"},
		{namespace: "media", name: "studio", subresource: "screen"},
		{namespace: "media", name: "kitchen", subresource: "media"},
		{namespace: "studio", name: "studio", subresource: "media"},
	} {
		allowed, err := auth.authorize(subject, ask.namespace, ask.name, ask.subresource)
		mustSucceed(t, err)
		mustMatch(t, allowed, true)
	}

	mustMatch(t, len(reviews.tokenReviews), 1)
	mustMatch(t, len(reviews.accessReviews), 4)
}

// A Role can limit access with resourceNames, so the cached verdict for
// studio must not allow kitchen in the same namespace.
func TestAPIAuthDoesNotServeOnePlayersVerdictForAnother(t *testing.T) {
	reviews := &reviewServer{tokenStatus: acceptedStatus(), allowed: true}
	auth := newTestAuthorizer(t, reviews)
	subject, err := auth.authenticate("a-minted-token")
	mustSucceed(t, err)

	allowed, err := auth.authorize(subject, "media", "studio", "media")
	mustSucceed(t, err)
	mustMatch(t, allowed, true)

	reviews.allowed = false
	refused, err := auth.authorize(subject, "media", "kitchen", "media")

	mustSucceed(t, err)
	mustMatch(t, refused, false)
	mustMatch(t, len(reviews.accessReviews), 2)
	mustMatch(t, reviews.accessReviews[1].Spec.ResourceAttributes.Name, "kitchen")
}

func TestAPIAuthNeverCachesADenial(t *testing.T) {
	reviews := &reviewServer{tokenStatus: acceptedStatus()}
	auth := newTestAuthorizer(t, reviews)

	for range 3 {
		allowed, err := auth.authorize(namedSubject("alice"), "media", "studio", "media")
		mustSucceed(t, err)
		mustMatch(t, allowed, false)
	}

	mustMatch(t, len(reviews.accessReviews), 3)
}

// signedToken returns a token in the form of a JWT, so the authorizer
// reads its exp claim. The signature is not valid, because the fake
// TokenReview does not check it.
func signedToken(t *testing.T, claims string) string {
	t.Helper()
	encode := base64.RawURLEncoding.EncodeToString
	return encode([]byte(`{"alg":"RS256"}`)) + "." + encode([]byte(claims)) + "." + encode([]byte("signature"))
}

func TestAPIAuthVerdictsExpire(t *testing.T) {
	start := time.Date(2026, 9, 16, 21, 2, 16, 0, time.UTC)
	cases := []struct {
		name    string
		token   string
		step    time.Duration
		reviews int
	}{
		{name: "an opaque token inside the minute", token: "opaque", step: 59 * time.Second, reviews: 1},
		{name: "an opaque token past the minute", token: "opaque", step: 61 * time.Second, reviews: 2},
		{
			name:    "a token whose exp is nearer than the minute",
			token:   signedToken(t, fmt.Sprintf(`{"exp":%d}`, start.Add(30*time.Second).Unix())),
			step:    31 * time.Second,
			reviews: 2,
		},
		{
			name:    "a token whose exp has not arrived",
			token:   signedToken(t, fmt.Sprintf(`{"exp":%d}`, start.Add(30*time.Second).Unix())),
			step:    29 * time.Second,
			reviews: 1,
		},
		{
			name:    "a token whose exp is further than the minute",
			token:   signedToken(t, fmt.Sprintf(`{"exp":%d}`, start.Add(10*time.Minute).Unix())),
			step:    61 * time.Second,
			reviews: 2,
		},
		{name: "a token with no exp claim", token: signedToken(t, `{"sub":"alice"}`), step: 59 * time.Second, reviews: 1},
		{name: "a token whose claims are not JSON", token: signedToken(t, "not json"), step: 59 * time.Second, reviews: 1},
		{name: "a token whose claims are not base64", token: "header.!not base64!.signature", step: 59 * time.Second, reviews: 1},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			reviews := &reviewServer{tokenStatus: acceptedStatus(), allowed: true}
			auth := newTestAuthorizer(t, reviews)
			clock := start
			auth.now = func() time.Time { return clock }

			subject, err := auth.authenticate(each.token)
			mustSucceed(t, err)
			_, err = auth.authorize(subject, "media", "studio", "media")
			mustSucceed(t, err)
			clock = clock.Add(each.step)
			subject, err = auth.authenticate(each.token)
			mustSucceed(t, err)
			_, err = auth.authorize(subject, "media", "studio", "media")
			mustSucceed(t, err)

			mustMatch(t, len(reviews.tokenReviews), each.reviews)
			mustMatch(t, len(reviews.accessReviews), each.reviews)
		})
	}
}
