package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestASubdomainRedirectsToItsManual(t *testing.T) {
	cases := []struct {
		name, host, target, location string
	}{
		{"the root", "display.liken.sh", "/", "https://liken.sh/display/"},
		{"a page", "display.liken.sh", "/docs/guides/install/", "https://liken.sh/display/docs/guides/install/"},
		{"a query", "media.liken.sh", "/docs/?q=remote", "https://liken.sh/media/docs/?q=remote"},
		{"a port", "per-node.liken.sh:443", "/deploy/driver.yaml", "https://liken.sh/per-node/deploy/driver.yaml"},
		{"capitals", "Bluetooth.Liken.SH", "/", "https://liken.sh/bluetooth/"},
		{"a name no component has yet", "weather.liken.sh", "/", "https://liken.sh/weather/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, c.target, nil)
			r.Host = c.host
			w := httptest.NewRecorder()
			handler("liken.sh").ServeHTTP(w, r)
			if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != c.location {
				t.Errorf("%d %q, want 301 %q", w.Code, w.Header().Get("Location"), c.location)
			}
		})
	}
}

func TestAHostThatIsNotOneSubdomainIsNotFound(t *testing.T) {
	for _, host := range []string{"liken.sh", "a.b.liken.sh", "example.com", "liken.sh.example.com", "-x.liken.sh", ""} {
		t.Run(host, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Host = host
			w := httptest.NewRecorder()
			handler("liken.sh").ServeHTTP(w, r)
			if w.Code != http.StatusNotFound {
				t.Errorf("%s answered %d", host, w.Code)
			}
		})
	}
}

func TestTheHealthCheckAnswersOnAnyOtherHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Host = "10.42.0.7:8080"
	w := httptest.NewRecorder()
	handler("liken.sh").ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("the health check answered %d", w.Code)
	}
}

func TestRunRefusesAnUnknownFlag(t *testing.T) {
	if err := run([]string{"-nope"}); err == nil {
		t.Error("run took an unknown flag")
	}
}

func TestRunReportsAnAddressItCannotListenOn(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	if err := run([]string{"-addr", taken.Addr().String()}); err == nil {
		t.Error("run listened on an address that is taken")
	}
}

func TestTheCertificatesCoverOnlyTheNamedSubdomains(t *testing.T) {
	manager := certificates("liken.sh", []string{"display", " git ", ""}, t.TempDir())
	for host, allowed := range map[string]bool{
		"display.liken.sh": true, "git.liken.sh": true,
		"random.liken.sh": false, "liken.sh": false, "display.example.com": false,
	} {
		if err := manager.HostPolicy(context.Background(), host); (err == nil) != allowed {
			t.Errorf("%s: policy answered %v", host, err)
		}
	}
}

func TestNamesNeedACache(t *testing.T) {
	if err := run([]string{"-names", "display"}); err == nil {
		t.Error("run served HTTPS with no certificate cache")
	}
}

func TestRunWithNamesReportsAnAddressItCannotListenOn(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	err = run([]string{"-names", "display", "-cache", t.TempDir(), "-addr", taken.Addr().String(), "-tls-addr", "127.0.0.1:0"})
	if err == nil {
		t.Error("run listened on an address that is taken")
	}
}
