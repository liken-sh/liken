package main

// Asking the API server whether the caller may have what the route
// answers.
//
// The check is verb get on sinks/audio or sources/audio in the group
// audio.liken.sh. That subresource shape lets an ordinary RBAC rule
// grant a tap, per sink if wanted, with no vocabulary of this API's
// own. An info route checks the ordinary resource instead, and the
// discovery and OpenAPI documents need authentication and no
// authorization.
//
// The namespace is empty because both kinds are cluster-scoped. user,
// groups, uid, and extra are copied from the TokenReview's status, so
// the decision the API server makes is the one it would make for the
// caller itself.
//
// Every route authorizes before it reads, so a 403 never reveals that
// a name exists.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// accessReviewPath is where a SubjectAccessReview is created.
const accessReviewPath = "/apis/authorization.k8s.io/v1/subjectaccessreviews"

// accessReview is the object sent and the object returned.
type accessReview struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Spec       accessReviewSpec   `json:"spec"`
	Status     accessReviewStatus `json:"status,omitempty"`
}

type accessReviewSpec struct {
	ResourceAttributes *resourceAttributes `json:"resourceAttributes,omitempty"`
	User               string              `json:"user,omitempty"`
	Groups             []string            `json:"groups,omitempty"`
	UID                string              `json:"uid,omitempty"`
	Extra              map[string][]string `json:"extra,omitempty"`
}

type resourceAttributes struct {
	Namespace   string `json:"namespace"`
	Verb        string `json:"verb"`
	Group       string `json:"group"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource,omitempty"`
	Name        string `json:"name"`
}

type accessReviewStatus struct {
	Allowed         bool   `json:"allowed"`
	Denied          bool   `json:"denied"`
	Reason          string `json:"reason,omitempty"`
	EvaluationError string `json:"evaluationError,omitempty"`
}

// scopeOf names what a route needs, which is also the scope the 403's
// WWW-Authenticate header publishes (RFC 6750 section 3.1).
func scopeOf(route apiRoute) string {
	if route.Kind == routeTap {
		return route.Resource + "/" + route.Aspect
	}
	return route.Resource
}

// attributesFor builds the check one route and one name need.
func attributesFor(route apiRoute, name string) *resourceAttributes {
	if route.Resource == "" {
		return nil
	}
	attributes := &resourceAttributes{
		// Both kinds are cluster-scoped, so the namespace is empty.
		Namespace: "",
		Verb:      "get",
		Group:     EndpointGroup,
		Resource:  route.Resource,
		Name:      name,
	}
	if route.Kind == routeTap {
		attributes.Subresource = route.Aspect
	}
	return attributes
}

// authorizer asks the API server one question per request. There is
// no cache here where there is one on the token, because a grant can
// be withdrawn at any moment, and the API server's own authorizer
// caches on its side.
type authorizer struct {
	client *Client
}

func newAuthorizer(client *Client) *authorizer {
	return &authorizer{client: client}
}

// authorize answers whether the caller may have this route's answer.
// A route that needs no authorization, which is the two documents,
// answers yes with no call.
func (a *authorizer) authorize(who caller, route apiRoute, name string) (bool, string, error) {
	attributes := attributesFor(route, name)
	if attributes == nil {
		return true, "", nil
	}
	sent := accessReview{
		APIVersion: "authorization.k8s.io/v1",
		Kind:       "SubjectAccessReview",
		Spec: accessReviewSpec{
			ResourceAttributes: attributes,
			User:               who.Username,
			Groups:             who.Groups,
			UID:                who.UID,
			Extra:              who.Extra,
		},
	}
	body, err := json.Marshal(&sent)
	if err != nil {
		return false, "", err
	}
	var answer accessReview
	if err := a.client.RequestJSON("POST", accessReviewPath, body, &answer); err != nil {
		return false, "", fmt.Errorf("authorizing %s on %s: %w", who.Username, scopeOf(route), err)
	}
	if answer.Status.EvaluationError != "" && !answer.Status.Allowed {
		return false, "", fmt.Errorf("authorizing %s on %s: %s",
			who.Username, scopeOf(route), answer.Status.EvaluationError)
	}
	if answer.Status.Denied || !answer.Status.Allowed {
		return false, refusalReason(answer.Status.Reason), nil
	}
	return true, "", nil
}

// refusalReason is the authorizer's own words, or a stand-in when it
// gave none. The 403's problem document carries it.
func refusalReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "no RBAC policy allows this request"
	}
	return reason
}

// challenge is the WWW-Authenticate field a 401 or a 403 carries (RFC
// 6750 section 3). A request with no token gets the realm alone.
func challenge(parameters ...string) string {
	value := `Bearer realm="` + apiAudience + `"`
	if len(parameters) == 0 {
		return value
	}
	return value + ", " + strings.Join(parameters, ", ")
}

// invalidTokenChallenge carries the TokenReview's own words back to
// the caller, which is section 3's error_description.
func invalidTokenChallenge(words string) string {
	return challenge(`error="invalid_token"`, `error_description="`+quoted(words)+`"`)
}

// insufficientScopeChallenge names what the caller would need, which
// is section 3.1's scope.
func insufficientScopeChallenge(scope string) string {
	return challenge(`error="insufficient_scope"`, `scope="`+quoted(scope)+`"`)
}

// quoted makes text safe inside a quoted-string: a field value cannot
// carry a bare quote, a backslash, or a line break.
func quoted(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", " ", "\n", " ")
	return replacer.Replace(text)
}
