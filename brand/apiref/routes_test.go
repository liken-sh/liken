package main

import (
	"strings"
	"testing"
)

// generate renders a page from an inline JSON document, for cases
// that are easier to read than a fixture file.
func generate(t *testing.T, document string) string {
	t.Helper()
	page, err := Generate([]byte(document), "x.json", Options{})
	if err != nil {
		t.Fatalf("generating the page: %v", err)
	}
	return string(page)
}

// When no response declares content, the table leaves out the media
// type and schema columns.
func TestResponsesWithNoContentGetTwoColumns(t *testing.T) {
	page := generate(t, `{"openapi":"3.1.0","info":{"title":"a","version":"v1"},"paths":{"/a":{"get":{
		"summary":"A thing","responses":{"200":{"description":"The thing."}}}}}}`)
	for _, want := range []string{
		"| Status | Description |\n",
		"| 200 | The thing. |\n",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q:\n%s", want, page)
		}
	}
}

// A response with several media types gets one row for each media
// type, so the reader sees the schema of each.
func TestEachMediaTypeIsItsOwnRow(t *testing.T) {
	page := generate(t, `{"openapi":"3.1.0","info":{"title":"a","version":"v1"},"paths":{"/a":{"get":{
		"responses":{"200":{"description":"The sound.","content":{
			"audio/flac":{"schema":{"type":"string","format":"binary"}},
			"audio/wav":{"schema":{"type":"string","format":"binary"}}}}}}}}}`)
	for _, want := range []string{
		"| 200 | `audio/flac` | string (binary) | The sound. |\n",
		"| 200 | `audio/wav` | string (binary) | The sound. |\n",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q:\n%s", want, page)
		}
	}
}

// A response with no content still gets a row when other responses of
// the operation have content.
func TestAResponseWithNoContentKeepsItsRow(t *testing.T) {
	page := generate(t, `{"openapi":"3.1.0","info":{"title":"a","version":"v1"},"paths":{"/a":{"get":{
		"responses":{
			"200":{"description":"The thing.","content":{"application/json":{"schema":{"type":"object"}}}},
			"204":{"description":"Nothing to send."}}}}}}`)
	if want := "| 204 | none | | Nothing to send. |\n"; !strings.Contains(page, want) {
		t.Errorf("the page is missing %q:\n%s", want, page)
	}
}

// OpenAPI says a path item's parameters apply to every method on the
// path, and that an operation's own parameter with the same name and
// location replaces one of them.
func TestAnOperationParameterReplacesThePathItemParameter(t *testing.T) {
	page := generate(t, `{"openapi":"3.1.0","info":{"title":"a","version":"v1"},"paths":{"/a":{
		"parameters":[
			{"name":"name","in":"path","required":true,"schema":{"type":"string"}},
			{"name":"loudness","in":"query","schema":{"type":"integer"}}],
		"get":{"parameters":[
			{"name":"loudness","in":"query","required":true,"schema":{"type":"string"}}],
			"responses":{"200":{"description":"The thing."}}}}}}`)
	for _, want := range []string{
		"| `name` | path | yes | string |  |\n",
		"| `loudness` | query | yes | string |  |\n",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "| `loudness` | query | no | integer |") {
		t.Errorf("the path item's parameter is still on the page:\n%s", page)
	}
}

// A path item member that is not a method, such as its summary or
// its parameters, is not an operation and gets no section.
func TestOnlyMethodsGetSections(t *testing.T) {
	page := generate(t, `{"openapi":"3.1.0","info":{"title":"a","version":"v1"},"paths":{"/a":{
		"summary":"The path", "description":"What it is for",
		"parameters":[{"name":"name","in":"path","required":true,"schema":{"type":"string"}}],
		"trace":{"responses":{"200":{"description":"The trace."}}}}}}`)
	if want := "## `TRACE` `/a` {data-method=TRACE}\n"; !strings.Contains(page, want) {
		t.Errorf("the page is missing %q:\n%s", want, page)
	}
	for _, unwanted := range []string{"## SUMMARY", "## PARAMETERS", "## DESCRIPTION"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("the page has a %s section:\n%s", unwanted, page)
		}
	}
}

func TestRequirementPhrase(t *testing.T) {
	for _, tc := range []struct {
		name  string
		given string
		want  string
	}{
		{"none", `[]`, ""},
		{"one scheme", `[{"bearer":[]}]`, "`bearer`"},
		{"two together", `[{"bearer":[],"certificate":[]}]`, "`bearer` and `certificate`"},
		{"either one", `[{"bearer":[]},{"certificate":[]}]`, "`bearer`, or `certificate`"},
		{"an open route", `[{}]`, "no credential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := requirementPhrase(parseSchema(t, tc.given)); got != tc.want {
				t.Errorf("the result is %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEmitSecurityStatesTheDocumentRequirement(t *testing.T) {
	schemes := parseSchema(t, `{"bearer":{"type":"http","scheme":"bearer"}}`)
	for _, tc := range []struct {
		name         string
		requirements string
		want         string
	}{
		{"a scheme", `[{"bearer":[]}]`, "Every route requires `bearer`, unless its own section states otherwise."},
		{"an open document", `[{}]`, "Every route accepts a caller with no credential, unless its own section states otherwise."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			emitSecurity(&b, schemes, parseSchema(t, tc.requirements))
			if !strings.Contains(b.String(), tc.want) {
				t.Errorf("the section does not contain %q:\n%s", tc.want, b.String())
			}
		})
	}
}

func TestSchemeType(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scheme string
		want   string
	}{
		{"bearer", `{"type":"http","scheme":"bearer","bearerFormat":"JWT"}`, "HTTP bearer, JWT"},
		{"basic", `{"type":"http","scheme":"basic"}`, "HTTP basic"},
		{"api key", `{"type":"apiKey","name":"X-Token","in":"header"}`, "API key, `X-Token` in the header"},
		{"oauth2", `{"type":"oauth2"}`, "OAuth 2.0"},
		{"openid", `{"type":"openIdConnect","openIdConnectUrl":"https://example/.well-known"}`,
			"OpenID Connect, https://example/.well-known"},
		{"mutual tls", `{"type":"mutualTLS"}`, "Mutual TLS"},
		{"a type this page does not name", `{"type":"somethingElse"}`, "somethingElse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := schemeType(parseSchema(t, tc.scheme)); got != tc.want {
				t.Errorf("the result is %q, want %q", got, tc.want)
			}
		})
	}
}

// A document with no security schemes gets no security section.
func TestADocumentWithNoSchemesHasNoSecuritySection(t *testing.T) {
	page := generate(t, `{"openapi":"3.1.0","info":{"title":"a","version":"v1"},"paths":{"/a":{"get":{
		"responses":{"200":{"description":"The thing."}}}}}}`)
	if strings.Contains(page, "## Security") {
		t.Errorf("the page has a security section:\n%s", page)
	}
	if strings.Contains(page, "## Schemas") {
		t.Errorf("the page has a schemas section:\n%s", page)
	}
}
