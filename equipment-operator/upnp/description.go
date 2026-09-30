// Package upnp reads the UPnP device description a piece of equipment
// serves over plain HTTP. The description names the maker and the
// model in the maker's own words, which no control protocol this
// operator speaks reports as plainly. A WiiM serves it beside its
// MediaRenderer, and so does every other brand built on the LinkPlay
// platform, so the model is what tells a WiiM from another brand's
// device before the operator drives it.
package upnp

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Description is the part of a device description the operator reads.
type Description struct {
	Manufacturer string
	Model        string
}

// timeout bounds one read. A device on the LAN answers its description
// in milliseconds, so a read that takes longer is a device that will
// not answer, and the caller moves on.
const timeout = 2 * time.Second

// maxBytes bounds the body the operator reads. A LinkPlay description
// is a few kilobytes, so the bound only stops a device that sends
// without end.
const maxBytes = 64 << 10

// client reads every description. A description is plain HTTP, and each
// read is one request, so the client keeps no connection open.
var client = &http.Client{
	Timeout:   timeout,
	Transport: &http.Transport{DisableKeepAlives: true},
}

// Fetch reads the description at one URL with one GET. A description it
// cannot read, or one that names no model, is an error that carries the
// device's own answer, so a log line states why the device was not read.
func Fetch(ctx context.Context, url string) (Description, error) {
	return fetch(ctx, client, url)
}

// FetchVia reads the description the way Fetch does, over a connection
// that dial opens. A driver that reaches its device through a dialer of
// its own reads the description through the same dialer, so the read
// goes where the driver's connection goes.
func FetchVia(ctx context.Context, dial func(ctx context.Context, network, address string) (net.Conn, error), url string) (Description, error) {
	return fetch(ctx, &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DisableKeepAlives: true, DialContext: dial},
	}, url)
}

func fetch(ctx context.Context, client *http.Client, url string) (Description, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Description{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Description{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return Description{}, err
	}
	if response.StatusCode != http.StatusOK {
		return Description{}, fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if len(body) > maxBytes {
		return Description{}, fmt.Errorf("the description is larger than %d bytes", maxBytes)
	}
	return parse(body)
}

// document is the shape of a description, cut to the root device's
// fields. The tags name no namespace, so they match the UPnP device
// namespace that every description declares.
type document struct {
	Device struct {
		Manufacturer string `xml:"manufacturer"`
		ModelName    string `xml:"modelName"`
	} `xml:"device"`
}

// parse reads the root device's maker and model out of one description.
func parse(body []byte) (Description, error) {
	var doc document
	if err := xml.Unmarshal(body, &doc); err != nil {
		return Description{}, err
	}
	model := strings.TrimSpace(doc.Device.ModelName)
	if model == "" {
		return Description{}, fmt.Errorf("the description names no modelName")
	}
	return Description{Manufacturer: strings.TrimSpace(doc.Device.Manufacturer), Model: model}, nil
}
