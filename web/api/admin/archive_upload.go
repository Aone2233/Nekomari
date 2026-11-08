package admin

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Aone2233/nekomari/internal/lifecycle"
	"github.com/Aone2233/nekomari/internal/plugin"
	logger "github.com/Aone2233/nekomari/utils/log"
	"github.com/Aone2233/nekomari/web/backup"
	"github.com/Aone2233/nekomari/web/upload"
)

func NewArchiveUploadHandler() *upload.Handler {
	return upload.NewHandler(upload.DefaultStore, map[upload.Purpose]upload.Finalizer{
		upload.PurposeBackup: finalizeBackupUpload,
		upload.PurposePlugin: finalizePluginUpload,
		upload.PurposeTheme:  finalizeThemeUpload,
	})
}

func finalizeBackupUpload(session upload.Session) (upload.Result, error) {
	archive, err := os.Open(session.ArchivePath)
	if err != nil {
		return upload.Result{}, fmt.Errorf("open merged backup: %w", err)
	}
	if err := backup.StageUploadedBackup(archive, session.Metadata.Filename); err != nil {
		_ = archive.Close()
		return upload.Result{}, err
	}
	if err := archive.Close(); err != nil {
		// The staged file owns its own complete copy; a read-only source close
		// cannot invalidate it or safely turn this into a retryable failure.
		logger.Warnf("admin-api", "Could not close the merged backup source: %v", err)
	}

	go func() {
		logger.InfoArgs("admin-api", "Backup uploaded, requesting a clean restart in 2 seconds...")
		time.Sleep(2 * time.Second)
		lifecycle.RequestRestart(lifecycle.RestartForBackupRestore)
	}()

	return upload.Result{
		Message: "Backup uploaded successfully. The service will apply it after a clean shutdown.",
		Data:    map[string]string{"path": filepath.Join(".", "data", "backup.zip")},
	}, nil
}

func finalizePluginUpload(session upload.Session) (upload.Result, error) {
	info, err := plugin.InstallZip(session.ArchivePath)
	if err != nil {
		return upload.Result{}, err
	}
	return upload.Result{Message: "插件上传成功", Data: info}, nil
}

func finalizeThemeUpload(session upload.Session) (upload.Result, error) {
	info, err := extractAndValidateTheme(session.ArchivePath)
	if err != nil {
		return upload.Result{}, err
	}
	return upload.Result{Message: "主题上传成功", Data: info}, nil
}
