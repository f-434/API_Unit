//go:build !windows

package config

import "syscall"

// HardenProcess 收紧进程创建文件的默认权限：新文件仅属主可读写。
// 数据库、密钥与图片目录都落在 DATA_DIR，避免同机其他用户读取。
func HardenProcess() {
	syscall.Umask(0o077)
}
