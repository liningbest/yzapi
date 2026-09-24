package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"yzapi/internal/backup"
	"yzapi/internal/settings"
)

// restoreUploadLimit bounds an uploaded archive (the database and journal of a busy
// instance are tens of MB; 2 GB leaves ample room).
const restoreUploadLimit = 2 << 30

// backupUnsupported explains why this instance cannot use the built-in backup, or "".
// Only the embedded SQLite database at its default path is covered: a restore installs
// files into the data directory, which a custom DSN (or PostgreSQL) would never read.
func (s *Server) backupUnsupported() string {
	return backup.Unsupported(s.cfg.DBDriver, s.cfg.DBDSN)
}

// listBackups: local archives, newest first, plus whether a restore is staged.
func (s *Server) listBackups(c *gin.Context) {
	items, err := backup.List(s.cfg.DataDir)
	if err != nil {
		serverError(c, err)
		return
	}
	_, pending := backup.Pending(s.cfg.DataDir)
	why := s.backupUnsupported()
	c.JSON(200, gin.H{"items": items, "pending_restore": pending, "db_driver": s.cfg.DBDriver, "supported": why == "", "unsupported_reason": why})
}

// createBackup writes a new archive into the local backup directory.
func (s *Server) createBackup(c *gin.Context) {
	if why := s.backupUnsupported(); why != "" {
		fail(c, 400, "backup_unsupported", why)
		return
	}
	info, err := backup.CreateLocal(c.Request.Context(), s.db, s.cfg.DataDir, s.version)
	if err != nil {
		if errors.Is(err, backup.ErrUnsupportedDriver) {
			fail(c, 400, "backup_unsupported", err.Error())
			return
		}
		serverError(c, err)
		return
	}
	slog.Info("backup written", "name", info.Name, "bytes", info.Size, "actor", cur(c).Username)
	c.JSON(200, info)
}

func (s *Server) downloadBackup(c *gin.Context) {
	p, err := backup.Path(s.cfg.DataDir, c.Param("name"))
	if err != nil {
		badRequest(c, "invalid backup name")
		return
	}
	if _, err := os.Stat(p); err != nil {
		notFound(c)
		return
	}
	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", `attachment; filename="`+c.Param("name")+`"`)
	c.File(p)
}

func (s *Server) deleteBackup(c *gin.Context) {
	if !backup.ValidName(c.Param("name")) {
		badRequest(c, "invalid backup name")
		return
	}
	if err := backup.Delete(s.cfg.DataDir, c.Param("name")); err != nil {
		if os.IsNotExist(err) {
			notFound(c)
			return
		}
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"deleted": true})
}

// restoreBackup stages an uploaded archive and then asks the process to exit
// gracefully: the container / service manager restarts it and the staged files are
// applied before the database is opened. Nothing changes until that restart, so a
// rejected archive (bad checksum, foreign file, not a yzapi database) leaves the
// instance exactly as it was.
func (s *Server) restoreBackup(c *gin.Context) {
	if why := s.backupUnsupported(); why != "" {
		fail(c, 400, "backup_unsupported", why)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, restoreUploadLimit)
	fh, err := c.FormFile("file")
	if err != nil {
		badRequest(c, "missing file")
		return
	}
	f, err := fh.Open()
	if err != nil {
		badRequest(c, "cannot read upload")
		return
	}
	defer f.Close()
	m, err := backup.Stage(s.cfg.DataDir, f)
	if err != nil {
		if errors.Is(err, backup.ErrRestorePending) {
			fail(c, 409, "restore_pending", "已有一份还原包在等待应用，网关重启后即生效；请等重启完成后再上传新的备份包")
			return
		}
		if errors.Is(err, backup.ErrUnsupportedDriver) {
			fail(c, 400, "backup_unsupported", err.Error())
			return
		}
		fail(c, 400, "backup_invalid", err.Error())
		return
	}
	slog.Warn("restore staged; the gateway will exit for the service manager to restart it", "backup_created_at", m.CreatedAt, "backup_version", m.AppVersion, "actor", cur(c).Username)
	c.JSON(200, gin.H{"staged": true, "restart": "scheduled", "backup": m})
	go func() {
		time.Sleep(1500 * time.Millisecond) // let the response reach the client first
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
}

// putBackup saves the automatic backup policy.
func (s *Server) putBackup(c *gin.Context) {
	var in settings.Backup
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if in.HourLocal < 0 || in.HourLocal > 23 {
		badRequest(c, "hour_local must be 0-23")
		return
	}
	if in.KeepCount < 0 || in.KeepCount > 365 {
		badRequest(c, "keep_count must be 0-365")
		return
	}
	if why := s.backupUnsupported(); in.Enabled && why != "" {
		fail(c, 400, "backup_unsupported", why)
		return
	}
	if err := s.st.SetBackup(in); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, in)
}

// backupPolicy adapts the settings store for the scheduler.
func (s *Server) backupPolicy() backup.Policy {
	b := s.st.Get().Backup
	return backup.Policy{Enabled: b.Enabled, HourLocal: b.HourLocal, KeepCount: b.KeepCount}
}

// StartBackupScheduler runs the daily backup loop until ctx is cancelled.
func (s *Server) StartBackupScheduler(ctx context.Context) {
	go backup.RunScheduler(ctx, s.db, s.cfg.DataDir, s.version, s.backupPolicy)
}
