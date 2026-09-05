package brand

import (
	"encoding/xml"
	"strings"
	"testing"
)

// The mark inlines into a page as markup, so a page is only as
// well-formed as the SVG is. This parses it the way a browser would
// have to: as one XML document with an svg root that carries its own
// namespace, because an inlined document cannot borrow one.
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

// The stylesheet inlines into every page, so an empty one is a page
// with no style, and no build would notice.
func TestStylesheetIsNotEmpty(t *testing.T) {
	if strings.TrimSpace(Stylesheet) == "" {
		t.Fatal("the stylesheet is empty")
	}
}
