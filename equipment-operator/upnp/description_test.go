package upnp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The descriptions of two LinkPlay devices, cut to the fields the
// operator reads and the ones around them. The WiiM and the Arylic
// amplifier answer the same XML shape, with the UPnP device namespace.
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

// serve answers one body at every path with one status code.
func serve(t *testing.T, code int, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL + "/description.xml"
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
			got, err := Fetch(context.Background(), serve(t, http.StatusOK, one.body))
			if err != nil {
				t.Fatal(err)
			}
			if got != one.want {
				t.Fatalf("got %+v, want %+v", got, one.want)
			}
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
		{"no model", http.StatusOK, `<root><device><manufacturer>Someone</manufacturer></device></root>`, "names no modelName"},
		{"too large", http.StatusOK, "<root>" + strings.Repeat(" ", maxBytes) + "</root>", "larger than 65536 bytes"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			_, err := Fetch(context.Background(), serve(t, one.code, one.body))
			if err == nil || !strings.Contains(err.Error(), one.want) {
				t.Fatalf("got %v, want an error that contains %q", err, one.want)
			}
		})
	}
}

func TestFetchFailsWhenNothingAnswers(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL + "/description.xml"
	server.Close()

	if _, err := Fetch(context.Background(), url); err == nil {
		t.Fatal("a closed port gave a description")
	}
}
