package dbcore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Aone2233/nekomari/internal/fsyncdir"
	"github.com/Aone2233/nekomari/internal/panellock"
	"github.com/Aone2233/nekomari/internal/restorearchive"
	logger "github.com/Aone2233/nekomari/utils/log"
)

const restoreStateName = restorearchive.StateFileName

type restoreState struct {
	Version        int    `json:"version"`
	Archive        string `json:"archive_sha256"`
	Snapshot       string `json:"snapshot"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
}

// restorePendingBackup fails closed until an interrupted destructive restore
// either completes or is inspected by an operator. The journal is made durable
// only after the original data has an independent, durable snapshot.
func restorePendingBackup() error {
	dataDir := filepath.Join(".", "data")
	archive := filepath.Join(dataDir, "backup.zip")
	statePath := filepath.Join(dataDir, restoreStateName)
	backupDir := filepath.Join(dataDir, "backup")

	state, err := readRestoreState(statePath)
	if err != nil {
		return err
	}
	archiveInfo, err := os.Lstat(archive)
	if os.IsNotExist(err) {
		if state == nil {
			return nil
		}
		return errors.New("restore journal exists without backup.zip; operator inspection is required")
	}
	if err != nil {
		return fmt.Errorf("inspect pending archive: %w", err)
	}
	if !archiveInfo.Mode().IsRegular() || archiveInfo.Size() > restorearchive.MaxArchiveSize {
		return errors.New("pending archive must be a regular file within the size limit")
	}

	if state != nil {
		if err := verifyRestoreSnapshot(state, backupDir); err != nil {
			return fmt.Errorf("restore journal requires operator inspection: %w", err)
		}
	}
	beforeHash, err := hashFile(archive)
	if err != nil {
		return fmt.Errorf("hash pending archive: %w", err)
	}
	if state != nil && beforeHash != state.Archive {
		return errors.New("pending archive changed since restore journal was written")
	}
	if err := restorearchive.ValidateArchive(archive); err != nil {
		return fmt.Errorf("validate pending archive: %w", err)
	}
	archiveHash, err := hashFile(archive)
	if err != nil {
		return fmt.Errorf("hash pending archive: %w", err)
	}
	if archiveHash != beforeHash {
		return errors.New("pending archive changed during validation")
	}
	if state == nil {
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return fmt.Errorf("create pre-restore snapshot directory: %w", err)
		}
		info, err := os.Lstat(backupDir)
		if err != nil || !info.IsDir() {
			return errors.New("pre-restore snapshot directory must be a real directory")
		}
		if err := syncDirectory(dataDir); err != nil {
			return fmt.Errorf("sync snapshot directory entry: %w", err)
		}
		name, err := newRestoreSnapshotName()
		if err != nil {
			return fmt.Errorf("name pre-restore snapshot: %w", err)
		}
		snapshot := filepath.Join(backupDir, name)
		exclude := map[string]struct{}{
			archive: {}, backupDir: {}, filepath.Join(dataDir, panellock.FileName): {},
		}
		if err := zipDirectoryExcludingWithPublish(dataDir, snapshot, exclude, true); err != nil {
			return fmt.Errorf("create pre-restore snapshot: %w", err)
		}
		snapshotHash, err := hashFile(snapshot)
		if err != nil {
			return fmt.Errorf("hash pre-restore snapshot: %w", err)
		}
		state = &restoreState{Version: 2, Archive: archiveHash, Snapshot: name, SnapshotSHA256: snapshotHash}
		if err := writeRestoreState(statePath, state); err != nil {
			return fmt.Errorf("publish restore journal: %w", err)
		}
		logger.Infof("dbcore", "[restore] current data zipped to %s", snapshot)
	}

	keep := map[string]struct{}{
		archive: {}, backupDir: {}, statePath: {}, filepath.Join(dataDir, panellock.FileName): {},
	}
	// Failed or uncertain earlier handoffs and journal publications are evidence,
	// not disposable data. Do not erase them when applying an unrelated archive.
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return fmt.Errorf("inspect retained restore staging files: %w", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), restorearchive.PendingPrefix) || strings.HasPrefix(entry.Name(), restorearchive.StateTempPrefix) {
			keep[filepath.Join(dataDir, entry.Name())] = struct{}{}
		}
	}
	if err := removeAllInDirExcept(dataDir, keep); err != nil {
		return fmt.Errorf("clear data directory; journal and archive retained: %w", err)
	}
	if err := syncDirectory(dataDir); err != nil {
		return fmt.Errorf("sync cleared data directory; journal and archive retained: %w", err)
	}
	if err := unzipToDir(archive, dataDir); err != nil {
		return fmt.Errorf("extract backup; journal and archive retained: %w", err)
	}
	if err := os.Remove(filepath.Join(dataDir, "komari-backup-markup")); err != nil {
		return fmt.Errorf("remove restored markup; journal and archive retained: %w", err)
	}
	if err := syncDirectory(dataDir); err != nil {
		return fmt.Errorf("sync restored data; journal and archive retained: %w", err)
	}
	// If removal or its sync is interrupted, the journal makes the next startup
	// fail closed instead of reapplying or assuming an uncertain restore.
	if err := os.Remove(archive); err != nil {
		return fmt.Errorf("remove applied archive; journal retained: %w", err)
	}
	if err := syncDirectory(dataDir); err != nil {
		return fmt.Errorf("sync applied archive removal; journal retained: %w", err)
	}
	if err := os.Remove(statePath); err != nil {
		return fmt.Errorf("remove restore journal: %w", err)
	}
	if err := syncDirectory(dataDir); err != nil {
		return fmt.Errorf("sync restore journal removal: %w", err)
	}
	logger.Infof("dbcore", "[restore] backup restored successfully")
	return nil
}

func readRestoreState(path string) (*restoreState, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect restore journal: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, errors.New("invalid restore journal")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read restore journal: %w", err)
	}
	var state restoreState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != 2 ||
		!validRestoreDigest(state.Archive) || !validRestoreDigest(state.SnapshotSHA256) || filepath.Base(state.Snapshot) != state.Snapshot ||
		!strings.HasPrefix(state.Snapshot, "pre-restore-") || !strings.HasSuffix(state.Snapshot, ".zip") {
		return nil, errors.New("invalid restore journal; operator inspection is required")
	}
	return &state, nil
}

func validRestoreDigest(digest string) bool {
	if len(digest) != sha256.Size*2 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func verifyRestoreSnapshot(state *restoreState, backupDir string) error {
	info, err := os.Lstat(backupDir)
	if err != nil || !info.IsDir() {
		return errors.New("pre-restore snapshot directory is missing or not a real directory")
	}
	snapshot := filepath.Join(backupDir, state.Snapshot)
	info, err = os.Lstat(snapshot)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("pre-restore snapshot is missing or not a regular file")
	}
	beforeHash, err := hashFile(snapshot)
	if err != nil || beforeHash != state.SnapshotSHA256 {
		return errors.New("pre-restore snapshot does not match the journal")
	}
	if err := verifyZipArchive(snapshot); err != nil {
		return fmt.Errorf("pre-restore snapshot is unreadable: %w", err)
	}
	afterHash, err := hashFile(snapshot)
	if err != nil || afterHash != beforeHash {
		return errors.New("pre-restore snapshot changed during verification")
	}
	return nil
}

func writeRestoreState(path string, state *restoreState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), restorearchive.StateTempPrefix+"*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := errors.Join(temp.Sync(), temp.Close()); err != nil {
		return err
	}
	if err := os.Link(tempPath, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	if err := errors.Join(copyErr, f.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func syncDirectory(path string) error { return fsyncdir.Sync(path) }
