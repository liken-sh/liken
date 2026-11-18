// This file writes the Security section: the credentials that a
// caller of the API presents.

package main

import (
	"fmt"
	"strings"
)

// emitSecurity writes the document's security schemes and its
// document-level requirement. That requirement applies to every route
// that does not set its own, and a route's own requirement is written
// in the route's section.
func emitSecurity(b *strings.Builder, schemes *node, requirements *node) {
	if schemes == nil || len(schemes.members) == 0 {
		return
	}
	b.WriteString("## Security\n\n")
	switch phrase := requirementPhrase(requirements); phrase {
	case "":
	case "no credential":
		b.WriteString("Every route accepts a caller with no credential, unless its own section states otherwise.\n\n")
	default:
		fmt.Fprintf(b, "Every route requires %s, unless its own section states otherwise.\n\n", phrase)
	}
	b.WriteString("| Scheme | Type | Description |\n")
	b.WriteString("| --- | --- | --- |\n")
	schemes.each(func(name string, scheme *node) {
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", name, schemeType(scheme), cellText(scheme))
	})
	b.WriteString("\n")
}

// schemeType describes a scheme for a reader of the page. For an HTTP
// bearer scheme that uses JWTs, the `type` member alone is "http", and
// schemeType returns "HTTP bearer, JWT".
func schemeType(scheme *node) string {
	switch scheme.member("type").value() {
	case "http":
		parts := []string{"HTTP " + scheme.member("scheme").value()}
		if format := scheme.member("bearerFormat").value(); format != "" {
			parts = append(parts, format)
		}
		return strings.Join(parts, ", ")
	case "apiKey":
		return fmt.Sprintf("API key, `%s` in the %s",
			scheme.member("name").value(), scheme.member("in").value())
	case "oauth2":
		return "OAuth 2.0"
	case "openIdConnect":
		return "OpenID Connect, " + scheme.member("openIdConnectUrl").value()
	case "mutualTLS":
		return "Mutual TLS"
	}
	return scheme.member("type").value()
}

// requirementPhrase renders a security requirement list as a phrase.
// Each entry is a set of schemes that a caller presents together, and
// presenting any one set is enough, so the phrase joins the sets with
// "or" and the schemes of a set with "and". An entry with no scheme
// means that the route accepts a caller with no credential.
func requirementPhrase(requirements *node) string {
	var sets []string
	for _, requirement := range requirements.elements() {
		var schemes []string
		requirement.each(func(name string, _ *node) {
			schemes = append(schemes, "`"+name+"`")
		})
		if len(schemes) == 0 {
			sets = append(sets, "no credential")
			continue
		}
		sets = append(sets, strings.Join(schemes, " and "))
	}
	if len(sets) == 0 {
		return ""
	}
	if len(sets) == 1 {
		return sets[0]
	}
	return strings.Join(sets[:len(sets)-1], ", ") + ", or " + sets[len(sets)-1]
}
