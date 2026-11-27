//go:build windows

package config

// HardenProcess 在 Windows 上无需 umask，文件权限由 NTFS ACL 控制。
func HardenProcess() {}
