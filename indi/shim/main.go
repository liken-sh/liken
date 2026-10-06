// Command indi-shim connects indiserver to an INDI driver that runs in
// another pod.
//
// indiserver starts each driver as a child process and speaks INDI
// with it over the child's stdin and stdout. It starts the child with
// execlp and no arguments, so a driver in another pod needs a local
// program that stands in for it. Each device is a symbolic link to
// this one binary, named for the address of the device pod's Service,
// such as /run/indi/drivers/ccd.observatory:7625, and indiserver
// starts the link. The shim reads the address from its own name,
// connects, and copies bytes in both directions.
//
// In the server's pod, the shim also runs indiserver, and makes the
// links and starts and stops the drivers on the running server:
//
//	indi-shim serve <drivers file> <links dir> <fifo> <indiserver> [arg...]
//
// serve.go gives the design.
//
// The shim exits when either side closes, and indiserver restarts it.
// indiserver then does what a restarted driver needs: it sends
// delProperty for the device to every client, and getProperties to
// the new driver. So the shim parses no INDI message and keeps no
// state.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// While the device pod is not up, its Service refuses the connection.
// The shim tries again after retryInterval. Without the wait, a shim
// whose pod is down would exit at once, indiserver would restart it at
// once, and the two would loop as fast as the CPU allows. After
// giveUpAfter the shim exits, and each such exit spends one of
// indiserver's restarts (-r), which never refill. At one exit a minute,
// a server started with -r 2147483647 runs out after about 4,000
// years.
const (
	retryInterval = time.Second
	giveUpAfter   = time.Minute
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if len(os.Args) < 6 {
			fmt.Fprintln(os.Stderr, "usage: indi-shim serve <drivers file> <links dir> <fifo> <indiserver> [arg...]")
			os.Exit(2)
		}
		// The shim is process 1 of the server's container. The kernel
		// ignores a signal that process 1 does not handle, so the shim
		// handles SIGTERM, and the context's end kills indiserver.
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		defer stop()
		self, err := os.Executable()
		if err == nil {
			err = serve(ctx, serveConfig{
				self: self, drivers: os.Args[2], links: os.Args[3], fifo: os.Args[4],
				server: os.Args[5:], output: os.Stderr,
			})
		}
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	address, err := target(os.Args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	conn, err := dial(address, retryInterval, giveUpAfter)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	relay(os.Stdin, os.Stdout, conn)
}

// target reads the device's address from the name the shim runs
// under.
func target(argv0 string) (string, error) {
	name := filepath.Base(argv0)
	host, port, err := net.SplitHostPort(name)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("indi-shim runs as a link named host:port for the device it connects to, not as %q", name)
	}
	return name, nil
}

// dial connects to the device, and tries again every interval until
// the deadline.
func dial(address string, interval, deadline time.Duration) (net.Conn, error) {
	end := time.Now().Add(deadline)
	for {
		conn, err := net.DialTimeout("tcp", address, interval)
		if err == nil {
			return conn, nil
		}
		if time.Now().Add(interval).After(end) {
			time.Sleep(time.Until(end))
			return nil, fmt.Errorf("no device answered at %s for %v: %w", address, deadline, err)
		}
		time.Sleep(interval)
	}
}

// relay copies the server's messages to the device and the device's
// messages to the server, and returns when either side closes.
func relay(fromServer io.Reader, toServer io.Writer, device net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(device, fromServer)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(toServer, device)
		done <- struct{}{}
	}()
	<-done
	device.Close()
}
