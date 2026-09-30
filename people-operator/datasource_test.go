package main

import (
	"encoding/base64"
	"net/url"
	"testing"
)

// A data: URI's picture decodes from base64 or from percent-encoding,
// and a URI that holds no picture reports DecodeFailed.
func TestADataURI(t *testing.T) {
	picture := solid(t, 8, 8, green)
	cases := []struct {
		name       string
		uri        string
		wantReason string
	}{
		{"base64", "data:image/png;base64," + base64.StdEncoding.EncodeToString(picture), ""},
		{"percent-encoded", "data:image/png," + url.PathEscape(string(picture)), ""},
		{"no comma", "data:image/png;base64", reasonDecodeFailed},
		{"broken base64", "data:image/png;base64,***", reasonDecodeFailed},
		{"broken percent-encoding", "data:image/png,%zz", reasonDecodeFailed},
		{"not a picture", "data:text/plain,hello", reasonDecodeFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// url.Parse refuses the broken percent-encoding in an opaque
			// URI, so the test builds the URL itself.
			ref := &url.URL{Scheme: schemeData, Opaque: c.uri[len("data:"):]}
			got, err := dataSource{}.read(t.Context(), ref, pictureVersion{}, blue)
			if c.wantReason == "" && (err != nil || got.thumbnail == "") {
				t.Errorf("the data: URI did not bake: %v", err)
			}
			if c.wantReason != "" && reasonOf(err) != c.wantReason {
				t.Errorf("error = %v, want reason %s", err, c.wantReason)
			}
		})
	}
}
