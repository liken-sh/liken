package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestEveryRouteIsInTheOpenAPIDocument(t *testing.T) {
	document := openAPIDocument(openAPIPlaceholderServer)
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatal("the document has no paths object")
	}
	if len(paths) != len(apiRoutes) {
		t.Errorf("the document has %d paths, want %d, one for each route the router serves", len(paths), len(apiRoutes))
	}
	for _, route := range apiRoutes {
		item, described := paths[route.template].(map[string]any)
		if !described {
			t.Fatalf("the document has no path item for %s", route.template)
		}
		for _, method := range []string{"get", "head", "options"} {
			if _, ok := item[method]; !ok {
				t.Errorf("%s has no %s operation", route.template, method)
			}
		}
	}
}

func TestTheDocumentEnumeratesTheExtensionPaths(t *testing.T) {
	paths := openAPIDocument(openAPIPlaceholderServer)["paths"].(map[string]any)
	for _, form := range screenForms {
		template := apiRoot + "/" + displaysPlural + "/{name}/" + screenAspect + "." + form.ext
		if _, described := paths[template]; !described {
			t.Errorf("the document has no path item for %s", template)
		}
	}
}

func TestTheDocumentHasTheServerItIsGiven(t *testing.T) {
	servers := openAPIDocument("https://display.example")["servers"].([]any)
	if got := servers[0].(map[string]any)["url"]; got != "https://display.example" {
		t.Errorf("the servers url is %v, want https://display.example", got)
	}
}

// The copy in the manual must equal the generated document, so the
// manual lists every route.
func TestTheCommittedDocumentIsTheGeneratorsOwn(t *testing.T) {
	committed, err := os.ReadFile("docs/content/docs/reference/openapi.json")
	if err != nil {
		t.Fatalf("reading the committed OpenAPI document: %v", err)
	}
	var manual, rendered map[string]any
	if err := json.Unmarshal(committed, &manual); err != nil {
		t.Fatalf("parsing the committed OpenAPI document: %v", err)
	}
	fresh, err := renderJSON(openAPIDocument(openAPIPlaceholderServer))
	if err != nil {
		t.Fatalf("rendering the OpenAPI document: %v", err)
	}
	if err := json.Unmarshal(fresh, &rendered); err != nil {
		t.Fatalf("parsing the rendered OpenAPI document: %v", err)
	}
	// A development build writes the manual's copy, with the placeholder
	// server, so servers and info.version are not compared.
	for _, document := range []map[string]any{manual, rendered} {
		delete(document, "servers")
		delete(document["info"].(map[string]any), "version")
	}
	if !reflect.DeepEqual(manual, rendered) {
		t.Error("the committed OpenAPI document differs from the document this build renders. Run make -C docs build")
	}
}

// The discovery document has the same members as the discovery
// documents of the audio and media capture APIs.
func TestTheDiscoveryDocumentHasTheSharedMembers(t *testing.T) {
	document := discovery()
	screens, ok := document.Resources[displaysPlural]
	if !ok {
		t.Fatalf("the document has resources %v, want the displays resource", document.Resources)
	}
	if screens.Self != apiRoot+"/displays/{name}" {
		t.Errorf("self is %q, want %q", screens.Self, apiRoot+"/displays/{name}")
	}
	aspect, described := screens.Aspects[screenAspect]
	if !described {
		t.Fatalf("the resource has aspects %v, want the screen aspect", screens.Aspects)
	}
	if aspect.Template != screenTemplate {
		t.Errorf("the template is %q, want %q", aspect.Template, screenTemplate)
	}
	if !reflect.DeepEqual(aspect.MediaTypes, screenMediaTypes()) {
		t.Errorf("the media types are %v, want %v", aspect.MediaTypes, screenMediaTypes())
	}
	if !reflect.DeepEqual(aspect.Extensions, []string{"png", "jpg", "mp4", "mjpeg"}) {
		t.Errorf("the extensions are %v, want png, jpg, mp4, and mjpeg", aspect.Extensions)
	}
	if aspect.Redirects {
		t.Error("the screen aspect redirects, want no redirect because the Display serves the screen itself")
	}
	if document.OpenAPI != apiRoot+"/openapi.json" {
		t.Errorf("the openapi member is %q, want %q", document.OpenAPI, apiRoot+"/openapi.json")
	}
}
