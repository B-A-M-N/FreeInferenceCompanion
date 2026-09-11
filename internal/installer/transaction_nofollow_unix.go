//go:build linux || darwin

package installer

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type mutationInfo struct {
	exists    bool
	symlink   bool
	directory bool
}

// mutationParent keeps the complete ancestor chain pinned while a
// transaction renames sibling paths. Every component is opened with
// O_NOFOLLOW, so a concurrent symlink swap cannot redirect the rename.
type mutationParent struct {
	fd int
}

func openMutationParent(path string) (*mutationParent, error) {
	clean, err := canonicalMutationPath(filepath.Clean(path))
	if err != nil || !filepath.IsAbs(clean) {
		return nil, fmt.Errorf("mutation parent must be absolute")
	}
	relative := strings.TrimPrefix(clean, string(filepath.Separator))
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parent := &mutationParent{fd: fd}
	components := []string{}
	if relative != "" {
		components = strings.Split(relative, string(filepath.Separator))
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			parent.close()
			return nil, errors.New("mutation path contains an unsafe component")
		}
		child, openErr := unix.Openat(parent.fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) {
			if mkdirErr := unix.Mkdirat(parent.fd, component, 0755); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				parent.close()
				return nil, mkdirErr
			}
			child, openErr = unix.Openat(parent.fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if openErr != nil {
			parent.close()
			return nil, openErr
		}
		_ = unix.Close(parent.fd)
		parent.fd = child
	}
	return parent, nil
}

func (parent *mutationParent) close() {
	if parent != nil && parent.fd >= 0 {
		_ = unix.Close(parent.fd)
		parent.fd = -1
	}
}

func (parent *mutationParent) lstat(name string) (mutationInfo, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(parent.fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return mutationInfo{}, nil
		}
		return mutationInfo{}, err
	}
	kind := stat.Mode & unix.S_IFMT
	return mutationInfo{exists: true, symlink: kind == unix.S_IFLNK, directory: kind == unix.S_IFDIR}, nil
}

func (parent *mutationParent) rename(oldName, newName string) error {
	return unix.Renameat(parent.fd, oldName, parent.fd, newName)
}

func (parent *mutationParent) createStageDirectory(prefix string) (string, error) {
	for attempt := 0; attempt < 16; attempt++ {
		var suffix [12]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", err
		}
		name := prefix + hex.EncodeToString(suffix[:])
		if err := unix.Mkdirat(parent.fd, name, 0700); err != nil {
			if errors.Is(err, unix.EEXIST) {
				continue
			}
			return "", err
		}
		return name, nil
	}
	return "", errors.New("could not allocate a unique staging directory")
}

func (parent *mutationParent) createTemporaryFile(prefix string) (string, error) {
	for attempt := 0; attempt < 16; attempt++ {
		var suffix [12]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", err
		}
		name := prefix + hex.EncodeToString(suffix[:])
		fd, err := unix.Openat(parent.fd, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
		if err != nil {
			if errors.Is(err, unix.EEXIST) {
				continue
			}
			return "", err
		}
		if err := unix.Close(fd); err != nil {
			_ = unix.Unlinkat(parent.fd, name, 0)
			return "", err
		}
		// The name is only a collision-resistant placeholder. Remove it before
		// returning so callers can atomically rename either a file or directory
		// over the reserved name.
		if err := unix.Unlinkat(parent.fd, name, 0); err != nil {
			return "", err
		}
		return name, nil
	}
	return "", errors.New("could not allocate a unique transaction file")
}

func (parent *mutationParent) copyTree(stageName, src string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destination := stageName
		if rel != "." {
			destination = filepath.Join(stageName, rel)
		}
		if info.IsDir() {
			return parent.mkdirRelative(destination)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported plugin file type: %s", rel)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := parent.createFileRelative(destination, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		return outputErr
	})
}

func (parent *mutationParent) openRelativeDir(relative string) (int, error) {
	fd, err := unix.Dup(parent.fd)
	if err != nil {
		return -1, err
	}
	clean := filepath.Clean(relative)
	if clean == "." {
		return fd, nil
	}
	if filepath.IsAbs(clean) {
		_ = unix.Close(fd)
		return -1, errors.New("relative mutation path is absolute")
	}
	for _, component := range strings.Split(clean, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			_ = unix.Close(fd)
			return -1, errors.New("relative mutation path contains an unsafe component")
		}
		child, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			if !errors.Is(openErr, unix.ENOENT) {
				_ = unix.Close(fd)
				return -1, openErr
			}
			if mkdirErr := unix.Mkdirat(fd, component, 0755); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, mkdirErr
			}
			child, openErr = unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if openErr != nil {
				_ = unix.Close(fd)
				return -1, openErr
			}
		}
		_ = unix.Close(fd)
		fd = child
	}
	return fd, nil
}

func (parent *mutationParent) mkdirRelative(relative string) error {
	fd, err := parent.openRelativeDir(relative)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}

func (parent *mutationParent) createFileRelative(relative string, mode os.FileMode) (*os.File, error) {
	dir := filepath.Dir(relative)
	base := filepath.Base(relative)
	dirFD, err := parent.openRelativeDir(dir)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirFD)
	if mode.Perm() == 0 {
		mode = 0644
	}
	fd, err := unix.Openat(dirFD, base, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(mode.Perm()))
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), base)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not create staged file")
	}
	if err := file.Chmod(mode.Perm()); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (parent *mutationParent) remove(name string) error {
	info, err := parent.lstat(name)
	if err != nil || !info.exists {
		return err
	}
	if !info.directory || info.symlink {
		return unix.Unlinkat(parent.fd, name, 0)
	}
	childFD, err := unix.Openat(parent.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	child := &mutationParent{fd: childFD}
	file := os.NewFile(uintptr(childFD), name)
	if file == nil {
		child.close()
		return errors.New("cannot open mutation directory")
	}
	names, readErr := file.Readdirnames(-1)
	for _, childName := range names {
		if readErr == nil {
			if err := child.remove(childName); err != nil {
				_ = file.Close()
				child.fd = -1
				return err
			}
		}
	}
	_ = file.Close()
	child.fd = -1
	if readErr != nil {
		return readErr
	}
	return unix.Unlinkat(parent.fd, name, unix.AT_REMOVEDIR)
}
