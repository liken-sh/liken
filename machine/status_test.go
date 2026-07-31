package machine

import "testing"

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
