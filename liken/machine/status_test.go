package machine

import (
	"testing"
	"time"
)

func TestRoleAddressesEveryRoleAndNothingElse(t *testing.T) {
	s := AllRolesInMemory()
	for _, name := range StorageRoleNames {
		rs := s.Role(name)
		if rs == nil {
			t.Fatalf("role %s should be addressable", name)
		}
		rs.Backing = BackingPartition
	}
	// Each name reached a distinct field.
	if s.MachineState.Backing != BackingPartition ||
		s.MachineEphemeral.Backing != BackingPartition ||
		s.ClusterState.Backing != BackingPartition ||
		s.PodStorage.Backing != BackingPartition ||
		s.PodEphemeral.Backing != BackingPartition {
		t.Errorf("some role name addressed the wrong field: %+v", s)
	}
	if s.Role("archive") != nil {
		t.Error("names outside the vocabulary must return nil")
	}
}

func TestApprovalGrantsAcceptsTheFullHash(t *testing.T) {
	hash := "3943abfa6adf0123456789abcdef0123456789abcdef0123456789abcdef0123"
	if !ApprovalGrants(hash, hash) {
		t.Fatal("the full hash must grant")
	}
}

func TestApprovalGrantsAcceptsTheTwelveCharPrefix(t *testing.T) {
	hash := "3943abfa6adf0123456789abcdef0123456789abcdef0123456789abcdef0123"
	if !ApprovalGrants("3943abfa6adf", hash) {
		t.Fatal("the 12-character prefix that condition messages show must grant")
	}
}

func TestApprovalGrantsRefusesShortMismatchedAndEmptyValues(t *testing.T) {
	hash := "3943abfa6adf0123456789abcdef0123456789abcdef0123456789abcdef0123"
	for _, annotation := range []string{"", "3943abfa6ad", "deadbeefdead", "0000"} {
		if ApprovalGrants(annotation, hash) {
			t.Fatalf("%q must not grant", annotation)
		}
	}
}

func TestApprovalGrantsRefusesWhenNothingIsStaged(t *testing.T) {
	if ApprovalGrants("3943abfa6adf", "") {
		t.Fatal("an approval must not grant against an empty hash")
	}
}

func TestBootIDDistinguishesOneBootFromTheNext(t *testing.T) {
	first := time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)
	second := first.Add(3 * time.Minute)
	if BootID(BootStatus{Time: &first}) == BootID(BootStatus{Time: &second}) {
		t.Fatal("two boots must not share an identity, or a request could outlive its reboot")
	}
}

func TestBootIDIsStableForOneBoot(t *testing.T) {
	at := time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)
	elsewhere := at.In(time.FixedZone("east", 3600))
	if BootID(BootStatus{Time: &at}) != BootID(BootStatus{Time: &elsewhere}) {
		t.Fatal("the same instant is the same boot, whatever zone it is rendered in")
	}
}

func TestBootIDIsEmptyWithoutABootTime(t *testing.T) {
	var zero time.Time
	for _, boot := range []BootStatus{{}, {Time: &zero}} {
		if got := BootID(boot); got != "" {
			t.Fatalf("a boot that published no time has no identity, got %q", got)
		}
	}
}

func TestRebootRequestNamesTheBootTheCLIWroteItFor(t *testing.T) {
	at := time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)
	id := BootID(BootStatus{Time: &at})
	// The CLI writes the 12-character short form, the same form every
	// condition message shows.
	if !RebootRequestNames(id[:12], id) {
		t.Fatal("the annotation the CLI writes must name the boot the operator computes")
	}
	if !RebootRequestNames(id, id) {
		t.Fatal("the full identity must name the boot too")
	}
}

func TestRebootRequestDoesNotNameAnotherBoot(t *testing.T) {
	first := time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)
	second := first.Add(3 * time.Minute)
	stale := BootID(BootStatus{Time: &first})[:12]
	if RebootRequestNames(stale, BootID(BootStatus{Time: &second})) {
		t.Fatal("a request must spend itself on the boot that comes back")
	}
}

func TestRebootRequestRefusesShortAndEmptyValues(t *testing.T) {
	at := time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)
	id := BootID(BootStatus{Time: &at})
	for _, annotation := range []string{"", id[:11], "deadbeefdead"} {
		if RebootRequestNames(annotation, id) {
			t.Fatalf("%q must not name this boot", annotation)
		}
	}
	if RebootRequestNames(id[:12], "") {
		t.Fatal("nothing names a boot that published no identity")
	}
}
