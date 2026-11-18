package main

import (
	"net/http"
	"testing"
)

func TestLinkFieldRendersOneFieldValue(t *testing.T) {
	cases := []struct {
		name      string
		target    string
		relation  string
		mediaType string
		want      string
	}{
		{
			name:     "the screen the composition came from",
			target:   "https://display-api.liken-system.svc/v1/display/displays/boe-1080/screen.mp4?t=1,11&width=960",
			relation: relScreen,
			want: `<https://display-api.liken-system.svc/v1/display/displays/boe-1080/screen.mp4?t=1,11&width=960>; ` +
				`rel="https://liken.sh/rel/screen"`,
		},
		{
			name:     "one audio track of the composition",
			target:   "https://audio-api.liken-system.svc/v1/audio/sinks/hdmi-0-pch/audio.opus?t=1,11",
			relation: relAudio,
			want: `<https://audio-api.liken-system.svc/v1/audio/sinks/hdmi-0-pch/audio.opus?t=1,11>; ` +
				`rel="https://liken.sh/rel/audio"`,
		},
		{
			name:     "the Player on the API server",
			target:   describedByTarget("media", "studio"),
			relation: relDescribedBy,
			want: `<https://kubernetes.default.svc/apis/media.liken.sh/v1alpha1/namespaces/media/players/studio>; ` +
				`rel="describedby"`,
		},
		{
			name:      "the composed form a redirect relates to",
			target:    "/v1/media/namespaces/media/players/studio/media.mp4",
			relation:  relRelated,
			mediaType: "video/mp4",
			want:      `</v1/media/namespaces/media/players/studio/media.mp4>; rel="related"; type="video/mp4"`,
		},
		{
			name:      "a substitute for the negotiated form",
			target:    "/v1/media/namespaces/media/players/studio/media.mkv",
			relation:  relAlternate,
			mediaType: "video/matroska",
			want:      `</v1/media/namespaces/media/players/studio/media.mkv>; rel="alternate"; type="video/matroska"`,
		},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, linkField(one.target, one.relation, one.mediaType), one.want)
		})
	}
}

func TestLinkFieldsAreOneLineEach(t *testing.T) {
	header := http.Header{}

	addLink(header, "/v1/media/namespaces/media/players/studio/media.mp4", relAlternate, "video/mp4")
	addLink(header, "/v1/media/namespaces/media/players/studio/media.mkv", relAlternate, "video/matroska")
	addLink(header, describedByTarget("media", "studio"), relDescribedBy, "")

	mustMatchAll(t, header.Values("Link"), []string{
		`</v1/media/namespaces/media/players/studio/media.mp4>; rel="alternate"; type="video/mp4"`,
		`</v1/media/namespaces/media/players/studio/media.mkv>; rel="alternate"; type="video/matroska"`,
		`<https://kubernetes.default.svc/apis/media.liken.sh/v1alpha1/namespaces/media/players/studio>; rel="describedby"`,
	})
}

func TestServiceLinksAreTheTwoRFC8631Links(t *testing.T) {
	header := http.Header{}

	addServiceLinks(header)

	mustMatchAll(t, header.Values("Link"), []string{
		`</v1/media/openapi.json>; rel="service-desc"`,
		`<https://liken.sh/media/docs/reference/api/>; rel="service-doc"`,
	})
}

func TestDescribedByTargetIsAnAbsoluteURLOfThePlayer(t *testing.T) {
	cases := []struct {
		name      string
		namespace string
		player    string
		want      string
	}{
		{
			name:      "a Player in the media namespace",
			namespace: "media",
			player:    "studio",
			want:      "https://kubernetes.default.svc/apis/media.liken.sh/v1alpha1/namespaces/media/players/studio",
		},
		{
			name:      "another namespace and name",
			namespace: "lab",
			player:    "second",
			want:      "https://kubernetes.default.svc/apis/media.liken.sh/v1alpha1/namespaces/lab/players/second",
		},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, describedByTarget(one.namespace, one.player), one.want)
		})
	}
}
