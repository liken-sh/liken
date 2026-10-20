package main

import (
	"errors"
	"testing"
)

const efivarsMounted = "sysfs /sys sysfs rw 0 0\nefivarfs /sys/firmware/efi/efivars efivarfs ro,nosuid,nodev,noexec 0 0\n"

func TestEnsureEFIVars(t *testing.T) {
	tests := []struct {
		name       string
		efi        bool
		table      string
		tableErr   error
		wantMounts int
		wantErr    bool
	}{
		{"a BIOS machine", false, "", nil, 0, false},
		{"not mounted yet", true, "sysfs /sys sysfs rw 0 0\n", nil, 1, false},
		{"already mounted", true, efivarsMounted, nil, 0, false},
		{"the mount table is unreadable", true, "", errors.New("no /proc"), 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mounts := 0
			m := mounter{
				exists: func(string) bool { return tt.efi },
				mounts: func() (string, error) { return tt.table, tt.tableErr },
				mount:  func(string) error { mounts++; return nil },
			}

			err := m.ensureEFIVars()

			if (err != nil) != tt.wantErr || mounts != tt.wantMounts {
				t.Errorf("error %v, %d mounts; want error %v, %d mounts", err, mounts, tt.wantErr, tt.wantMounts)
			}
		})
	}
}

func TestOSMounterSeesThisMachine(t *testing.T) {
	m := osMounter()

	table, err := m.mounts()

	if err != nil || !mountedAt(table, "/proc") {
		t.Errorf("/proc is not in the mount table: %v", err)
	}
	if !m.exists("/proc") || m.exists("/no/such/path") {
		t.Error("exists does not tell a present path from a missing one")
	}
}
