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
			t.Fatalf("Role(%q) = nil, want the status of that role", name)
		}
		rs.Backing = BackingPartition
	}
	// Each name reached a distinct field.
	if s.MachineState.Backing != BackingPartition ||
		s.MachineEphemeral.Backing != BackingPartition ||
		s.ClusterState.Backing != BackingPartition ||
		s.PodStorage.Backing != BackingPartition ||
		s.PodEphemeral.Backing != BackingPartition {
		t.Errorf("setting each role through Role(name) left a field unset, want all five set to partition: %+v", s)
	}
	if s.Role("archive") != nil {
		t.Error(`Role("archive") is not nil, want nil for a name that is not a role`)
	}
}

func TestApprovalGrantsAcceptsTheFullHash(t *testing.T) {
	hash := "3943abfa6adf0123456789abcdef0123456789abcdef0123456789abcdef0123"
	if !ApprovalGrants(hash, hash) {
		t.Fatal("ApprovalGrants(hash, hash) = false, want true for the full hash")
	}
}

func TestApprovalGrantsAcceptsTheTwelveCharPrefix(t *testing.T) {
	hash := "3943abfa6adf0123456789abcdef0123456789abcdef0123456789abcdef0123"
	if !ApprovalGrants("3943abfa6adf", hash) {
		t.Fatal("ApprovalGrants(prefix, hash) = false, want true for the 12-character prefix that condition messages show")
	}
}

func TestApprovalGrantsRefusesShortMismatchedAndEmptyValues(t *testing.T) {
	hash := "3943abfa6adf0123456789abcdef0123456789abcdef0123456789abcdef0123"
	for _, annotation := range []string{"", "3943abfa6ad", "deadbeefdead", "0000"} {
		if ApprovalGrants(annotation, hash) {
			t.Fatalf("ApprovalGrants(%q, hash) = true, want false", annotation)
		}
	}
}

func TestApprovalGrantsRefusesWhenNothingIsStaged(t *testing.T) {
	if ApprovalGrants("3943abfa6adf", "") {
		t.Fatal(`ApprovalGrants("3943abfa6adf", "") = true, want false when no hash is staged`)
	}
}

func TestBootIDDistinguishesOneBootFromTheNext(t *testing.T) {
	first := time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)
	second := first.Add(3 * time.Minute)
	if BootID(BootStatus{Time: &first}) == BootID(BootStatus{Time: &second}) {
		t.Fatal("two boots 3 minutes apart have the same BootID, want different IDs so that a request does not outlive its reboot")
	}
}

func TestBootIDIsStableForOneBoot(t *testing.T) {
	at := time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)
	elsewhere := at.In(time.FixedZone("east", 3600))
	if BootID(BootStatus{Time: &at}) != BootID(BootStatus{Time: &elsewhere}) {
		t.Fatal("one instant in two time zones has different BootIDs, want the same ID")
	}
}

func TestBootIDIsEmptyWithoutABootTime(t *testing.T) {
	var zero time.Time
	for _, boot := range []BootStatus{{}, {Time: &zero}} {
		if got := BootID(boot); got != "" {
			t.Fatalf("BootID(%+v) = %q, want empty for a boot with no time", boot, got)
		}
	}
}

func TestRebootRequestNamesTheBootTheCLIWroteItFor(t *testing.T) {
	at := time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)
	id := BootID(BootStatus{Time: &at})
	// The CLI writes the 12-character short form, the same form every
	// condition message shows.
	if !RebootRequestNames(id[:12], id) {
		t.Fatal("RebootRequestNames(id[:12], id) = false, want true for the short form that the CLI writes")
	}
	if !RebootRequestNames(id, id) {
		t.Fatal("RebootRequestNames(id, id) = false, want true for the full ID")
	}
}

func TestRebootRequestDoesNotNameAnotherBoot(t *testing.T) {
	first := time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)
	second := first.Add(3 * time.Minute)
	stale := BootID(BootStatus{Time: &first})[:12]
	if RebootRequestNames(stale, BootID(BootStatus{Time: &second})) {
		t.Fatal("RebootRequestNames names the second boot from the ID of the first, want false so that a request ends with its reboot")
	}
}

func TestRebootRequestRefusesShortAndEmptyValues(t *testing.T) {
	at := time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)
	id := BootID(BootStatus{Time: &at})
	for _, annotation := range []string{"", id[:11], "deadbeefdead"} {
		if RebootRequestNames(annotation, id) {
			t.Fatalf("RebootRequestNames(%q, id) = true, want false", annotation)
		}
	}
	if RebootRequestNames(id[:12], "") {
		t.Fatal(`RebootRequestNames(id[:12], "") = true, want false when the boot has no ID`)
	}
}
