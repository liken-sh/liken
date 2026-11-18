package main

import (
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// inlineRequest returns the request that the kubelet sends for an
// inline volume, with the attributes that the test sets.
func inlineRequest(context map[string]string) *csi.NodePublishVolumeRequest {
	volumeContext := map[string]string{
		ephemeralKey:    "true",
		podNameKey:      "reader",
		podNamespaceKey: "home",
		podUIDKey:       "9b1c",
	}
	for key, value := range context {
		volumeContext[key] = value
	}
	return &csi.NodePublishVolumeRequest{
		VolumeId:      "csi-1",
		TargetPath:    "/var/lib/kubelet/pods/9b1c/volumes/kubernetes.io~csi/data/mount",
		Readonly:      true,
		VolumeContext: volumeContext,
	}
}

func TestParseAttributesDefaultsEverythingButTheURL(t *testing.T) {
	parsed, err := parseAttributes(inlineRequest(map[string]string{
		"url": "https://example.com/data.git",
	}))
	if err != nil {
		t.Fatalf("parsing the attributes: %v", err)
	}
	want := attributes{
		url:       "https://example.com/data.git",
		ref:       "main",
		pull:      pullEvery(5 * time.Minute),
		depth:     0,
		offline:   offlineRefuse,
		ephemeral: true,
		pod:       podReference{name: "reader", namespace: "home", uid: "9b1c"},
	}
	if *parsed != want {
		t.Errorf("parseAttributes returned %+v, want %+v", *parsed, want)
	}
}

func TestParseAttributesAcceptsEveryAttribute(t *testing.T) {
	for _, c := range []struct {
		name    string
		context map[string]string
		want    attributes
	}{
		{
			name:    "a ref of its own",
			context: map[string]string{"ref": "release"},
			want:    attributes{ref: "release", pull: pullEvery(5 * time.Minute), offline: offlineRefuse},
		},
		{
			name:    "a pull of its own",
			context: map[string]string{"pull": "30s"},
			want:    attributes{ref: "main", pull: pullEvery(30 * time.Second), offline: offlineRefuse},
		},
		{
			name:    "a pull of never",
			context: map[string]string{"pull": "never"},
			want:    attributes{ref: "main", pull: pullPolicy{mode: pullNever}, offline: offlineRefuse},
		},
		{
			name:    "a depth of one",
			context: map[string]string{"depth": "1"},
			want:    attributes{ref: "main", pull: pullEvery(5 * time.Minute), depth: 1, offline: offlineRefuse},
		},
		{
			name:    "a depth of zero",
			context: map[string]string{"depth": "0"},
			want:    attributes{ref: "main", pull: pullEvery(5 * time.Minute), depth: 0, offline: offlineRefuse},
		},
		{
			name:    "a stale publish allowed",
			context: map[string]string{"offline": "allowStale"},
			want:    attributes{ref: "main", pull: pullEvery(5 * time.Minute), offline: offlineAllowStale},
		},
		{
			name:    "a stale publish refused",
			context: map[string]string{"offline": "refuse"},
			want:    attributes{ref: "main", pull: pullEvery(5 * time.Minute), offline: offlineRefuse},
		},
		{
			name:    "the service account the kubelet names",
			context: map[string]string{serviceAccountKey: "default"},
			want:    attributes{ref: "main", pull: pullEvery(5 * time.Minute), offline: offlineRefuse},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.context["url"] = "https://example.com/data.git"
			parsed, err := parseAttributes(inlineRequest(c.context))
			if err != nil {
				t.Fatalf("parsing the attributes: %v", err)
			}
			want := c.want
			want.url = "https://example.com/data.git"
			want.ephemeral = true
			want.pod = podReference{name: "reader", namespace: "home", uid: "9b1c"}
			if *parsed != want {
				t.Errorf("parseAttributes returned %+v, want %+v", *parsed, want)
			}
		})
	}
}

func TestParseAttributesRefuses(t *testing.T) {
	for _, c := range []struct {
		name        string
		request     *csi.NodePublishVolumeRequest
		wantMessage string
	}{
		{
			name:        "no url",
			request:     inlineRequest(nil),
			wantMessage: "url: the volume sets no url",
		},
		{
			name:        "an empty ref",
			request:     inlineRequest(map[string]string{"url": "u", "ref": ""}),
			wantMessage: "ref: the value is empty",
		},
		{
			name:        "an unknown attribute",
			request:     inlineRequest(map[string]string{"url": "u", "branch": "main"}),
			wantMessage: "branch: unknown attribute, want one of url, ref, pull, depth, offline, webhookSecret",
		},
		{
			name:        "a pull that is not a duration",
			request:     inlineRequest(map[string]string{"url": "u", "pull": "often"}),
			wantMessage: `pull: "often" is not a duration, on-demand, or never`,
		},
		{
			name: "an inline volume that names a webhook Secret",
			request: inlineRequest(map[string]string{
				"url": "u", "webhookSecret": "x-webhook",
			}),
			wantMessage: "webhookSecret: an inline volume has no PersistentVolume for a webhook to mark",
		},
		{
			name:    "an inline volume that pulls on demand",
			request: inlineRequest(map[string]string{"url": "u", "pull": "on-demand"}),
			wantMessage: "pull: on-demand pulls when a demand annotates the PersistentVolume, " +
				"and an inline volume has none",
		},
		{
			name:        "a pull of zero",
			request:     inlineRequest(map[string]string{"url": "u", "pull": "0s"}),
			wantMessage: `pull: "0s" must be longer than zero`,
		},
		{
			name:        "a pull that runs backwards",
			request:     inlineRequest(map[string]string{"url": "u", "pull": "-5m"}),
			wantMessage: `pull: "-5m" must be longer than zero`,
		},
		{
			name:        "a depth that is not a number",
			request:     inlineRequest(map[string]string{"url": "u", "depth": "deep"}),
			wantMessage: `depth: "deep" is not a whole number of commits`,
		},
		{
			name:        "a depth below zero",
			request:     inlineRequest(map[string]string{"url": "u", "depth": "-1"}),
			wantMessage: `depth: "-1" is not a whole number of commits`,
		},
		{
			name:        "an offline policy the driver does not have",
			request:     inlineRequest(map[string]string{"url": "u", "offline": "cache"}),
			wantMessage: `offline: "cache" is not refuse or allowStale`,
		},
		{
			name: "an inline volume that is not read-only",
			request: func() *csi.NodePublishVolumeRequest {
				request := inlineRequest(map[string]string{"url": "u"})
				request.Readonly = false
				return request
			}(),
			wantMessage: "readOnly: an inline volume must be read-only",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseAttributes(c.request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("parseAttributes returned the code %v, want %v", got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.wantMessage {
				t.Errorf("parseAttributes message is %q, want %q", got, c.wantMessage)
			}
		})
	}
}

func TestParseAttributesMarksAPersistentVolume(t *testing.T) {
	request := inlineRequest(map[string]string{"url": "u"})
	delete(request.VolumeContext, ephemeralKey)
	request.Readonly = false
	parsed, err := parseAttributes(request)
	if err != nil {
		t.Fatalf("parsing the attributes: %v", err)
	}
	if parsed.ephemeral {
		t.Error("parseAttributes marked a volume as inline, want a volume with no ephemeral attribute to be not inline")
	}
}

func TestParseAttributesRefusesAURLThatIsNotOne(t *testing.T) {
	for _, c := range []struct {
		name        string
		url         string
		wantMessage string
	}{
		{
			name:        "an option to git",
			url:         "--upload-pack=/bin/sh",
			wantMessage: `url: "--upload-pack=/bin/sh" starts with a dash, so git reads it as an option`,
		},
		{
			name:        "a transport helper",
			url:         "ext::sh -c whoami",
			wantMessage: `url: "ext::sh -c whoami" has the form <transport>::<address>, which makes git run a transport helper, and the driver runs none`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseAttributes(inlineRequest(map[string]string{"url": c.url}))
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("parseAttributes returned the code %v, want %v", got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.wantMessage {
				t.Errorf("parseAttributes message is %q, want %q", got, c.wantMessage)
			}
		})
	}
}

func TestParseAttributesAcceptsTheURLSchemesGitFetchesFrom(t *testing.T) {
	for _, url := range []string{
		"https://example.com/data.git",
		"git://10.0.2.2:9418/hello.git",
		"git@code.example.com:home/data.git",
		"ssh://git@code.example.com:2222/home/data.git",
		"file:///srv/data.git",
		"http://[::1]:8080/data.git",
	} {
		t.Run(url, func(t *testing.T) {
			if _, err := parseAttributes(inlineRequest(map[string]string{"url": url})); err != nil {
				t.Errorf("parseAttributes refused the URL %q, want it accepted: %v", url, err)
			}
		})
	}
}

func TestParseStageAttributesRefusesAReadOnlyAttribute(t *testing.T) {
	for attribute, value := range map[string]string{
		"pull": "5m", "depth": "1", "offline": "allowStale",
		"webhookSecret": "x-webhook",
	} {
		t.Run(attribute, func(t *testing.T) {
			_, err := parseStageAttributes(writeableVolume, map[string]string{
				"url": "https://example.com/data.git", attribute: value,
			})
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("parseStageAttributes returned the code %v, want %v", got, codes.InvalidArgument)
			}
			want := attribute + ": only a read-only volume accepts this attribute"
			if got := status.Convert(err).Message(); got != want {
				t.Errorf("parseStageAttributes message is %q, want %q", got, want)
			}
		})
	}
}

func TestParseStageAttributesAcceptsPullOnDemand(t *testing.T) {
	parsed, err := parseStageAttributes(readOnlyClaim, map[string]string{
		"url":  "https://example.com/data.git",
		"pull": "on-demand",
	})
	if err != nil {
		t.Fatalf("parsing the stage attributes: %v", err)
	}
	if parsed.pull != (pullPolicy{mode: pullOnDemand}) {
		t.Errorf("parseStageAttributes returned the pull %+v, want a pull on demand", parsed.pull)
	}
	if !parsed.pull.follows() {
		t.Error("a volume that pulls on demand is in no pull loop, want it in one")
	}
	if every, timed := parsed.pull.timer(); timed {
		t.Errorf("a volume that pulls on demand has a timer of %v, want none", every)
	}
}

func TestParseStageAttributesAcceptsTheAttributesOfAWriteableVolume(t *testing.T) {
	parsed, err := parseStageAttributes(writeableVolume, map[string]string{
		"url": "https://example.com/data.git",
		"ref": "release",
	})
	if err != nil {
		t.Fatalf("parsing the stage attributes: %v", err)
	}
	if parsed.url != "https://example.com/data.git" || parsed.ref != "release" {
		t.Errorf("parseStageAttributes returned %+v, want a volume that is not ephemeral", *parsed)
	}
	if parsed.ephemeral {
		t.Error("a staged volume is ephemeral, want it not ephemeral")
	}
}

func TestParseStageAttributesAcceptsAWebhookSecret(t *testing.T) {
	parsed, err := parseStageAttributes(readOnlyClaim, map[string]string{
		"url":           "https://example.com/data.git",
		"webhookSecret": "x-webhook",
	})
	if err != nil {
		t.Fatalf("parsing the stage attributes: %v", err)
	}
	if parsed.webhookSecret != "x-webhook" {
		t.Errorf("parseStageAttributes returned the Secret %q, want x-webhook", parsed.webhookSecret)
	}
}
