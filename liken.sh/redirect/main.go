// redirect answers every subdomain of liken.sh that has no record of
// its own with a 301 to that name's manual on the one site:
// display.liken.sh/docs/ goes to liken.sh/display/docs/.
//
// The old names stay in use because a cluster shows them. Each
// operator's API group, such as bluetooth.liken.sh, and each CSI
// driver's name, such as git.liken.sh, is also the address of its
// manual, so a person who opens a name from a cluster arrives at the
// manual. One wildcard DNS record points every such name here. www,
// releases, and log keep records of their own, so their requests never
// reach this service.
//
// With -names, the service also answers HTTPS for exactly those names,
// with a certificate for each one from Let's Encrypt over HTTP-01, and
// the host needs no secret at all. A name outside the list still
// redirects over plain HTTP, and fails its TLS handshake. The list is
// fixed on purpose: a
// policy that got a certificate for any name would let a stranger spend
// the weekly Let's Encrypt quota of liken.sh by asking for random names,
// and GitHub Pages renews the apex certificate from that same quota.
// Without -names, it speaks plain HTTP only, for a host in front of it
// that terminates TLS.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "redirect:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("redirect", flag.ContinueOnError)
	addr := flags.String("addr", ":8080", "the address to answer HTTP on")
	tlsAddr := flags.String("tls-addr", ":8443", "the address to answer HTTPS on, when -names is set")
	domain := flags.String("domain", "liken.sh", "the domain whose subdomains redirect to its path prefixes")
	names := flags.String("names", "", "comma-separated subdomains to answer HTTPS for, such as display,git")
	cache := flags.String("cache", "", "the directory that keeps the certificates across restarts; required with -names")
	if err := flags.Parse(args); err != nil {
		return err
	}
	redirects := handler(*domain)
	if *names == "" {
		return serve(&http.Server{Addr: *addr, Handler: redirects})
	}
	if *cache == "" {
		return errors.New("-names needs -cache, or every restart asks Let's Encrypt for every certificate again")
	}
	manager := certificates(*domain, strings.Split(*names, ","), *cache)
	// Port 80 answers Let's Encrypt's HTTP-01 challenges, and sends
	// every other request straight to the manual.
	errs := make(chan error, 2)
	go func() { errs <- serve(&http.Server{Addr: *addr, Handler: manager.HTTPHandler(redirects)}) }()
	go func() {
		server := &http.Server{Addr: *tlsAddr, Handler: redirects, TLSConfig: manager.TLSConfig()}
		server.ReadHeaderTimeout = 10 * time.Second
		errs <- server.ListenAndServeTLS("", "")
	}()
	return <-errs
}

func serve(server *http.Server) error {
	server.ReadHeaderTimeout = 10 * time.Second
	return server.ListenAndServe()
}

// certificates is the manager that gets and renews a certificate for
// each of the names, and refuses every other name.
func certificates(domain string, names []string, cache string) *autocert.Manager {
	hosts := make([]string, 0, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			hosts = append(hosts, name+"."+domain)
		}
	}
	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(hosts...),
		Cache:      autocert.DirCache(cache),
	}
}

// label is one DNS label: letters, digits, and inner hyphens.
var label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// handler redirects a request for <name>.<domain> to
// https://<domain>/<name>/ with the same path and query. A request for
// any other host is not found, except the health check, which a
// monitor sends to the host's own address.
func handler(domain string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		name, ok := strings.CutSuffix(host, "."+domain)
		if !ok || !label.MatchString(name) {
			if !strings.HasSuffix(host, domain) && r.URL.Path == "/healthz" {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Error(w, "this service redirects the subdomains of "+domain+" to their manuals", http.StatusNotFound)
			return
		}
		target := "https://" + domain + "/" + name + "/" + strings.TrimPrefix(r.URL.Path, "/")
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}
