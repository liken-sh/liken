package main

import (
	"context"
	"net"
)

// dialFunc has the signature of net.Dialer.DialContext. The operator
// opens its connections to the receivers, to the UPnP description of
// each device it discovers, and for its API watches through a dialFunc.
// A test passes one that connects to its fakes in memory.
type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// listenFunc has the signature of net.Listen. The operator opens the
// listener for WiiM events through a listenFunc, and a test passes one
// that listens in memory.
type listenFunc func(network, address string) (net.Listener, error)

func dialTCP(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}
