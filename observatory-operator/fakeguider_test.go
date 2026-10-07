package main

// The fake guiders: one fake PHD2 from phd2/phd2test for each guider
// pod that is Ready, as the kubelet would start PHD2 in it. A new pod,
// or the same name with a new UID, starts a new PHD2 with nothing
// connected, and a pod that stops ends every connection to its PHD2.
// A PHD2 whose INDI server's pod changes loses its INDI connection: its
// equipment disconnects and it shows an alert, as the real PHD2 does.

import (
	"context"
	"maps"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/liken-sh/liken/observatory-operator/phd2/phd2test"
)

type guiderWorld struct {
	api *fakeAPI

	mu      sync.Mutex
	running map[string]*fakeGuider
	// started counts the PHD2s each pod name has started.
	started map[string]int
	// prepare changes each new PHD2 before its first connection.
	prepare func(*phd2test.Server)
	// stopped runs when a guider's PHD2 stops, under no lock.
	stopped func(name string)
}

type fakeGuider struct {
	uid  string
	phd2 *phd2test.Server
	// server is the UID of the INDI server's pod that PHD2 reached
	// last, or "" while the pod is gone.
	server string
}

func startGuiderWorld(ctx context.Context, api *fakeAPI) *guiderWorld {
	w := &guiderWorld{api: api, running: map[string]*fakeGuider{}, started: map[string]int{}}
	go func() {
		for {
			w.api.mu.Lock()
			changed := w.api.changed
			w.api.mu.Unlock()
			w.catchUp()
			select {
			case <-ctx.Done():
				w.mu.Lock()
				for _, g := range w.running {
					g.phd2.Cut()
				}
				w.mu.Unlock()
				return
			case <-changed:
			}
		}
	}()
	return w
}

// catchUp starts a PHD2 for each Ready guider pod, and stops the PHD2
// of each pod that is gone or not Ready. The watch loop and each dial
// both catch up, so each one reads the pods under w.mu. A catch-up that
// read the pods before it held the lock could apply them after a newer
// one, and stop the PHD2 of a pod that still runs.
func (w *guiderWorld) catchUp() {
	var stopped []string
	w.mu.Lock()
	w.api.mu.Lock()
	pods := maps.Clone(w.api.objects[podsCollection])
	w.api.mu.Unlock()
	for name, g := range w.running {
		if p, ok := pods[name]; !ok || podUID(p) != g.uid || !podReady(p) {
			g.phd2.Cut()
			delete(w.running, name)
			stopped = append(stopped, name)
		}
	}
	for name, p := range pods {
		labels := p["metadata"].(map[string]any)["labels"].(map[string]any)
		if labels[labelRole] != roleGuider || !podReady(p) {
			continue
		}
		server := ""
		if indi, ok := pods[labels[labelServer].(string)]; ok {
			server = podUID(indi)
		}
		if g, ok := w.running[name]; ok {
			if g.server != "" && g.server != server {
				g.phd2.Set(func(s *phd2test.Server) { s.Equipment, s.AppState = false, "Stopped" })
				g.phd2.Broadcast(phd2test.Event("Alert", map[string]any{"Msg": "INDI server disconnected", "Type": "error"}))
			}
			g.server = server
			continue
		}
		g := &fakeGuider{uid: podUID(p), phd2: phd2test.New(), server: server}
		if w.prepare != nil {
			w.prepare(g.phd2)
		}
		w.running[name] = g
		w.started[name]++
	}
	hook := w.stopped
	w.mu.Unlock()
	for _, name := range stopped {
		if hook != nil {
			hook(name)
		}
	}
}

// phd2 answers the PHD2 that runs in a guider's pod now, or nil.
func (w *guiderWorld) phd2(name string) *phd2test.Server {
	w.catchUp()
	w.mu.Lock()
	defer w.mu.Unlock()
	if g, ok := w.running[name]; ok {
		return g.phd2
	}
	return nil
}

// starts answers how many PHD2s a guider's pod name has started.
func (w *guiderWorld) starts(name string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.started[name]
}

// DialContext answers a pipe to the PHD2 of the pod that an address
// names, such as east-guider.observatory.svc:4400, while the pod is
// Ready, and refuses the dial otherwise, as a closed port does.
func (w *guiderWorld) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	name, _, _ := strings.Cut(address, ".")
	if s := w.phd2(name); s != nil {
		return s.DialContext(ctx, network, address)
	}
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
}

// dialers sends a dial to a guider's PHD2 or to an INDI server, by the
// port it names.
type dialers struct {
	indi    *indiWorld
	guiders *guiderWorld
}

func (d dialers) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if strings.HasSuffix(address, ":4400") {
		return d.guiders.DialContext(ctx, network, address)
	}
	return d.indi.DialContext(ctx, network, address)
}
