package brand

import (
	"encoding/xml"
	"strings"
	"testing"
)

// A page inlines the mark as markup, so a malformed SVG breaks the
// page. The svg root must declare its namespace itself.
func TestMarkIsAWellFormedSVGDocument(t *testing.T) {
	var root struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal([]byte(Mark), &root); err != nil {
		t.Fatalf("the mark does not parse as XML: %v", err)
	}
	if root.XMLName.Local != "svg" || root.XMLName.Space != "http://www.w3.org/2000/svg" {
		t.Fatalf("the mark's root is %s in %q, not svg in the SVG namespace", root.XMLName.Local, root.XMLName.Space)
	}
	if strings.Contains(Mark, "href") || strings.Contains(Mark, "url(") {
		t.Fatalf("the mark references something outside itself:\n%s", Mark)
	}
}

// An empty stylesheet gives unstyled pages without failing any build.
func TestStylesheetIsNotEmpty(t *testing.T) {
	if strings.TrimSpace(Stylesheet) == "" {
		t.Fatal("the stylesheet is empty")
	}
}
