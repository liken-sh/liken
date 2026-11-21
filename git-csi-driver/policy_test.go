package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAnEmptyClassResolvesToTheDefaults(t *testing.T) {
	resolved, err := parsePolicy(nil)
	if err != nil {
		t.Fatalf("parsing the policy: %v", err)
	}
	for _, c := range []struct {
		name string
		got  any
		want any
	}{
		{name: quiesceParameter, got: resolved.quiesce, want: 30 * time.Second},
		{name: maxLatencyParameter, got: resolved.maxLatency, want: 5 * time.Minute},
		{name: maxFileSizeParameter, got: resolved.maxFileSize, want: int64(1 << 20)},
		{name: authorParameter + " name", got: resolved.authorName, want: "git-csi-driver"},
		{name: authorParameter + " email", got: resolved.authorEmail, want: "git-csi-driver@liken.sh"},
		{name: metadataParameter, got: resolved.metadata, want: true},
	} {
		if c.got != c.want {
			t.Errorf("%s resolved to %v, want %v", c.name, c.got, c.want)
		}
	}
	if len(resolved.ignore) != 0 {
		t.Errorf("%s resolved to %v, want no patterns", ignoreParameter, resolved.ignore)
	}
}

func TestALongQuiesceAloneStretchesTheDefaultLatency(t *testing.T) {
	resolved, err := parsePolicy(map[string]string{quiesceParameter: "10m"})
	if err != nil {
		t.Fatalf("parsing the policy: %v", err)
	}
	if resolved.maxLatency != 10*time.Minute {
		t.Errorf("push.maxLatency resolved to %s, want 10m0s", resolved.maxLatency)
	}
}

func TestAClassResolvesEveryParameter(t *testing.T) {
	resolved, err := parsePolicy(map[string]string{
		quiesceParameter:     "10s",
		maxLatencyParameter:  "2m",
		maxFileSizeParameter: "512Ki",
		authorParameter:      "Home Assistant <homeassistant@home.example>",
		ignoreParameter:      ".storage/, *.db* ,,*.log",
		metadataParameter:    "false",
	})
	if err != nil {
		t.Fatalf("parsing the policy: %v", err)
	}
	for _, c := range []struct {
		name string
		got  any
		want any
	}{
		{name: quiesceParameter, got: resolved.quiesce, want: 10 * time.Second},
		{name: maxLatencyParameter, got: resolved.maxLatency, want: 2 * time.Minute},
		{name: maxFileSizeParameter, got: resolved.maxFileSize, want: int64(512 * 1024)},
		{name: authorParameter + " name", got: resolved.authorName, want: "Home Assistant"},
		{name: authorParameter + " email", got: resolved.authorEmail, want: "homeassistant@home.example"},
		{name: metadataParameter, got: resolved.metadata, want: false},
		{name: ignoreParameter, got: strings.Join(resolved.ignore, "|"), want: ".storage/|*.db*|*.log"},
	} {
		if c.got != c.want {
			t.Errorf("%s resolved to %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestAClassCanTurnOffTheSizeLimitAndTheMaximumLatency(t *testing.T) {
	resolved, err := parsePolicy(map[string]string{
		maxFileSizeParameter: "0",
		maxLatencyParameter:  neverLatency,
	})
	if err != nil {
		t.Fatalf("parsing the policy: %v", err)
	}
	if resolved.maxFileSize != 0 {
		t.Errorf("%s resolved to %d, want no limit", maxFileSizeParameter, resolved.maxFileSize)
	}
	if resolved.maxLatency != 0 {
		t.Errorf("%s resolved to %s, want no maximum", maxLatencyParameter, resolved.maxLatency)
	}
}

func TestAClassIsRefusedByTheFirstBadParameter(t *testing.T) {
	for _, c := range []struct {
		name        string
		parameters  map[string]string
		wantMessage string
	}{
		{
			name:        "a quiesce that is not a duration",
			parameters:  map[string]string{quiesceParameter: "soon"},
			wantMessage: `push.quiesce: "soon" is not a duration`,
		},
		{
			name:        "a quiesce under the shortest quiesce",
			parameters:  map[string]string{quiesceParameter: "1s"},
			wantMessage: "push.quiesce: 1s is shorter than 5s",
		},
		{
			name:        "a latency that is not a duration",
			parameters:  map[string]string{maxLatencyParameter: "later"},
			wantMessage: `push.maxLatency: "later" is not a duration or never`,
		},
		{
			name: "a latency shorter than the quiesce",
			parameters: map[string]string{
				quiesceParameter: "1m", maxLatencyParameter: "30s",
			},
			wantMessage: "push.maxLatency: 30s is shorter than push.quiesce 1m0s",
		},
		{
			name:        "a size that is not a quantity",
			parameters:  map[string]string{maxFileSizeParameter: "big"},
			wantMessage: `commit.maxFileSize: "big" is not a quantity`,
		},
		{
			name:        "a size under zero",
			parameters:  map[string]string{maxFileSizeParameter: "-1Mi"},
			wantMessage: `commit.maxFileSize: "-1Mi" is smaller than zero`,
		},
		{
			name:        "an author with no address",
			parameters:  map[string]string{authorParameter: "Home Assistant"},
			wantMessage: `commit.author: "Home Assistant" is not Name <email>`,
		},
		{
			name:        "an author with no name",
			parameters:  map[string]string{authorParameter: "<home@example>"},
			wantMessage: `commit.author: "<home@example>" is not Name <email>`,
		},
		{
			name:        "a metadata flag of 1",
			parameters:  map[string]string{metadataParameter: "1"},
			wantMessage: `metadata: "1" is not true or false`,
		},
		{
			name:        "an unknown parameter",
			parameters:  map[string]string{"push.eager": "true"},
			wantMessage: "push.eager: unknown parameter, want one of push.quiesce, push.maxLatency, commit.maxFileSize, commit.author, ignore, metadata",
		},
		{
			name: "the first of two bad parameters, in sorted order",
			parameters: map[string]string{
				metadataParameter: "yes", quiesceParameter: "soon",
			},
			wantMessage: `metadata: "yes" is not true or false`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			resolved, err := parsePolicy(c.parameters)
			if err == nil {
				t.Fatalf("parsePolicy returned %+v, want an InvalidArgument refusal", resolved)
			}
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Errorf("parsePolicy returned code %v, want %v", got, codes.InvalidArgument)
			}
			if got := status.Convert(err).Message(); got != c.wantMessage {
				t.Errorf("parsePolicy returned the message %q, want %q", got, c.wantMessage)
			}
		})
	}
}

func TestAuthorSetsTheAuthorAndTheCommitterOfACommit(t *testing.T) {
	resolved, err := parsePolicy(map[string]string{
		authorParameter: "Home Assistant <homeassistant@home.example>",
	})
	if err != nil {
		t.Fatalf("parsing the policy: %v", err)
	}
	want := []string{
		"GIT_AUTHOR_NAME=Home Assistant",
		"GIT_AUTHOR_EMAIL=homeassistant@home.example",
		"GIT_COMMITTER_NAME=Home Assistant",
		"GIT_COMMITTER_EMAIL=homeassistant@home.example",
	}
	if strings.Join(resolved.author(), "\n") != strings.Join(want, "\n") {
		t.Errorf("author() returned %v, want %v", resolved.author(), want)
	}
}

func TestTheIgnoreParameterAcceptsNewlines(t *testing.T) {
	resolved, err := parsePolicy(map[string]string{
		ignoreParameter: ".storage/\n*.db*, *.log\n\n",
	})
	if err != nil {
		t.Fatalf("parsing the policy: %v", err)
	}
	if want := []string{".storage/", "*.db*", "*.log"}; !slices.Equal(resolved.ignore, want) {
		t.Errorf("%s resolved to %q, want %q", ignoreParameter, resolved.ignore, want)
	}
}
