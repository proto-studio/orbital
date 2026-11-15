//go:build !unix

package runtime

import (
	"errors"
	"io/fs"
	"os"
)

// On systems without openat, a LocalFilesystem with a root refuses every call, since it can't
// walk paths as local_fs_walk.go does.

var errNoWalk = errors.ErrUnsupported

func (l *LocalFilesystem) rootedStat(p string, follow bool) (*FileInfo, error) { return nil, errNoWalk }

func (l *LocalFilesystem) rootedOpenFile(p string, flag int, perm fs.FileMode) (*os.File, error) {
	return nil, errNoWalk
}

func (l *LocalFilesystem) rootedReadFile(p string) ([]byte, error) { return nil, errNoWalk }

func (l *LocalFilesystem) rootedWrite(p string, flag int, perm fs.FileMode, data []byte) error {
	return errNoWalk
}

func (l *LocalFilesystem) rootedReadDir(p string) ([]DirEntry, error) { return nil, errNoWalk }

func (l *LocalFilesystem) rootedMkdir(p string, perm fs.FileMode) error { return errNoWalk }

func (l *LocalFilesystem) rootedMkdirAll(p string, perm fs.FileMode) error { return errNoWalk }

func (l *LocalFilesystem) rootedRemove(p string) error { return errNoWalk }

func (l *LocalFilesystem) rootedRemoveAll(p string) error { return errNoWalk }

func (l *LocalFilesystem) rootedRename(oldPath, newPath string) error { return errNoWalk }

func (l *LocalFilesystem) rootedCopy(src, dst string) error { return errNoWalk }
