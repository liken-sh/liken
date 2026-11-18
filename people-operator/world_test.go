package main

// The environment that tests run the operator in: a fake API server, a
// picture web server, and the operator's loop. Both servers use the
// in-memory connections of apiservertest, so a test runs in a synctest
// bubble and waiting for recheckInterval takes no real time.

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
)

// servedPicture is one file that the picture server serves.
type servedPicture struct {
	body   []byte
	etag   string
	status int
	// neverResponds makes the server wait until the client cancels the request,
	// and never respond.
	neverResponds bool
}

// pictureServer serves a picture for each path, and returns 304 when
// the request's If-None-Match matches the picture's ETag.
type pictureServer struct {
	server *apiservertest.Server

	mu          sync.Mutex
	pictures    map[string]servedPicture
	conditional []string // the If-None-Match of each request
}

func startPictureServer(t *testing.T) *pictureServer {
	s := &pictureServer{pictures: map[string]servedPicture{}}
	s.server = apiservertest.Start(t, s)
	return s
}

func (s *pictureServer) serve(path string, picture servedPicture) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pictures[path] = picture
}

// requests returns the If-None-Match header of each request so far.
func (s *pictureServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.conditional...)
}

func (s *pictureServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	picture, found := s.pictures[r.URL.Path]
	s.conditional = append(s.conditional, r.Header.Get("If-None-Match"))
	s.mu.Unlock()
	switch {
	case !found:
		http.NotFound(w, r)
	case picture.neverResponds:
		<-r.Context().Done()
	case picture.status != 0:
		w.WriteHeader(picture.status)
	case picture.etag != "" && r.Header.Get("If-None-Match") == picture.etag:
		w.WriteHeader(http.StatusNotModified)
	default:
		if picture.etag != "" {
			w.Header().Set("ETag", picture.etag)
		}
		_, _ = w.Write(picture.body)
	}
}

// world is one running operator and the servers it talks to.
type world struct {
	api      *fakeAPI
	pictures *pictureServer
}

// startWorld runs the operator until the test ends. The caller must be
// in a synctest bubble.
func startWorld(t *testing.T) *world {
	w := &world{api: startFakeAPI(t), pictures: startPictureServer(t)}
	client := apiclient.New(apiservertest.Host, w.api.server.Client(), "").WithContext(t.Context())
	watcher, err := dynamic.NewForConfig(w.api.server.Config())
	if err != nil {
		t.Fatalf("building the dynamic client: %v", err)
	}
	image, err := ownImage(client, operatorNS, operatorPodName)
	if err != nil {
		t.Fatalf("reading the operator's image from its own pod: %v", err)
	}
	bakers := &bakers{client: client, image: image, namespace: operatorNS}
	recorder := events.New(t.Context(), client, component, events.Options{Instance: operatorPodName, Log: io.Discard})
	operator := newOperator(client, w.pictures.server, bakers, recorder)
	done := make(chan struct{})
	go func() {
		defer close(done)
		operator.run(t.Context(), watcher)
	}()
	t.Cleanup(func() { <-done })
	w.waitForIdle()
	return w
}

// waitForIdle advances the fake clock by one second, then waits until the
// operator is idle.
func (w *world) waitForIdle() {
	time.Sleep(time.Second)
	synctest.Wait()
}

// ada returns the Person that most tests create, with the given avatar.
func ada(avatar string) person {
	p := person{Spec: personSpec{DisplayName: "Ada Lovelace", Avatar: avatar}}
	p.Metadata.Name = "ada"
	return p
}

func withAnnotation(p person, value string) person {
	p.Metadata.Annotations = map[string]string{checkAvatarAnnotation: value}
	return p
}

// solid encodes a PNG of a single colour.
func solid(t *testing.T, width, height int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, c)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatalf("encoding the PNG: %v", err)
	}
	return out.Bytes()
}

var (
	red   = color.RGBA{0xFF, 0x00, 0x00, 0xFF}
	green = color.RGBA{0x00, 0xFF, 0x00, 0xFF}
	blue  = color.RGBA{0x00, 0x00, 0xFF, 0xFF}
)

// decodeThumbnail decodes the value of status.thumbnail.
func decodeThumbnail(t *testing.T, thumbnail string) image.Image {
	t.Helper()
	payload, found := strings.CutPrefix(thumbnail, thumbnailPrefix)
	if !found {
		t.Fatalf("the thumbnail %.40q is not a JPEG data: URI", thumbnail)
	}
	encoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("decoding the base64 of the thumbnail: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decoding the JPEG of the thumbnail: %v", err)
	}
	return img
}

// centre returns the colour of the middle pixel of a thumbnail.
func centre(t *testing.T, thumbnail string) color.RGBA {
	t.Helper()
	r, g, b, _ := decodeThumbnail(t, thumbnail).At(thumbnailSide/2, thumbnailSide/2).RGBA()
	return colourOf(r, g, b)
}

// colourOf converts the 16-bit channels that color.Color.RGBA returns
// to an 8-bit colour.
func colourOf(r, g, b uint32) color.RGBA {
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0xFF}
}

// near reports whether two colours differ by no more than JPEG
// compression can change them.
func near(a, b color.RGBA) bool {
	close := func(x, y uint8) bool { return max(x, y)-min(x, y) <= 12 }
	return close(a.R, b.R) && close(a.G, b.G) && close(a.B, b.B)
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing the URL %q: %v", raw, err)
	}
	return parsed
}
