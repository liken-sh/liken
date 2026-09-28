// redirect answers every subdomain of liken.sh that has no record of
// its own with a 301 to that name's manual on the one site:
// display.liken.sh/docs/ goes to liken.sh/display/docs/.
//
// The old names stay in use because a cluster shows them. Each
// operator's API group, such as bluetooth.liken.sh, and each CSI
// driver's name, such as git.liken.sh, is also the address of its
// manual, so a person who opens a name from a cluster arrives at the
// manual. One wildcard DNS record points every such name here, so a
// component that does not exist yet is covered too. www, releases, and
// log keep records of their own, so their requests never reach this
// service.
//
// The service speaks plain HTTP. The host in front of it holds the
// wildcard certificate and terminates TLS.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "redirect:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("redirect", flag.ContinueOnError)
	addr := flags.String("addr", ":8080", "the address to listen on")
	domain := flags.String("domain", "liken.sh", "the domain whose subdomains redirect to its path prefixes")
	if err := flags.Parse(args); err != nil {
		return err
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           handler(*domain),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return server.ListenAndServe()
}

// label is one DNS label: letters, digits, and inner hyphens.
var label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// handler redirects a request for <name>.<domain> to
// https://<domain>/<name>/ with the same path and query. A request for
// any other host is not found, except the health check, which a
// kubelet sends to the pod's own address.
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
