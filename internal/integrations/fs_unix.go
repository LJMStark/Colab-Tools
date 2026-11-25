//go:build !windows

package integrations

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type resolvedTarget struct {
	ResolvedPath string
	Exists       bool
	FileMode     os.FileMode
}

func resolveSafeWritableTarget(path string) (resolvedTarget, error) {
	uid := uint32(os.Getuid())
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
			if st, ok := tst.Sys().(*syscall.Stat_t); ok && st.Uid != uid {
				return resolvedTarget{}, fmt.Errorf("%w: 符号链接目标不属于当前用户：%s", ErrUnsafeFilePath, realPath)
			}
			return resolvedTarget{ResolvedPath: realPath, Exists: true, FileMode: tst.Mode().Perm()}, nil
		}
		if !lst.Mode().IsRegular() {
			return resolvedTarget{}, fmt.Errorf("%w: 目标路径不是普通文件：%s", ErrUnsafeFilePath, path)
		}
		if st, ok := lst.Sys().(*syscall.Stat_t); ok && st.Uid != uid {
			return resolvedTarget{}, fmt.Errorf("%w: 目标文件不属于当前用户：%s", ErrUnsafeFilePath, path)
		}
		return resolvedTarget{ResolvedPath: path, Exists: true, FileMode: lst.Mode().Perm()}, nil
	}
	if !os.IsNotExist(err) {
		return resolvedTarget{}, fmt.Errorf("%w: 无法检查目标状态：%v", ErrIO, err)
	}
	parent := filepath.Dir(path)
	if plst, pErr := os.Lstat(parent); pErr == nil && plst.Mode()&os.ModeSymlink != 0 {
		realParent, rErr := filepath.EvalSymlinks(parent)
		if rErr != nil {
			return resolvedTarget{}, fmt.Errorf("%w: 父目录符号链接无效：%s", ErrUnsafeFilePath, parent)
		}
		rpst, rpErr := os.Stat(realParent)
		if rpErr != nil || !rpst.IsDir() {
			return resolvedTarget{}, fmt.Errorf("%w: 父目录符号链接未指向目录：%s", ErrUnsafeFilePath, parent)
		}
		if st, ok := rpst.Sys().(*syscall.Stat_t); ok && st.Uid != uid {
			return resolvedTarget{}, fmt.Errorf("%w: 父目录不属于当前用户：%s", ErrUnsafeFilePath, realParent)
		}
		return resolvedTarget{ResolvedPath: filepath.Join(realParent, filepath.Base(path)), Exists: false, FileMode: 0o600}, nil
	}
	return resolvedTarget{ResolvedPath: path, Exists: false, FileMode: 0o600}, nil
}

func ensurePrivateBackupDir(dir string) error {
	uid := uint32(os.Getuid())
	lst, err := os.Lstat(dir)
	if err == nil {
		if lst.Mode()&os.ModeSymlink != 0 || !lst.IsDir() {
			return fmt.Errorf("%w: 备份目录不能为符号链接或非目录：%s", ErrUnsafeFilePath, dir)
		}
		if st, ok := lst.Sys().(*syscall.Stat_t); ok && st.Uid != uid {
			return fmt.Errorf("%w: 备份目录所有者异常：%s", ErrUnsafeFilePath, dir)
		}
		_ = os.Chmod(dir, 0o700)
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	return nil
}

func openExclusiveNoFollow(dst string) (*os.File, error) {
	return os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
}
