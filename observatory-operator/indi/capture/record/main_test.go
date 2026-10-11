package main

// The record runs Docker, which no test may do, so these tests cover
// the decisions it makes without a container. The capture package
// holds the rest, with its own tests.

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
)

// A connection that Docker's port forward closes before indiserver
// listens is dialed again. Any other failure ends the record.
func TestRefused(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"closed before the baseline", fmt.Errorf("baseline: %w", io.EOF), true},
		{"refused", &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}, true},
		{"reset", &net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}, true},
		{"no device", errors.New("the baseline names no device"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := refused(c.err); got != c.want {
				t.Errorf("refused(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestWanted(t *testing.T) {
	cases := []struct {
		name string
		only []string
		want bool
	}{
		{"every name when none is given", nil, true},
		{"a name that is given", []string{"ccd", "dome"}, true},
		{"a name that is not given", []string{"dome"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (recorder{only: c.only}).wanted("ccd"); got != c.want {
				t.Errorf("wanted(ccd) = %v, want %v", got, c.want)
			}
		})
	}
}
