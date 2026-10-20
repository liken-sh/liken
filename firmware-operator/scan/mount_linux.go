package main

import "syscall"

func mountEFIVars(target string) error {
	const flags = syscall.MS_RDONLY | syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC
	return syscall.Mount("efivarfs", target, "efivarfs", flags, "")
}
