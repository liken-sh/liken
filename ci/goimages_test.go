package main

import (
	"strings"
	"testing"
)

func TestAGoImageInTheFormHasNoFinding(t *testing.T) {
	findings, err := checkPins(pinsTree(t, map[string]string{
		"a/Dockerfile.go": "FROM golang AS build\n" +
			"ARG VERSION=dev\n" +
			"# A comment that says go build -o /usr/local/bin/x is not a build.\n" +
			"RUN CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w -X main.version=${VERSION}\" -o /operator . \\\n" +
			"    && CGO_ENABLED=0 go build -trimpath -tags pod -ldflags=\"-s -w\" -o /operator-pod .\n" +
			"RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \\\n" +
			"    go build -trimpath -ldflags \"-s -w\" \\\n" +
			"    -o /kubectl-liken-a ./cli\n" +
			"RUN CGO_ENABLED=0 sh /go.sh /notices .\n" +
			"FROM scratch\n" +
			"COPY --from=build /operator /operator\n" +
			"COPY --from=build /operator-pod /kubectl-liken-a /\n" +
			"COPY --from=build /notices /\n" +
			"USER 65534:65534\n" +
			"ENTRYPOINT [\"/operator\", \"run\"]\n",
		"a/Dockerfile.c": "FROM debian AS build\n" +
			"RUN make && cp daemon /usr/local/bin/daemon\n" +
			"FROM scratch\n" +
			"COPY --from=build /usr/local/bin/daemon /usr/sbin/daemon\n" +
			"ENTRYPOINT [\"/usr/sbin/daemon\"]\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Errorf("a Go image in the form is a finding:\n%s", strings.Join(findings, "\n"))
	}
}

func TestAGoImageOutOfTheFormIsFound(t *testing.T) {
	const build = "FROM golang AS build\n"
	cases := map[string]struct {
		text, wants string
	}{
		"no -trimpath": {build + "RUN CGO_ENABLED=0 go build -ldflags \"-s -w\" -o /a .\n",
			"a/Dockerfile.go:2 runs go build without -trimpath"},
		"no CGO_ENABLED=0": {build + "RUN go build -trimpath -ldflags \"-s -w\" -o /a .\n",
			"a/Dockerfile.go:2 runs go build without CGO_ENABLED=0"},
		"no -s -w": {build + "RUN CGO_ENABLED=0 go build -trimpath -o /a .\n",
			"a/Dockerfile.go:2 runs go build without -ldflags \"-s -w\""},
		"only -s": {build + "RUN CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /a .\n",
			"a/Dockerfile.go:2 runs go build without -ldflags \"-s -w\""},
		"the second of two builds": {build + "RUN CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w\" -o /a . && go build -trimpath -ldflags \"-s -w\" -o /b .\n",
			"a/Dockerfile.go:2 runs go build without CGO_ENABLED=0"},
		"no output": {build + "RUN CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w\" .\n",
			"a/Dockerfile.go:2 runs go build with no -o /<name>"},
		"an output in a directory": {build + "RUN CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w\" -o /usr/local/bin/a .\n",
			"a/Dockerfile.go:2 builds /usr/local/bin/a; build the program at /<name>"},
		"a copy to another path": {build + "RUN CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w\" -o /a .\nFROM scratch\nCOPY --from=build /a /usr/local/bin/a\n",
			"a/Dockerfile.go:4 copies /a to /usr/local/bin/a; copy it to /a"},
		"a copy into a tree": {build + "RUN CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w\" -o /a .\nFROM debian AS tree\nCOPY --from=build /a /out/usr/bin/a\n",
			"a/Dockerfile.go:4 copies /a to /out/usr/bin/a; copy it to /a"},
		"an entrypoint at another path": {build + "RUN CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w\" -o /a .\nFROM scratch\nCOPY --from=build /a /a\nENTRYPOINT [\"/usr/local/bin/a\"]\n",
			"a/Dockerfile.go:5 runs /usr/local/bin/a; the entrypoint of a Go program is /a"},
		"a stripped binary": {build + "RUN CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w\" -o /a . && upx --best /a\n",
			"a/Dockerfile.go:2 runs upx on /a; strip a Go program with -ldflags \"-s -w\""},
		"a removed section": {build + "RUN CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w\" -o /a .\nRUN objcopy --remove-section .go.buildinfo /a\n",
			"a/Dockerfile.go:3 runs objcopy on /a; strip a Go program with -ldflags \"-s -w\""},
		"a strip": {build + "RUN CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w\" -o /a .\nRUN strip /a\n",
			"a/Dockerfile.go:3 runs strip on /a; strip a Go program with -ldflags \"-s -w\""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			findings, err := checkPins(pinsTree(t, map[string]string{"a/Dockerfile.go": c.text}))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(findings, "\n"), c.wants) {
				t.Errorf("got:\n%s\nwant a finding with %q", strings.Join(findings, "\n"), c.wants)
			}
		})
	}
}
