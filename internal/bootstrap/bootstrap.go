// Package bootstrap seeds the database on first start.
package bootstrap

import (
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"yzapi/internal/config"
	"yzapi/internal/crypto"
	"yzapi/internal/model"
)

// Run ensures a default group and an initial administrator exist.
func Run(cfg *config.Config, db *gorm.DB) error {
	var def model.UserGroup
	err := db.Where("is_default = ?", true).First(&def).Error
	if err == gorm.ErrRecordNotFound {
		def = model.UserGroup{Name: "Default", IsDefault: true, Enabled: true, Note: "系统默认用户组"}
		if err := db.Create(&def).Error; err != nil {
			return fmt.Errorf("create default group: %w", err)
		}
		slog.Info("created default user group")
	} else if err != nil {
		return err
	}
	if !def.Enabled {
		db.Model(&def).Update("enabled", true)
	}

	var n int64
	db.Model(&model.User{}).Where("role = ?", model.RoleAdmin).Count(&n)
	if n > 0 {
		return nil
	}
	pw := cfg.InitialAdminPassword
	generated := false
	if pw == "" {
		pw = crypto.RandomPassword()
		generated = true
	}
	hash, err := crypto.HashPassword(pw)
	if err != nil {
		return err
	}
	admin := model.User{Username: "admin", PasswordHash: hash, Role: model.RoleAdmin, GroupID: def.ID, Enabled: true, MustChangePassword: true}
	if err := db.Create(&admin).Error; err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	if generated {
		slog.Info("initial administrator created", "username", "admin", "temporary_password", pw)
		fmt.Printf("\n==============================================\n  initial administrator created\n  username: admin\n  temporary password: %s\n  (you will be asked to change it on first login)\n==============================================\n\n", pw)
	} else {
		slog.Info("initial administrator created from YZAPI_INITIAL_ADMIN_PASSWORD", "username", "admin")
	}
	return nil
}

// ResetAdminPassword sets a new password for the given admin (CLI helper).
func ResetAdminPassword(db *gorm.DB, username, pw string) error {
	if pw == "" {
		pw = crypto.RandomPassword()
	}
	hash, err := crypto.HashPassword(pw)
	if err != nil {
		return err
	}
	res := db.Model(&model.User{}).Where("username = ?", username).Updates(map[string]any{
		"password_hash": hash, "locked": false, "failed_logins": 0, "enabled": true, "must_change_password": true,
		"session_version": gorm.Expr("session_version + 1"),
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("user %q not found", username)
	}
	fmt.Printf("password for %s reset to: %s\n", username, pw)
	return nil
}
