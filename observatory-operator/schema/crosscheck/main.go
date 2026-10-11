// Command crosscheck fetches the standard properties page of
// indilib/docs and writes the report of the disagreements between the
// sources of the standard properties, schema/disagreements.txt. It
// reaches GitHub, so it runs on a workstation through make crosscheck,
// never in a test.
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

// The page's source is in the indilib/docs repository.
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

// pageURL answers the address of the page's source at a commit.
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
		return nil, fmt.Errorf("%s: %s", url, response.Status)
	}
	return io.ReadAll(response.Body)
}
