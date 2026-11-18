package main

import "syscall"

// freeBytes returns the bytes available in the filesystem at path. On
// efivarfs the kernel reads the number from the firmware's
// QueryVariableInfo, so it is the free space of the UEFI variable store.
func freeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * st.Bsize, nil
}
