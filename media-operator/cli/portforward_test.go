package main

import (
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

func TestPortForwardReportsADeadAPIServer(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, _, err := portForward(&rest.Config{Host: "https://127.0.0.1:1"}, apiServiceName+"-0")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("portForward returned no error for a dead API server address, want one")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("portForward still ran after 10 seconds against a dead API server address, want an error")
	}
}

func TestPortForwardRejectsABadHost(t *testing.T) {
	if _, _, err := portForward(&rest.Config{Host: "://no-scheme"}, apiServiceName+"-0"); err == nil {
		t.Fatal("portForward returned no error for the host \"://no-scheme\", want one")
	}
}
