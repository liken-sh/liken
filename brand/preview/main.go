// preview serves the built sites on a workstation, so you can read
// all the manuals together before they are published.
//
// `make preview` at the top of the repository builds every
// component's site into one tree, with liken's manual at the root and
// each component's manual under its own prefix, and runs this program
// on it:
//
//	go run ./preview -dir <tree> [-addr localhost:8080]
//
// It is a plain file server with two changes, so that it responds the
// way GitHub Pages does. It serves each page's Markdown version, the
// index.md beside its index.html, as text/markdown. It responds to a
// missing page with the status 404 and the site's own 404.html.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
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
		return fmt.Errorf("-dir is required\n%s", usage)
	}
	fmt.Printf("serving %s at http://%s/\n", *dir, *addr)
	return http.ListenAndServe(*addr, handler(*dir))
}

// handler serves the tree at root. Go's MIME table has no entry for
// .md, so without the registration the file server would guess a type
// from the content of a Markdown version.
func handler(root string) http.Handler {
	_ = mime.AddExtensionType(".md", "text/markdown; charset=utf-8")
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean removes a leading ".." from an absolute path, so a
		// request cannot name a file outside the tree.
		name := filepath.Join(root, filepath.Clean("/"+r.URL.Path))
		if _, err := os.Stat(name); errors.Is(err, os.ErrNotExist) {
			notFound(w, root)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// notFound responds with the status 404 and the 404.html that Hugo
// builds at the root of the tree, or with Go's plain-text 404 when
// the tree has none.
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
