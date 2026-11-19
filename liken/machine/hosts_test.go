package machine

import (
	"strings"
	"testing"
)

func TestHostsFileHasTheThreeFixedLinesWithNoEntries(t *testing.T) {
	want := "127.0.0.1 localhost\n::1 localhost\n127.0.1.1 node-1\n"
	if got := HostsFile("node-1", nil); got != want {
		t.Errorf("the hosts file is:\n%s\nwant:\n%s", got, want)
	}
}

func TestHostsFileAppendsOneLinePerEntryInSpecOrder(t *testing.T) {
	entries := []HostEntry{
		{Address: "10.10.0.20", Names: []string{"nas", "nas.home.arpa"}},
		{Address: "10.10.0.21", Names: []string{"printer"}},
	}
	want := "127.0.0.1 localhost\n::1 localhost\n127.0.1.1 node-1\n" +
		"10.10.0.20 nas nas.home.arpa\n10.10.0.21 printer\n"
	if got := HostsFile("node-1", entries); got != want {
		t.Errorf("the hosts file is:\n%s\nwant:\n%s", got, want)
	}
}

func TestHostsFileWritesEntriesAfterTheFixedLines(t *testing.T) {
	entries := []HostEntry{{Address: "10.10.0.1", Names: []string{"node-1"}}}
	got := HostsFile("node-1", entries)
	fixed := strings.Index(got, "127.0.1.1 node-1")
	entry := strings.Index(got, "10.10.0.1 node-1")
	if fixed == -1 || entry == -1 || fixed > entry {
		t.Errorf("the hosts file is:\n%s\nwant the fixed lines before the entry", got)
	}
}
