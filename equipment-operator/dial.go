package main

import (
	"context"
	"net"
)

// dialFunc opens one connection, the way net.Dialer.DialContext does.
// The operator reaches each Denon receiver through one, so a test hands
// in a dialFunc that connects to its fakes in memory.
type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// dialTCP is the dialFunc of a running operator.
func dialTCP(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}
