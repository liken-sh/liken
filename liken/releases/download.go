package releases

// This file bounds how long a download from a release channel can
// stall. A machine's operator downloads releases onto a boot slot,
// and a workstation downloads them to build install media. Both read
// the same documents and artifacts through Get.
//
// net/http bounds the dial and the TLS handshake, but nothing bounds
// the wait for the response headers or for the next byte of the body.
// A server that accepts the request and then stops sending keeps the
// connection open, and the download waits on it forever. On a machine
// this holds the operator's one release writer, so every later
// upgrade waits too. So Get cancels a request when no byte arrives
// for StallLimit, from the request to the headers and between any two
// reads of the body.
//
// Get bounds progress, not the whole transfer. An artifact is a few
// hundred megabytes, and a machine on a slow link takes as long as it
// takes. A deadline for the whole transfer would fail a download that
// makes steady progress, and it would retry from the start of the
// artifact each time.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// StallLimit is how long a download can wait for its next byte. A
// minute covers a busy server that is slow to answer and the
// retransmissions of a lossy link. A link that delivers nothing for a
// minute is broken, and a new request is the only way past a server
// that stopped sending.
const StallLimit = time.Minute

// errStalled is the cause of a request that Get cancelled because no
// byte arrived for StallLimit.
var errStalled = fmt.Errorf("no bytes arrived for %v", StallLimit)

// Get sends a GET for url through client and returns the response
// when the server answers 200. The request ends when ctx ends, or
// when no byte arrives for StallLimit. The caller closes the body.
func Get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	stall := time.AfterFunc(StallLimit, func() { cancel(errStalled) })
	stop := func() {
		stall.Stop()
		cancel(nil)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		stop()
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		stop()
		return nil, causeOf(ctx, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		stop()
		return nil, fmt.Errorf("the server answered %s", resp.Status)
	}
	stall.Reset(StallLimit)
	resp.Body = &progressBody{body: resp.Body, ctx: ctx, stall: stall, stop: stop}
	return resp, nil
}

// causeOf reports why a request failed. A request that Get or the
// caller cancelled fails with the transport's "context canceled", and
// the cause says which of the two happened.
func causeOf(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil && !errors.Is(err, cause) {
		return fmt.Errorf("%w (%v)", cause, err)
	}
	return err
}

// progressBody restarts the stall timer each time a read returns
// bytes.
type progressBody struct {
	body  io.ReadCloser
	ctx   context.Context
	stall *time.Timer
	stop  func()
}

func (b *progressBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.stall.Reset(StallLimit)
	}
	if err != nil && err != io.EOF {
		err = causeOf(b.ctx, err)
	}
	return n, err
}

func (b *progressBody) Close() error {
	b.stop()
	return b.body.Close()
}
