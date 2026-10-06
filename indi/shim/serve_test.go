package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The test binary runs again as a fake indiserver when its first
// argument is fakeServerArg. The fake reads the fifo that -f names and
// prints each line it reads, so a test reads the commands that a real
// indiserver would act on. With the mode "exit", it exits at once, as
// a crashed indiserver does.
const fakeServerArg = "fake-indiserver"

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == fakeServerArg {
		os.Exit(fakeIndiserver(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

func fakeIndiserver(mode string, args []string) int {
	if mode == "exit" {
		return 3
	}
	i := slices.Index(args, "-f")
	if i < 0 || i+1 >= len(args) {
		fmt.Println("no -f")
		return 2
	}
	fifo, err := os.Open(args[i+1])
	if err != nil {
		fmt.Println(err)
		return 1
	}
	scanner := bufio.NewScanner(fifo)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	return 0
}

// waitLimit bounds every wait on the shim and its fake server, which
// are real processes outside a synctest bubble.
const waitLimit = 10 * time.Second

// pod is one server's files as the server's pod mounts them.
type pod struct {
	t           *testing.T
	volume      string
	links, fifo string
	self        string
	generation  int
}

func newPod(t *testing.T) *pod {
	run := t.TempDir()
	p := &pod{
		t:      t,
		volume: filepath.Join(run, "devices"),
		links:  filepath.Join(run, "drivers"),
		fifo:   filepath.Join(run, "indiserver.fifo"),
		self:   "/usr/bin/indi-shim",
	}
	for _, dir := range []string{p.volume, p.links} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// publish writes the drivers file the way the kubelet writes a
// downward API volume: the file in a new directory, a link ..data_tmp
// to that directory, and a rename of ..data_tmp over ..data. The file
// that the shim reads is a link through ..data.
func (p *pod) publish(lines ...string) {
	p.t.Helper()
	p.generation++
	stamp := fmt.Sprintf("..2026_10_06_12_00_00.%d", p.generation)
	if err := os.Mkdir(filepath.Join(p.volume, stamp), 0o755); err != nil {
		p.t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(p.volume, stamp, "drivers"), []byte(body), 0o644); err != nil {
		p.t.Fatal(err)
	}
	if err := os.Symlink(stamp, filepath.Join(p.volume, "..data_tmp")); err != nil {
		p.t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(p.volume, "..data_tmp"), filepath.Join(p.volume, "..data")); err != nil {
		p.t.Fatal(err)
	}
	if p.generation == 1 {
		if err := os.Symlink("..data/drivers", filepath.Join(p.volume, "drivers")); err != nil {
			p.t.Fatal(err)
		}
	}
}

// server is a running serve and the lines its fake indiserver read.
// ended closes when serve returns, and err holds what it returned.
type server struct {
	lines <-chan string
	ended <-chan struct{}
	err   error
}

// serve runs the shim's serve mode with a fake indiserver until the
// test ends.
func (p *pod) serve(mode string) *server {
	ctx, cancel := context.WithCancel(context.Background())
	out, in := io.Pipe()
	lines := make(chan string, 100)
	go func() {
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	ended := make(chan struct{})
	s := &server{lines: lines, ended: ended}
	go func() {
		s.err = serve(ctx, serveConfig{
			self: p.self, drivers: filepath.Join(p.volume, "drivers"), links: p.links, fifo: p.fifo,
			server: []string{os.Args[0], fakeServerArg, mode}, output: in,
		})
		in.Close()
		close(ended)
	}()
	p.t.Cleanup(func() {
		cancel()
		select {
		case <-ended:
		case <-time.After(waitLimit):
			p.t.Error("serve did not end with its context")
		}
	})
	return s
}

// commands reads the next n commands that indiserver read from the
// fifo. The shim logs each command too, with a prefix, and the reader
// skips those lines.
func (s *server) commands(t *testing.T, n int) []string {
	t.Helper()
	var got []string
	deadline := time.After(waitLimit)
	for len(got) < n {
		select {
		case line := <-s.lines:
			if len(line) > 0 && line[0] != 'i' {
				got = append(got, line)
			}
		case <-deadline:
			t.Fatalf("indiserver read %q, want %d commands", got, n)
		}
	}
	return got
}

// linked answers the links in the links directory, by name, and fails
// on a link to anything but the shim.
func (p *pod) linked() []string {
	p.t.Helper()
	entries, err := os.ReadDir(p.links)
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if to, err := os.Readlink(filepath.Join(p.links, e.Name())); err != nil || to != p.self {
			p.t.Errorf("%s links to %q, %v", e.Name(), to, err)
		}
		out = append(out, e.Name())
	}
	return out
}

func TestTheServerStartsEachDriverOfTheFile(t *testing.T) {
	p := newPod(t)
	p.publish("mount.observatory:7625", "ccd.observatory:7625")
	s := p.serve("echo")

	want := []string{"start " + p.links + "/mount.observatory:7625", "start " + p.links + "/ccd.observatory:7625"}
	if got := s.commands(t, 2); !slices.Equal(got, want) {
		t.Errorf("indiserver read %q, want %q", got, want)
	}
	if got := p.linked(); !slices.Equal(got, []string{"ccd.observatory:7625", "mount.observatory:7625"}) {
		t.Errorf("links = %q", got)
	}
}

// A device that leaves is stopped and a device that joins is started,
// and the server runs on: the fake indiserver is the same process that
// read the first commands.
func TestAChangedFileStopsAndStartsOnlyTheDriversThatChanged(t *testing.T) {
	p := newPod(t)
	p.publish("mount.observatory:7625", "focuser.observatory:7625")
	s := p.serve("echo")
	s.commands(t, 2)

	p.publish("mount.observatory:7625", "ccd.observatory:7625")

	want := []string{"stop " + p.links + "/focuser.observatory:7625", "start " + p.links + "/ccd.observatory:7625"}
	if got := s.commands(t, 2); !slices.Equal(got, want) {
		t.Errorf("indiserver read %q, want %q", got, want)
	}
	if got := p.linked(); !slices.Equal(got, []string{"ccd.observatory:7625", "mount.observatory:7625"}) {
		t.Errorf("links = %q", got)
	}
}

// The links directory is an emptyDir, which keeps the links of the
// container that ran before a restart.
func TestALinkFromAnEarlierContainerIsRemoved(t *testing.T) {
	p := newPod(t)
	if err := os.Symlink(p.self, filepath.Join(p.links, "gone.observatory:7625")); err != nil {
		t.Fatal(err)
	}
	p.publish("mount.observatory:7625")
	s := p.serve("echo")
	s.commands(t, 1)
	if got := p.linked(); !slices.Equal(got, []string{"mount.observatory:7625"}) {
		t.Errorf("links = %q", got)
	}
}

// A file that names no device starts a server with no drivers, and the
// first device that joins starts on it.
func TestAServerWithNoDevicesStartsTheFirstOneThatJoins(t *testing.T) {
	p := newPod(t)
	p.publish()
	s := p.serve("echo")
	p.publish("dome.observatory:7625")
	want := []string{"start " + p.links + "/dome.observatory:7625"}
	if got := s.commands(t, 1); !slices.Equal(got, want) {
		t.Errorf("indiserver read %q, want %q", got, want)
	}
}

// The kubelet starts the container again when serve ends, and the new
// indiserver starts with every driver of the file.
func TestServeEndsWhenIndiserverExits(t *testing.T) {
	p := newPod(t)
	p.publish("mount.observatory:7625")
	s := p.serve("exit")
	select {
	case <-s.ended:
		if s.err == nil {
			t.Error("serve ended with no error")
		}
	case <-time.After(waitLimit):
		t.Fatal("serve kept running after indiserver exited")
	}
}

func TestTheDriversFileListsOneAddressOnEachLine(t *testing.T) {
	cases := map[string]struct {
		body string
		want []string
	}{
		"two devices":      {"mount:7625\nccd:7625\n", []string{"mount:7625", "ccd:7625"}},
		"no final newline": {"mount:7625", []string{"mount:7625"}},
		"blank lines":      {"\nmount:7625\n\n", []string{"mount:7625"}},
		"empty":            {"", nil},
		"a line twice":     {"mount:7625\nmount:7625\n", []string{"mount:7625"}},
		"a bad line":       {"mount:7625\nmount\n", []string{"mount:7625"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "drivers")
			if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := readDrivers(path, io.Discard); !slices.Equal(got, c.want) {
				t.Errorf("readDrivers = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAMissingDriversFileIsAnEmptyList(t *testing.T) {
	if got := readDrivers(filepath.Join(t.TempDir(), "drivers"), io.Discard); got != nil {
		t.Errorf("readDrivers = %q", got)
	}
}
