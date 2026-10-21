package main

import "syscall"

// freeBytes answers the bytes free in the filesystem at path. On
// efivarfs, the kernel answers from the firmware's QueryVariableInfo,
// so the count is the variable store's own.
func freeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * st.Bsize, nil
}
