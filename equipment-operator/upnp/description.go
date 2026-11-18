// Package upnp reads the manufacturer and model from the UPnP device
// description that a device serves over HTTP. None of the control
// protocols this operator speaks report the model, and the model is how
// the operator tells a WiiM from another brand's LinkPlay device.
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

// Description is the manufacturer and model from a device description.
type Description struct {
	Manufacturer string
	Model        string
}

// timeout limits one read. A device on the LAN that answers at all
// answers in milliseconds.
const timeout = 2 * time.Second

// maxBytes is far above the few kilobytes of a LinkPlay description, so
// it only stops a device that sends data without end.
const maxBytes = 64 << 10

// FetchVia reads the description at url with one GET over a connection
// that dial opens, so the GET takes the same network path as the
// caller's other traffic to the device. A non-200 error includes the
// response body, for the log line.
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
		return Description{}, fmt.Errorf("the device answered %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if len(body) > maxBytes {
		return Description{}, fmt.Errorf("the description is larger than %d bytes", maxBytes)
	}
	return parse(body)
}

// document is the root device of a description. The tags name no
// namespace, so they match the UPnP device namespace that the
// description declares.
type document struct {
	Device struct {
		Manufacturer string `xml:"manufacturer"`
		ModelName    string `xml:"modelName"`
	} `xml:"device"`
}

func parse(body []byte) (Description, error) {
	var doc document
	if err := xml.Unmarshal(body, &doc); err != nil {
		return Description{}, err
	}
	model := strings.TrimSpace(doc.Device.ModelName)
	if model == "" {
		return Description{}, fmt.Errorf("the description has no modelName")
	}
	return Description{Manufacturer: strings.TrimSpace(doc.Device.Manufacturer), Model: model}, nil
}
