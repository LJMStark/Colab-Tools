//go:build windows

package integrations

import (
	"fmt"
	"os"
	"path/filepath"
)

type resolvedTarget struct {
	ResolvedPath string
	Exists       bool
	FileMode     os.FileMode
}

func resolveSafeWritableTarget(path string) (resolvedTarget, error) {
	lst, err := os.Lstat(path)
	if err == nil {
		if lst.Mode()&os.ModeSymlink != 0 {
			realPath, rErr := filepath.EvalSymlinks(path)
			if rErr != nil {
				return resolvedTarget{}, fmt.Errorf("%w: 符号链接目标不存在：%s", ErrUnsafeFilePath, path)
			}
			tst, tErr := os.Stat(realPath)
			if tErr != nil || !tst.Mode().IsRegular() {
				return resolvedTarget{}, fmt.Errorf("%w: 符号链接未指向普通文件：%s", ErrUnsafeFilePath, path)
			}
			return resolvedTarget{ResolvedPath: realPath, Exists: true, FileMode: tst.Mode().Perm()}, nil
		}
		if !lst.Mode().IsRegular() {
			return resolvedTarget{}, fmt.Errorf("%w: 目标路径不是普通文件：%s", ErrUnsafeFilePath, path)
		}
		return resolvedTarget{ResolvedPath: path, Exists: true, FileMode: lst.Mode().Perm()}, nil
	}
	if !os.IsNotExist(err) {
		return resolvedTarget{}, fmt.Errorf("%w: 无法检查目标状态：%v", ErrIO, err)
	}
	return resolvedTarget{ResolvedPath: path, Exists: false, FileMode: 0o600}, nil
}

func ensurePrivateBackupDir(dir string) error {
	lst, err := os.Lstat(dir)
	if err == nil {
		if lst.Mode()&os.ModeSymlink != 0 || !lst.IsDir() {
			return fmt.Errorf("%w: 备份目录不能为符号链接或非目录：%s", ErrUnsafeFilePath, dir)
		}
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}

func openExclusiveNoFollow(dst string) (*os.File, error) {
	return os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}
