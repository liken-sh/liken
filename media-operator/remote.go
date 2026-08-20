package main

// The remote sidecar reads the controller its claim delivered,
// translates each press through the compiled table, and writes mpv
// commands to the socket on the shared volume. It holds no API
// credentials and reports nothing; what it says to the world is mpv
// commands, and mpv's own state changes reach the Play's status
// through the supervisor.
//
// Every persistent failure here ends in a clean exit rather than a
// retry loop, because the kubelet already is the retry loop: a
// sidecar restarts alone, with backoff, and a fresh process reopens
// whatever nodes and sockets exist by then. A controller that
// sleeps closes its nodes, a film that ends closes the socket, and
// both look the same from here: exit, restart, wait again.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// The claim's CDI spec mounts the controller's event nodes at their
// kernel paths, and nothing tells this process which paths those
// are. So it globs the whole directory, and the capability test in
// evdev.go picks the node that carries the buttons. A variable so a
// test can point the glob at a directory of its own.
var inputNodePattern = "/dev/input/event*"

// A sleeping controller has no nodes at all, so the sidecar waits
// for them to appear. The budget bounds the wait: after five
// minutes the process exits and the kubelet starts the next wait,
// so a controller that sleeps all night costs a restart every few
// minutes instead of a process that waits forever.
var (
	nodeWaitBudget = 300 * time.Second
	nodePollDelay  = 2 * time.Second
)

// A sidecar starts before the player container, so the first dial
// waits minutes where the supervisor's waits seconds: the socket
// appears only once mpv runs. The redial budget is short because a
// redial happens mid-run, where mpv either restarted already or the
// run is over.
var (
	dialBudget   = 300 * time.Second
	redialBudget = 10 * time.Second
)

// remoteControl is the remote mode's whole process. The exit code
// is the message: 1 for a keymap the process cannot read, which a
// person must fix and should see as a crash loop, and 0 for
// everything a restart can fix by itself.
func remoteControl() {
	bindings, err := bindingsFromEnvironment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote: %v\n", err)
		os.Exit(1)
	}

	// This process is its container's PID 1, and the kernel runs no
	// default action for a signal sent to PID 1, so the kubelet's
	// SIGTERM ends the pod's grace period unanswered unless it is
	// handled here.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := driveRemote(ctx, bindings); err != nil {
		fmt.Fprintf(os.Stderr, "remote: %v\n", err)
	}
	os.Exit(0)
}

// bindingsFromEnvironment reads the table the operator compiled.
// The operator already turned every name into a number and refused
// what it could not, so a value that fails here is a build defect
// or a hand-edited pod, and both deserve the crash loop.
func bindingsFromEnvironment() ([]compiledBinding, error) {
	value := os.Getenv(keymapVariable)
	if value == "" {
		return nil, fmt.Errorf("%s is unset; the operator sets it on every remote sidecar", keymapVariable)
	}
	var bindings []compiledBinding
	if err := json.Unmarshal([]byte(value), &bindings); err != nil {
		return nil, fmt.Errorf("%s does not decode as a compiled keymap: %w", keymapVariable, err)
	}
	if len(bindings) == 0 {
		return nil, fmt.Errorf("%s carries no bindings", keymapVariable)
	}
	return bindings, nil
}

// driveRemote is the run's order: find the controller, then dial
// the player, then translate until something ends. The nodes come
// first because they are the scarcer thing: a controller that never
// wakes makes the socket useless, while a film usually starts
// within the dial budget.
func driveRemote(ctx context.Context, bindings []compiledBinding) error {
	nodes, err := awaitNodes(ctx, bindings)
	if err != nil {
		return err
	}

	defer closeNodes(nodes)

	reading, stopReading := context.WithCancel(ctx)
	defer stopReading()
	// A reader blocked in read(2) ends only when its file closes,
	// so the context's end must close the nodes to unblock it.
	defer context.AfterFunc(reading, func() { closeNodes(nodes) })()

	events := readNodes(reading, nodes, stopReading)

	link, err := dialPlayer(reading, dialBudget)
	if err != nil {
		return err
	}
	return translateEvents(reading, bindings, events, link, func(ctx context.Context) (io.ReadWriteCloser, error) {
		return dialPlayer(ctx, redialBudget)
	})
}

// translateEvents is the working loop: match each event against the
// table, translate the match, write the command. A send that fails
// earns exactly one redial, because one failure is mpv restarting
// under the same Play, and a second failure in a row is the run
// ending, where the right move is to exit and let the next process
// wait for the next socket.
func translateEvents(
	ctx context.Context,
	bindings []compiledBinding,
	events <-chan inputEvent,
	link io.ReadWriteCloser,
	redial func(context.Context) (io.ReadWriteCloser, error),
) error {
	defer func() { link.Close() }()
	// mpv writes replies and events to every IPC client whether or
	// not it asked, and a client that stops reading fills the socket
	// buffer until mpv itself blocks on the write. The reader's only
	// job is to keep that from happening.
	go discardReplies(link)

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, open := <-events:
			if !open {
				return nil
			}
			binding, matched := matchBinding(bindings, event)
			if !matched {
				continue
			}
			command := commandFor(binding)
			if command == nil {
				continue
			}
			if err := sendCommand(link, command); err == nil {
				continue
			}
			replacement, err := redial(ctx)
			if err != nil {
				return err
			}
			link.Close()
			link = replacement
			go discardReplies(link)
			if err := sendCommand(link, command); err != nil {
				return err
			}
		}
	}
}

// matchBinding compares type, code, and value, all three exactly.
// The exactness is the debounce: a key's autorepeat arrives as
// value 2 and its release as 0, a hat's return to center as 0, and
// none of them equals the 1, -1, or 1 a binding states, so a held
// button fires once.
func matchBinding(bindings []compiledBinding, event inputEvent) (compiledBinding, bool) {
	for _, binding := range bindings {
		if binding.EventType == event.Type && binding.Code == event.Code && binding.Value == event.Value {
			return binding, true
		}
	}
	return compiledBinding{}, false
}

// commandFor is where the action vocabulary becomes mpv's words,
// and the only place in the system that holds both. The osd-auto
// prefix makes mpv show each press on the screen, which is the
// viewer's proof the controller works. An action this build has no
// case for sends nothing, so a newer operator's keymap degrades to
// fewer buttons rather than a crash.
func commandFor(binding compiledBinding) []any {
	switch binding.Action {
	case actionPause:
		return []any{"osd-auto", "cycle", "pause"}
	case actionMute:
		return []any{"osd-auto", "cycle", "mute"}
	case actionSeek:
		return []any{"osd-auto", "seek", binding.Amount}
	case actionVolume:
		return []any{"osd-auto", "add", "volume", binding.Amount}
	case actionChapter:
		return []any{"osd-auto", "add", "chapter", binding.Amount}
	case actionSubtitles:
		return []any{"osd-auto", "cycle", "sub"}
	case actionAudio:
		return []any{"osd-auto", "cycle", "audio"}
	case actionInfo:
		return []any{"expand-properties", "show-text", "${filename}\n${time-pos} / ${duration}", 4000}
	}
	return nil
}

// sendCommand writes one newline-delimited JSON command, the same
// shape the supervisor writes on its own connection.
func sendCommand(writer io.Writer, command []any) error {
	return json.NewEncoder(writer).Encode(mpvCommand{Command: command})
}

func discardReplies(link io.Reader) {
	_, _ = io.Copy(io.Discard, link)
}

// awaitNodes polls for a node that carries a bound button. The
// nodes appear when the controller connects, which can be minutes
// after this container starts, and vanish again when it sleeps, so
// an empty directory is ordinary and only the budget ends the wait.
func awaitNodes(ctx context.Context, bindings []compiledBinding) ([]*os.File, error) {
	deadline := time.Now().Add(nodeWaitBudget)
	for {
		nodes := matchingNodes(bindings)
		if len(nodes) > 0 {
			return nodes, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("no input node reported a bound button or axis within %s", nodeWaitBudget)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(nodePollDelay):
		}
	}
}

// matchingNodes opens every event node and keeps the ones that pass
// the capability test. The open is syscall.Open because the ioctl
// in deviceMatches needs the raw descriptor before any *os.File
// exists. A node that will not open or answer is skipped silently:
// the directory holds every input device on the machine, and most
// of them are not this claim's to read.
func matchingNodes(bindings []compiledBinding) []*os.File {
	paths, err := filepath.Glob(inputNodePattern)
	if err != nil {
		return nil
	}
	var nodes []*os.File
	for _, path := range paths {
		descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		if !deviceMatches(descriptor, bindings) {
			syscall.Close(descriptor)
			continue
		}
		nodes = append(nodes, os.NewFile(uintptr(descriptor), path))
	}
	return nodes
}

func closeNodes(nodes []*os.File) {
	for _, node := range nodes {
		node.Close()
	}
}

// readNodes starts one reader per matched node and merges their
// events into one channel. The first reader to end cancels the
// whole run, because a vanished node means the controller slept and
// the clean restart is how fresh nodes are found.
func readNodes(ctx context.Context, nodes []*os.File, ended context.CancelFunc) <-chan inputEvent {
	events := make(chan inputEvent, 64)
	var reading sync.WaitGroup
	for _, node := range nodes {
		reading.Add(1)
		go func() {
			defer reading.Done()
			defer ended()
			readNode(ctx, node, events)
		}()
	}
	go func() {
		reading.Wait()
		close(events)
	}()
	return events
}

// readNode delivers one node's events until the node ends. A read
// error is not retried and the node is not reopened, because the
// error means the controller disconnected and its nodes are gone;
// the next process finds the new ones.
func readNode(ctx context.Context, node *os.File, events chan<- inputEvent) {
	buffer := make([]byte, inputEventSize*64)
	for {
		read, err := node.Read(buffer)
		if err != nil {
			return
		}
		for _, event := range parseInputEvents(buffer[:read]) {
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}
}

// dialPlayer wraps dialMPV's short attempt loop in a budget of
// time. The supervisor's own budget answers "did mpv start", a
// question of seconds; this one answers "has the film started yet",
// a question of minutes, and a time budget states that directly.
func dialPlayer(ctx context.Context, budget time.Duration) (io.ReadWriteCloser, error) {
	ctx, stop := context.WithTimeout(ctx, budget)
	defer stop()

	var lastError error
	for ctx.Err() == nil {
		link, err := dialMPV(ctx, mpvSocketPath)
		if err == nil {
			return link, nil
		}
		lastError = err
	}
	if lastError == nil {
		lastError = ctx.Err()
	}
	return nil, fmt.Errorf("mpv served no socket at %s within %s: %w", mpvSocketPath, budget, lastError)
}
