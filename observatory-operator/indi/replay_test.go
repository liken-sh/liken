// The replay server: a real TCP listener that answers each message of
// a client with the bytes that indiserver sent in reply to the same
// message, as the recorder captured them in testdata/. It parses what
// the client sends with its own types, so a client that sends a wrong
// message gets no reply, and its test fails.

package indi

import (
	"encoding/xml"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A request is one element that a client sent, as the server read it.
type request struct {
	XMLName xml.Name
	Device  string `xml:"device,attr"`
	Name    string `xml:"name,attr"`
	Version string `xml:"version,attr"`
	UID     string `xml:"uid,attr"`
	Members []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:",chardata"`
	} `xml:",any"`
	Text string `xml:",chardata"`
}

// value is the trimmed value that the request gives a member.
func (r request) value(member string) (string, bool) {
	for _, m := range r.Members {
		if m.Name == member {
			return strings.TrimSpace(m.Value), true
		}
	}
	return "", false
}

// answers reports whether a recorded phase's message asks for the same
// change as the request. The client may send members that the
// recorder's message left out, so every member of the phase must be in
// the request with an equal value.
func (r request) answers(p phase) bool {
	var want request
	if err := xml.Unmarshal([]byte(p.message), &want); err != nil {
		panic(err)
	}
	if want.XMLName.Local != r.XMLName.Local || want.Device != r.Device || want.Name != r.Name {
		return false
	}
	for _, m := range want.Members {
		got, ok := r.value(m.Name)
		if !ok || !sameValue(got, strings.TrimSpace(m.Value)) {
			return false
		}
	}
	return true
}

func sameValue(a, b string) bool {
	x, errX := strconv.ParseFloat(a, 64)
	y, errY := strconv.ParseFloat(b, 64)
	if errX == nil && errY == nil {
		return x == y
	}
	return a == b
}

type replayServer struct {
	t         *testing.T
	simulator string
	device    string
	phases    []phase
	listener  net.Listener

	mutex    sync.Mutex
	conns    []net.Conn
	requests []request
	// arrived is closed and replaced each time a request arrives, so a
	// test waits for a request with no timer.
	arrived chan struct{}
	// muted stops the replies, so a test can send its own.
	muted bool
}

// startReplay serves the transcripts of one simulator until the test
// ends.
func startReplay(t *testing.T, simulator string) *replayServer {
	t.Helper()
	baseline := transcript(t, simulator, "baseline")
	device := firstDevice(t, baseline)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &replayServer{
		t: t, simulator: simulator, device: device, listener: listener,
		phases: phases(simulator, device), arrived: make(chan struct{}),
	}
	t.Cleanup(func() {
		listener.Close()
		s.drop()
	})
	go s.accept()
	return s
}

func transcript(t *testing.T, simulator, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", simulator, name+".xml"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (s *replayServer) address() string { return s.listener.Addr().String() }

func (s *replayServer) accept() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mutex.Lock()
		s.conns = append(s.conns, conn)
		s.mutex.Unlock()
		go s.serve(conn)
	}
}

func (s *replayServer) serve(conn net.Conn) {
	decoder := xml.NewDecoder(conn)
	for {
		token, err := decoder.Token()
		if err != nil {
			return
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		var r request
		if err := decoder.DecodeElement(&r, &start); err != nil {
			return
		}
		s.mutex.Lock()
		s.requests = append(s.requests, r)
		close(s.arrived)
		s.arrived = make(chan struct{})
		muted := s.muted
		s.mutex.Unlock()
		for _, p := range s.phases {
			if muted {
				break
			}
			if r.answers(p) {
				conn.Write(transcript(s.t, s.simulator, p.name))
				break
			}
		}
		if r.XMLName.Local == "pingRequest" {
			conn.Write([]byte(`<pingReply uid="` + r.UID + `"/>`))
		}
	}
}

// mute stops the replies from the transcripts.
func (s *replayServer) mute() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.muted = true
}

// send writes bytes to every open connection, for the cases that no
// simulator produces on its own.
func (s *replayServer) send(data string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, conn := range s.conns {
		conn.Write([]byte(data))
	}
}

// drop closes every open connection, as a server that stops does.
func (s *replayServer) drop() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, conn := range s.conns {
		conn.Close()
	}
	s.conns = nil
}

// received lists the elements that clients sent, in order.
func (s *replayServer) received() []request {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]request(nil), s.requests...)
}

// await returns the last request for a property, and waits up to
// testTimeout for one when none has arrived.
func (s *replayServer) await(property string) request {
	s.t.Helper()
	deadline := time.After(testTimeout)
	for {
		s.mutex.Lock()
		arrived := s.arrived
		for i := len(s.requests) - 1; i >= 0; i-- {
			if s.requests[i].Name == property {
				r := s.requests[i]
				s.mutex.Unlock()
				return r
			}
		}
		s.mutex.Unlock()
		select {
		case <-arrived:
		case <-deadline:
			s.t.Fatalf("the client sent nothing for %s", property)
		}
	}
}

// awaitElement waits up to testTimeout until clients have sent count
// elements of one tag, and returns the last of them.
func (s *replayServer) awaitElement(tag string, count int) request {
	s.t.Helper()
	deadline := time.After(testTimeout)
	for {
		s.mutex.Lock()
		arrived := s.arrived
		var found []request
		for _, r := range s.requests {
			if r.XMLName.Local == tag {
				found = append(found, r)
			}
		}
		s.mutex.Unlock()
		if len(found) >= count {
			return found[len(found)-1]
		}
		select {
		case <-arrived:
		case <-deadline:
			s.t.Fatalf("the client sent %d %s elements, want %d", len(found), tag, count)
		}
	}
}
