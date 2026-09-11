//go:build !linux && !darwin

package installer

import (
	"os"
	"path/filepath"
)

type mutationInfo struct {
	exists    bool
	symlink   bool
	directory bool
}

// Platforms without descriptor-relative no-follow renames still perform the
// complete ancestor validation before every mutation.
type mutationParent struct{ path string }

func openMutationParent(path string) (*mutationParent, error) {
	if err := ensureNoSymlinkedAncestors(filepath.Join(path, "placeholder")); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	if err := ensureNoSymlinkedAncestors(filepath.Join(path, "placeholder")); err != nil {
		return nil, err
	}
	return &mutationParent{path: path}, nil
}

func (parent *mutationParent) close() {}

func (parent *mutationParent) createStageDirectory(prefix string) (string, error) {
	stage, err := os.MkdirTemp(parent.path, prefix)
	if err != nil {
		return "", err
	}
	return filepath.Base(stage), nil
}

func (parent *mutationParent) createTemporaryFile(prefix string) (string, error) {
	file, err := os.CreateTemp(parent.path, prefix)
	if err != nil {
		return "", err
	}
	name := filepath.Base(file.Name())
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	if err := os.Remove(file.Name()); err != nil {
		return "", err
	}
	return name, nil
}

func (parent *mutationParent) copyTree(stageName, src string) error {
	return copyDir(filepath.Join(parent.path, stageName), src)
}

func (parent *mutationParent) lstat(name string) (mutationInfo, error) {
	info, err := os.Lstat(filepath.Join(parent.path, name))
	if os.IsNotExist(err) {
		return mutationInfo{}, nil
	}
	if err != nil {
		return mutationInfo{}, err
	}
	return mutationInfo{exists: true, symlink: info.Mode()&os.ModeSymlink != 0, directory: info.IsDir()}, nil
}

func (parent *mutationParent) rename(oldName, newName string) error {
	return os.Rename(filepath.Join(parent.path, oldName), filepath.Join(parent.path, newName))
}

func (parent *mutationParent) remove(name string) error {
	return removePath(filepath.Join(parent.path, name))
}
