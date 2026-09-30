// The check that keeps the driver to WiiM devices: other brands build
// on LinkPlay's platform and answer the same API.

package wiim

import (
	"context"
	"strings"
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

func TestIsWiiMComparesTheNamePrefixWithoutCase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		project string
		want    bool
	}{
		{"WiiM_Amp_4layer", true},
		{"WiiM Amp", true},
		{"A50", false},
		{"WiiM_Mini", true},
		{"WiiM_Pro_with_gc4a", true},
		{"WIIM_ULTRA", true},
		{"wiim_amp", true},
		{"ARYLIC_A50TE", false},
		{"Muzo_Mini", false},
		{"", false},
	}
	for _, one := range cases {
		t.Run(one.project, func(t *testing.T) {
			mustMatch(t, IsWiiM(one.project), one.want)
		})
	}
}

// arylicAmp is a fake device that answers getStatusEx the way another
// LinkPlay brand does.
func arylicAmp(t *testing.T) *fakeAmp {
	amp := startFakeAmp(t)
	amp.answers[statusCommand] = strings.Replace(statusExJSON, `"project":"WiiM_Amp_4layer"`, `"project":"ARYLIC_A50TE"`, 1)
	return amp
}

func TestPollOfAnotherBrandReadsOnlyStatusEx(t *testing.T) {
	amp := arylicAmp(t)
	client := amp.client(nil)
	client.poll(context.Background())

	mustMatch(t, amp.sent(), []string{statusCommand})
}

func TestPollOfAnotherBrandNamesTheProject(t *testing.T) {
	amp := arylicAmp(t)
	var projects []string
	client := amp.client(nil)
	client.Foreign = func(project string) { projects = append(projects, project) }
	client.poll(context.Background())
	client.poll(context.Background())

	mustMatch(t, projects, []string{"ARYLIC_A50TE"})
	mustMatch(t, client.Surveyed(), false)
}

func TestPollOfAnotherBrandLeavesTheDeviceUnreachableAfterMisses(t *testing.T) {
	amp := arylicAmp(t)
	client := amp.client(nil)
	for range pollFailures {
		client.poll(context.Background())
	}

	mustMatch(t, client.State().Reachable, equipment.ConditionFalse)
}

func TestPollOfAWiiMNeverCallsForeign(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)
	client.Foreign = func(string) { t.Fatal("a WiiM was called foreign") }
	mustMatch(t, client.poll(context.Background()), true)
}

func TestPollOfADeviceWithNoProjectIsNotJudged(t *testing.T) {
	amp := startFakeAmp(t)
	amp.answers[statusCommand] = strings.Replace(statusExJSON, `"project":"WiiM_Amp_4layer"`, `"project":""`, 1)
	client := amp.client(nil)
	client.Foreign = func(string) { t.Fatal("a device with no project was called foreign") }
	mustMatch(t, client.poll(context.Background()), true)
	mustMatch(t, client.Surveyed(), true)
}
