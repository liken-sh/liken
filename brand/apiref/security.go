// The credentials: what a caller must present, and what the API does
// with it.
package main

import (
	"fmt"
	"strings"
)

// emitSecurity writes the schemes the document declares and the
// requirement that stands over every route. A route that states its
// own requirement says so in its own section.
func emitSecurity(b *strings.Builder, schemes *node, requirements *node) {
	if schemes == nil || len(schemes.members) == 0 {
		return
	}
	b.WriteString("## Security\n\n")
	if phrase := requirementPhrase(requirements); phrase != "" {
		fmt.Fprintf(b, "Every route requires %s, unless its own section states otherwise.\n\n", phrase)
	}
	b.WriteString("| Scheme | Type | Description |\n")
	b.WriteString("| --- | --- | --- |\n")
	schemes.each(func(name string, scheme *node) {
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", name, schemeType(scheme), cellText(scheme))
	})
	b.WriteString("\n")
}

// schemeType names the credential in the words a caller knows it by.
// The type member alone would say "http" where a caller needs to
// read "HTTP bearer, JWT".
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

// requirementPhrase renders a security requirement list. Each entry
// is one set of schemes a caller presents together, and a caller
// satisfies any one of the sets, so the sets are joined with "or"
// and the schemes within a set with "and". An entry with no scheme
// in it opens the route to a caller with no credential.
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
