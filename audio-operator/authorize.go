package main

// Each request is authorized with a SubjectAccessReview. A tap route
// is checked as get on the subresource audio of sinks or sources in
// the API group audio.liken.sh, so an ordinary RBAC rule grants taps,
// resourceNames limits a rule to some sinks, and the API needs no
// permission model of its own. An info route is checked as get on
// sinks or sources. The discovery and OpenAPI documents need a valid
// token and no authorization.
//
// Sinks and Sources are cluster-scoped, so the namespace is empty. The
// review copies user, groups, uid, and extra from the caller, so the
// API server applies the same rules as to the caller's own request.
//
// Every route authorizes before it looks up the named Sink or Source,
// so a 403 does not reveal whether the name exists.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const accessReviewPath = "/apis/authorization.k8s.io/v1/subjectaccessreviews"

// accessReview is both the request body and the response of the
// SubjectAccessReview API.
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

// scopeOf returns the permission a route needs, as resource or
// resource/aspect, for the scope of a 403's WWW-Authenticate header
// (RFC 6750 section 3.1).
func scopeOf(route apiRoute) string {
	if route.Kind == routeTap {
		return route.Resource + "/" + route.Aspect
	}
	return route.Resource
}

// attributesFor returns nil when the route needs no authorization.
func attributesFor(route apiRoute, name string) *resourceAttributes {
	if route.Resource == "" {
		return nil
	}
	attributes := &resourceAttributes{
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

// authorizer creates a SubjectAccessReview for every request and
// caches no result, unlike the TokenReview, so a withdrawn grant
// applies to the next request.
type authorizer struct {
	client *apiclient.Client
}

func newAuthorizer(client *apiclient.Client) *authorizer {
	return &authorizer{client: client}
}

// authorize reports whether the caller may read the route, and the API
// server's reason when it may not. It returns an error when the review
// fails, or when the API server reports an evaluation error and does
// not allow the request. A route that needs no authorization returns
// true without a review.
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

// refusalReason returns the reason for the 403's problem document, with
// a default when the API server gave none.
func refusalReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "no RBAC policy allows this request"
	}
	return reason
}

// challenge returns the WWW-Authenticate value for a 401 or a 403
// (RFC 6750 section 3). A request with no token gets the realm alone.
func challenge(parameters ...string) string {
	value := `Bearer realm="` + apiAudience + `"`
	if len(parameters) == 0 {
		return value
	}
	return value + ", " + strings.Join(parameters, ", ")
}

// invalidTokenChallenge puts the TokenReview's error text in
// error_description (RFC 6750 section 3).
func invalidTokenChallenge(words string) string {
	return challenge(`error="invalid_token"`, `error_description="`+quoted(words)+`"`)
}

// insufficientScopeChallenge names the permission the caller lacks as
// the scope (RFC 6750 section 3.1).
func insufficientScopeChallenge(scope string) string {
	return challenge(`error="insufficient_scope"`, `scope="`+quoted(scope)+`"`)
}

// quoted escapes text for a quoted-string in a header value, and
// replaces line breaks with spaces because a header cannot hold them.
func quoted(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", " ", "\n", " ")
	return replacer.Replace(text)
}
