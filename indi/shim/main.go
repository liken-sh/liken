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
// The pod that runs indiserver has no shell, so the shim also makes the
// links: indi-shim link <dir> <host:port>... writes one link in <dir>
// for each device, and an init container runs it.
//
// The shim exits when either side closes, and indiserver restarts it.
// indiserver then does what a restarted driver needs: it sends
// delProperty for the device to every client, and getProperties to
// the new driver. So the shim parses no INDI message and keeps no
// state.
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
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
	if len(os.Args) > 2 && os.Args[1] == "link" {
		self, err := os.Executable()
		if err == nil {
			err = link(self, os.Args[2], os.Args[3:])
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
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

// link writes one link to the shim in dir for each device address.
func link(self, dir string, addresses []string) error {
	for _, address := range addresses {
		if _, err := target(address); err != nil {
			return err
		}
		if err := os.Symlink(self, filepath.Join(dir, address)); err != nil {
			return err
		}
	}
	return nil
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
