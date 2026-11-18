package main

// The fake guiders run one fake PHD2 from phd2/phd2test for each guider
// pod that is Ready, as if the kubelet had started PHD2 in the pod. A
// new pod, or a pod with the same name and a new UID, starts a new PHD2
// with no equipment connected. When a pod stops, every connection to
// its PHD2 closes. When the INDI server's pod is replaced, the PHD2
// disconnects its equipment and sends an Alert, as the real PHD2 does
// when it loses its INDI connection.

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
	// server is the UID of the INDI server's pod at the last catchUp,
	// or "" while that pod is gone.
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
// both call it, so it reads the pods while it holds w.mu. Otherwise a
// call could apply an older list after a newer call, and stop the PHD2
// of a pod that still runs.
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

// phd2 returns nil when no PHD2 runs in the named guider pod.
func (w *guiderWorld) phd2(name string) *phd2test.Server {
	w.catchUp()
	w.mu.Lock()
	defer w.mu.Unlock()
	if g, ok := w.running[name]; ok {
		return g.phd2
	}
	return nil
}

func (w *guiderWorld) starts(name string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.started[name]
}

// DialContext connects to the PHD2 of the pod in address, such as
// east-guider.observatory.svc:4400, while the pod is Ready. Otherwise
// it refuses the dial, as a closed port does.
func (w *guiderWorld) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	name, _, _ := strings.Cut(address, ".")
	if s := w.phd2(name); s != nil {
		return s.DialContext(ctx, network, address)
	}
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
}

// dialers sends a dial for port 4400 to a guider's PHD2, and any other
// dial to an INDI server.
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
