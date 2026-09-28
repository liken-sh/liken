// preview serves a built tree of the project's sites on a workstation,
// so a person can read every manual together before anything
// publishes.
//
// The root Makefile builds each component's site into one tree, in the
// layout of the one site: liken's manual at the root, and each
// component's manual under its own prefix. `make preview` then runs
// this program on that tree:
//
//	go run ./preview -dir <tree> [-addr localhost:8080]
//
// A plain file server is almost enough. It differs from GitHub Pages
// in two answers, and this program gives the answer Pages gives: every
// page's Markdown twin is text/markdown, and a missing page gets the
// site's own 404.html with the status 404.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
)

const usage = "usage: preview -dir <tree> [-addr <host:port>]"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("preview", flag.ContinueOnError)
	dir := flags.String("dir", "", "the built tree to serve")
	addr := flags.String("addr", "localhost:8080", "the address to listen on")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New(usage)
	}
	fmt.Printf("serving %s at http://%s/\n", *dir, *addr)
	return http.ListenAndServe(*addr, handler(*dir))
}

// handler serves root the way GitHub Pages serves a site. Go's table
// of types has no entry for .md, so without the registration a twin
// would go out as whatever the content sniffs as.
func handler(root string) http.Handler {
	_ = mime.AddExtensionType(".md", "text/markdown; charset=utf-8")
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Join(root, filepath.FromSlash(path.Clean("/"+r.URL.Path)))
		if _, err := os.Stat(name); errors.Is(err, os.ErrNotExist) {
			notFound(w, root)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// notFound answers with the site's own 404.html, which Hugo builds at
// the root of the tree, or with a plain 404 when the tree has none.
func notFound(w http.ResponseWriter, root string) {
	page, err := os.Open(filepath.Join(root, "404.html"))
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	defer page.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.Copy(w, page)
}
