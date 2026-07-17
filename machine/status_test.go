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
