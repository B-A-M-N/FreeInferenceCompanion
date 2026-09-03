package installer

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/state"
)

type transactionEntry struct {
	target    string
	backup    string
	existed   bool
	committed bool
}

// installTransaction replaces files/directories through sibling renames and
// retains rollback copies until the whole installation has committed.
type installTransaction struct {
	entries []transactionEntry
}

// transactionFailureHook is test-only fault injection. It is nil in normal
// operation and lets installer tests simulate a failure after a sibling rename
// so rollback behavior is exercised at the live commit boundary.
var transactionFailureHook func(target string) error

func (tx *installTransaction) replace(target, staged string) error {
	return tx.replaceInternal(target, staged, false)
}

func (tx *installTransaction) replaceAllowSymlink(target, staged string) error {
	return tx.replaceInternal(target, staged, true)
}

func (tx *installTransaction) replaceInternal(target, staged string, allowSymlink bool) error {
	if target == "" || staged == "" {
		return fmt.Errorf("transaction target is empty")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	entry := transactionEntry{target: target}
	if info, err := os.Lstat(target); err == nil {
		if !allowSymlink && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to replace symlink %s", target)
		}
		backup, err := newSiblingPath(filepath.Dir(target), ".freeinference-rollback-*")
		if err != nil {
			return err
		}
		if err := os.Rename(target, backup); err != nil {
			_ = os.Remove(backup)
			return fmt.Errorf("stage existing %s: %w", target, err)
		}
		entry.backup = backup
		entry.existed = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		if entry.existed {
			_ = os.Rename(entry.backup, target)
		}
		return fmt.Errorf("commit %s: %w", target, err)
	}
	entry.committed = true
	tx.entries = append(tx.entries, entry)
	if transactionFailureHook != nil {
		if err := transactionFailureHook(target); err != nil {
			return err
		}
	}
	return nil
}

func (tx *installTransaction) remove(target string) error {
	return tx.removeInternal(target, false)
}

func (tx *installTransaction) removeAllowSymlink(target string) error {
	return tx.removeInternal(target, true)
}

func (tx *installTransaction) removeInternal(target string, allowSymlink bool) error {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !allowSymlink && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to remove symlink %s", target)
	}
	backup, err := newSiblingPath(filepath.Dir(target), ".freeinference-uninstall-*")
	if err != nil {
		return err
	}
	if err := os.Rename(target, backup); err != nil {
		_ = os.Remove(backup)
		return err
	}
	tx.entries = append(tx.entries, transactionEntry{target: target, backup: backup, existed: true, committed: true})
	return nil
}

func (tx *installTransaction) rollback() {
	for i := len(tx.entries) - 1; i >= 0; i-- {
		entry := tx.entries[i]
		if entry.committed {
			_ = removePath(entry.target)
		}
		if entry.existed {
			_ = os.Rename(entry.backup, entry.target)
		}
	}
	tx.entries = nil
}

func (tx *installTransaction) finalize() error {
	var firstErr error
	for _, entry := range tx.entries {
		if entry.backup == "" {
			continue
		}
		if err := removePath(entry.backup); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	tx.entries = nil
	return firstErr
}

func newSiblingPath(dir, pattern string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func stageFile(src, target string) (string, error) {
	staged, err := newSiblingPath(filepath.Dir(target), ".freeinference-stage-*")
	if err != nil {
		return "", err
	}
	if err := copyFile(src, staged); err != nil {
		_ = os.Remove(staged)
		return "", err
	}
	return staged, nil
}

func stageDirectory(src, target string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return "", err
	}
	staged, err := os.MkdirTemp(filepath.Dir(target), ".freeinference-stage-*")
	if err != nil {
		return "", err
	}
	if err := copyDir(staged, src); err != nil {
		_ = os.RemoveAll(staged)
		return "", err
	}
	return staged, nil
}

func removePath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

func withInstallerLock(paths Paths, fn func() error) error {
	lockPath := paths.lockPath()
	if lockPath == "" {
		return fmt.Errorf("installer lock path is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return fmt.Errorf("create installer lock directory: %w", err)
	}
	lock := state.NewFileLock(lockPath)
	if err := lock.Acquire(); err != nil {
		if state.IsLockBusy(err) {
			return fmt.Errorf("another install/update/uninstall operation is in progress")
		}
		return fmt.Errorf("acquire installer lock: %w", err)
	}
	defer lock.Release()
	return fn()
}

func canonicalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func pathDigest(path string) (string, error) {
	return pathDigestWithFraming(path, true)
}

func pathDigestMatches(path, expected string) (bool, error) {
	actual, err := pathDigest(path)
	if err != nil {
		return false, err
	}
	// Destructive ownership checks must only accept the current unambiguous
	// digest format. Legacy records are invalidated by metadata validation.
	return actual == expected, nil
}

func pathDigestWithFraming(path string, framed bool) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if info.Mode().IsRegular() {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		limited := io.LimitReader(f, maxArchiveTotalBytes+1)
		n, copyErr := io.Copy(h, limited)
		closeErr := f.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if n > maxArchiveTotalBytes {
			return "", fmt.Errorf("file exceeds the fingerprint size limit")
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("cannot fingerprint unsupported path")
	}
	type digestEntry struct {
		rel  string
		kind byte
	}
	var entries []digestEntry
	var total uint64
	if err := filepath.Walk(path, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("cannot fingerprint symlink")
		}
		if current == path {
			return nil
		}
		if info.Mode().IsRegular() {
			rel, err := filepath.Rel(path, current)
			if err != nil {
				return err
			}
			entries = append(entries, digestEntry{rel: rel, kind: 'f'})
		} else if info.IsDir() {
			rel, err := filepath.Rel(path, current)
			if err != nil {
				return err
			}
			entries = append(entries, digestEntry{rel: rel, kind: 'd'})
		} else {
			return fmt.Errorf("cannot fingerprint unsupported path")
		}
		return nil
	}); err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].rel != entries[j].rel {
			return entries[i].rel < entries[j].rel
		}
		return entries[i].kind < entries[j].kind
	})
	for _, entry := range entries {
		rel := entry.rel
		// Frame both path and content lengths. Without framing, a crafted
		// directory can make (path A, data B) hash identically to a different
		// path/data concatenation.
		var length [8]byte
		if framed {
			_, _ = h.Write([]byte{entry.kind})
			binary.BigEndian.PutUint64(length[:], uint64(len(rel)))
			_, _ = h.Write(length[:])
		}
		_, _ = io.WriteString(h, rel)
		if entry.kind == 'd' {
			if framed {
				binary.BigEndian.PutUint64(length[:], 0)
				_, _ = h.Write(length[:])
			}
			continue
		}
		f, err := os.Open(filepath.Join(path, rel))
		if err != nil {
			return "", err
		}
		fileInfo, statErr := f.Stat()
		if statErr != nil {
			_ = f.Close()
			return "", statErr
		}
		if fileInfo.Size() > maxArchiveFileBytes {
			_ = f.Close()
			return "", fmt.Errorf("file exceeds the fingerprint size limit")
		}
		if framed {
			binary.BigEndian.PutUint64(length[:], uint64(fileInfo.Size()))
			_, _ = h.Write(length[:])
		}
		n, copyErr := io.Copy(h, io.LimitReader(f, maxArchiveFileBytes+1))
		closeErr := f.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if n > maxArchiveFileBytes || n != fileInfo.Size() {
			return "", fmt.Errorf("file changed during fingerprinting")
		}
		total += uint64(n)
		if total > maxArchiveTotalBytes {
			return "", fmt.Errorf("directory exceeds the fingerprint size limit")
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
