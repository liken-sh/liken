// Command crosscheck downloads the standard properties page of
// indilib/docs and writes schema/disagreements.txt, which lists where
// the page, the captures, the conventions, and the simulators disagree,
// for a person to review. It makes requests to GitHub, so no test runs it.
// Run it on a workstation with make crosscheck.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/schema"
)

const (
	repository = "indilib/docs"
	page       = "drivers/standard-properties.md"
)

func main() {
	root := flag.String("root", ".", "the directory of observatory-operator")
	commit := flag.String("commit", "", "the commit of indilib/docs to read; the newest when empty")
	flag.Parse()
	client := &http.Client{Timeout: 30 * time.Second}
	if *commit == "" {
		head, err := get(client, "https://api.github.com/repos/"+repository+"/commits/HEAD", "application/vnd.github.sha")
		if err != nil {
			log.Fatal(err)
		}
		*commit = strings.TrimSpace(string(head))
	}
	body, err := get(client, pageURL(*commit), "")
	if err != nil {
		log.Fatal(err)
	}
	report, err := schema.CrossCheck(*root, body, *commit)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*root, "schema", schema.ReportFile), report, 0o644); err != nil {
		log.Fatal(err)
	}
}

// pageURL returns the URL of the page's source at the given commit.
func pageURL(commit string) string {
	return "https://raw.githubusercontent.com/" + repository + "/" + commit + "/" + page
}

func get(client *http.Client, url, accept string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reading %s: the server answered %s", url, response.Status)
	}
	return io.ReadAll(response.Body)
}
