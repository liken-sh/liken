package main

import (
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// inlineRequest is the request the kubelet sends for an inline volume,
// with the attributes a test names.
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
		t.Fatalf("parseAttributes: %v", err)
	}
	want := attributes{
		url:       "https://example.com/data.git",
		ref:       "main",
		pull:      5 * time.Minute,
		depth:     0,
		offline:   offlineRefuse,
		ephemeral: true,
		pod:       podReference{name: "reader", namespace: "home", uid: "9b1c"},
	}
	if *parsed != want {
		t.Errorf("parseAttributes answered %+v, want %+v", *parsed, want)
	}
}

func TestParseAttributesTakesEveryAttribute(t *testing.T) {
	for _, c := range []struct {
		name    string
		context map[string]string
		want    attributes
	}{
		{
			name:    "a ref of its own",
			context: map[string]string{"ref": "release"},
			want:    attributes{ref: "release", pull: 5 * time.Minute, offline: offlineRefuse},
		},
		{
			name:    "a pull of its own",
			context: map[string]string{"pull": "30s"},
			want:    attributes{ref: "main", pull: 30 * time.Second, offline: offlineRefuse},
		},
		{
			name:    "a pull of never",
			context: map[string]string{"pull": "never"},
			want:    attributes{ref: "main", pull: 0, offline: offlineRefuse},
		},
		{
			name:    "a depth of one",
			context: map[string]string{"depth": "1"},
			want:    attributes{ref: "main", pull: 5 * time.Minute, depth: 1, offline: offlineRefuse},
		},
		{
			name:    "a depth of zero",
			context: map[string]string{"depth": "0"},
			want:    attributes{ref: "main", pull: 5 * time.Minute, depth: 0, offline: offlineRefuse},
		},
		{
			name:    "a stale publish allowed",
			context: map[string]string{"offline": "allowStale"},
			want:    attributes{ref: "main", pull: 5 * time.Minute, offline: offlineAllowStale},
		},
		{
			name:    "a stale publish refused",
			context: map[string]string{"offline": "refuse"},
			want:    attributes{ref: "main", pull: 5 * time.Minute, offline: offlineRefuse},
		},
		{
			name:    "the service account the kubelet names",
			context: map[string]string{serviceAccountKey: "default"},
			want:    attributes{ref: "main", pull: 5 * time.Minute, offline: offlineRefuse},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.context["url"] = "https://example.com/data.git"
			parsed, err := parseAttributes(inlineRequest(c.context))
			if err != nil {
				t.Fatalf("parseAttributes: %v", err)
			}
			want := c.want
			want.url = "https://example.com/data.git"
			want.ephemeral = true
			want.pod = podReference{name: "reader", namespace: "home", uid: "9b1c"}
			if *parsed != want {
				t.Errorf("parseAttributes answered %+v, want %+v", *parsed, want)
			}
		})
	}
}

func TestParseAttributesRefuses(t *testing.T) {
	for _, c := range []struct {
		name    string
		request *csi.NodePublishVolumeRequest
		says    string
	}{
		{
			name:    "no url",
			request: inlineRequest(nil),
			says:    "url: an attribute the volume must set",
		},
		{
			name:    "an empty ref",
			request: inlineRequest(map[string]string{"url": "u", "ref": ""}),
			says:    "ref: an empty ref names nothing",
		},
		{
			name:    "an unknown attribute",
			request: inlineRequest(map[string]string{"url": "u", "branch": "main"}),
			says:    "branch: unknown attribute",
		},
		{
			name:    "a pull that is not a duration",
			request: inlineRequest(map[string]string{"url": "u", "pull": "often"}),
			says:    `pull: "often" is not a duration or never`,
		},
		{
			name:    "a pull of zero",
			request: inlineRequest(map[string]string{"url": "u", "pull": "0s"}),
			says:    `pull: "0s" is not longer than zero`,
		},
		{
			name:    "a pull that runs backwards",
			request: inlineRequest(map[string]string{"url": "u", "pull": "-5m"}),
			says:    `pull: "-5m" is not longer than zero`,
		},
		{
			name:    "a depth that is not a number",
			request: inlineRequest(map[string]string{"url": "u", "depth": "deep"}),
			says:    `depth: "deep" is not a whole number of commits`,
		},
		{
			name:    "a depth below zero",
			request: inlineRequest(map[string]string{"url": "u", "depth": "-1"}),
			says:    `depth: "-1" is not a whole number of commits`,
		},
		{
			name:    "an offline policy the driver does not have",
			request: inlineRequest(map[string]string{"url": "u", "offline": "cache"}),
			says:    `offline: "cache" is not refuse or allowStale`,
		},
		{
			name: "an inline volume that is not read-only",
			request: func() *csi.NodePublishVolumeRequest {
				request := inlineRequest(map[string]string{"url": "u"})
				request.Readonly = false
				return request
			}(),
			says: "readOnly: an inline volume of this driver has to be read-only",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseAttributes(c.request)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("parseAttributes answered %v, want %v", got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.says {
				t.Errorf("parseAttributes said %q, want %q", got, c.says)
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
		t.Fatalf("parseAttributes: %v", err)
	}
	if parsed.ephemeral {
		t.Error("parseAttributes marked a volume with no ephemeral attribute as inline")
	}
}

func TestParseAttributesRefusesAURLThatIsNotOne(t *testing.T) {
	for _, c := range []struct {
		name string
		url  string
		says string
	}{
		{
			name: "an option to git",
			url:  "--upload-pack=/bin/sh",
			says: `url: "--upload-pack=/bin/sh" reads as an option to git`,
		},
		{
			name: "a transport helper",
			url:  "ext::sh -c whoami",
			says: `url: "ext::sh -c whoami" names a transport helper, and the driver runs none`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseAttributes(inlineRequest(map[string]string{"url": c.url}))
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("parseAttributes answered %v, want %v", got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.says {
				t.Errorf("parseAttributes said %q, want %q", got, c.says)
			}
		})
	}
}

func TestParseAttributesTakesTheURLsGitReallyServes(t *testing.T) {
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
				t.Errorf("parseAttributes refused %q: %v", url, err)
			}
		})
	}
}

func TestParseStageAttributesRefusesAReadOnlyAttribute(t *testing.T) {
	for attribute, value := range map[string]string{
		"pull": "5m", "depth": "1", "offline": "allowStale",
	} {
		t.Run(attribute, func(t *testing.T) {
			_, err := parseStageAttributes(map[string]string{
				"url": "https://example.com/data.git", attribute: value,
			})
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("parseStageAttributes answered %v, want %v", got, codes.InvalidArgument)
			}
			want := attribute + ": a writeable volume follows its ref at stage alone"
			if got := status.Convert(err).Message(); got != want {
				t.Errorf("parseStageAttributes said %q, want %q", got, want)
			}
		})
	}
}

func TestParseStageAttributesTakesTheAttributesOfAWriteableVolume(t *testing.T) {
	parsed, err := parseStageAttributes(map[string]string{
		"url": "https://example.com/data.git",
		"ref": "release",
	})
	if err != nil {
		t.Fatalf("parseStageAttributes: %v", err)
	}
	if parsed.url != "https://example.com/data.git" || parsed.ref != "release" {
		t.Errorf("parseStageAttributes answered %+v", *parsed)
	}
	if parsed.ephemeral {
		t.Error("a staged volume is ephemeral")
	}
}
