package upnp

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
)

// The descriptions of a WiiM and of an Arylic amplifier, both LinkPlay
// devices, cut down to the fields the operator reads and a few around
// them.
const (
	wiimAmpDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0" xmlns:dlna="urn:schemas-dlna-org:device-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
    <friendlyName>Den Amp</friendlyName>
    <manufacturer>Linkplay Technology Inc.</manufacturer>
    <modelName>WiiM Amp</modelName>
    <modelNumber>V01-Sep 22 2026</modelNumber>
    <UDN>uuid:FF98F2F7-AABB-CCDD-EEFF-0011FF98F2F7</UDN>
  </device>
</root>`
	arylicDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <friendlyName>Porch Amp</friendlyName>
    <manufacturer>Rakoit Technology(SZ) Co., Ltd.</manufacturer>
    <modelName>A50</modelName>
    <modelNumber>V01-Apr 27 2022</modelNumber>
  </device>
</root>`
)

// device serves one body with one status code on every connection that
// its dial opens. The connections are net.Pipe, because a real socket
// blocks the fake clock of a synctest bubble.
type device struct {
	code int
	body string
}

const descriptionURL = "http://192.0.2.10:49152/description.xml"

func (d device) dial(_ context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	go d.answer(server)
	return client, nil
}

func (d device) answer(conn net.Conn) {
	defer conn.Close()
	if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
		return
	}
	response := &http.Response{
		StatusCode:    d.code,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Body:          io.NopCloser(strings.NewReader(d.body)),
		ContentLength: int64(len(d.body)),
		Close:         true,
	}
	_ = response.Write(conn)
}

func TestFetchViaDialsThroughTheDialerItTakes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		served := device{http.StatusOK, wiimAmpDescription}
		var dialed []string
		dial := func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, network+" "+address)
			return served.dial(ctx, network, address)
		}

		got, err := FetchVia(context.Background(), dial, descriptionURL)

		if err != nil {
			t.Fatalf("fetching the description: %v", err)
		}
		if got.Model != "WiiM Amp" {
			t.Fatalf("the model is %q, want WiiM Amp", got.Model)
		}
		if len(dialed) != 1 || dialed[0] != "tcp 192.0.2.10:49152" {
			t.Fatalf("dialed %v, want one dial of tcp 192.0.2.10:49152", dialed)
		}
	})
}

func TestFetchReadsTheManufacturerAndTheModel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want Description
	}{
		{"wiim", wiimAmpDescription, Description{Manufacturer: "Linkplay Technology Inc.", Model: "WiiM Amp"}},
		{"arylic", arylicDescription, Description{Manufacturer: "Rakoit Technology(SZ) Co., Ltd.", Model: "A50"}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				got, err := FetchVia(context.Background(), device{http.StatusOK, one.body}.dial, descriptionURL)
				if err != nil {
					t.Fatalf("fetching the description: %v", err)
				}
				if got != one.want {
					t.Fatalf("the description is %+v, want %+v", got, one.want)
				}
			})
		})
	}
}

func TestFetchFailsWithTheCause(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		code int
		body string
		want string
	}{
		{"refused status", http.StatusForbidden, "forbidden here", "403 Forbidden: forbidden here"},
		{"not xml", http.StatusOK, "not a description", "EOF"},
		{"no model", http.StatusOK, `<root><device><manufacturer>Someone</manufacturer></device></root>`, "has no modelName"},
		{"too large", http.StatusOK, "<root>" + strings.Repeat(" ", maxBytes) + "</root>", "larger than 65536 bytes"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				_, err := FetchVia(context.Background(), device{one.code, one.body}.dial, descriptionURL)
				if err == nil || !strings.Contains(err.Error(), one.want) {
					t.Fatalf("the error is %v, want one that contains %q", err, one.want)
				}
			})
		})
	}
}

// refuse is a dialer that fails as a connection to a closed port does.
func refuse(_ context.Context, network, _ string) (net.Conn, error) {
	return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
}

func TestFetchFailsWhenNothingAnswers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		if _, err := FetchVia(context.Background(), refuse, descriptionURL); err == nil {
			t.Fatal("fetching from a closed port returned a description, want an error")
		}
	})
}
