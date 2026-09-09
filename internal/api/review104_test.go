package api

import (
	"gorm.io/gorm"
	"sync/atomic"
	"testing"
	"yzapi/internal/model"
)

// The overlay adds a scheduling hook between the generation comparison and Store.
// It only pauses execution; all database/cache operations remain the production code.
func TestReview104LogoutAfterGenerationCheck(t *testing.T) {
	s := auditServer(t)
	u := auditUser(s, "review-admin")
	entered, resume := make(chan struct{}), make(chan struct{})
	var blocked atomic.Bool
	authBeforeStoreHook = func() {
		if blocked.CompareAndSwap(false, true) {
			close(entered)
			<-resume
		}
	}
	defer func() { authBeforeStoreHook = nil }()
	done := make(chan struct{})
	go func() { s.auth.user(&claims{UID: u.ID, Ver: 0}); close(done) }()
	<-entered
	w := auditCall(s.logout, u, 0, nil)
	close(resume)
	<-done
	if w.Code != 200 {
		t.Fatalf("logout=%d", w.Code)
	}
	if _, err := s.auth.user(&claims{UID: u.ID, Ver: 0}); err == nil {
		t.Fatal("new authorization accepted revoked token: invalidation happened after gen check but before cache.Store")
	}
}

func TestReview104AdminRoleSnapshot(t *testing.T) {
	s := auditServer(t)
	a := auditUser(s, "admin-a")
	b := &model.User{Username: "user-b", Enabled: true, Role: model.RoleUser}
	if err := s.db.Create(b).Error; err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	var blocked atomic.Bool
	s.db.Callback().Query().After("gorm:after_query").Register("review104_user_snapshot", func(tx *gorm.DB) {
		if u, ok := tx.Statement.Dest.(*model.User); ok && tx.Statement.Table == "users" && u.ID == b.ID && blocked.CompareAndSwap(false, true) {
			close(entered)
			<-resume
		}
	})
	done := make(chan int, 1)
	go func() { done <- auditCall(s.setUserEnabled, a, b.ID, map[string]any{"enabled": false}).Code }()
	<-entered
	promoted := auditCall(s.updateUser, a, b.ID, map[string]any{"role": "admin"})
	demoted := auditCall(s.updateUser, a, a.ID, map[string]any{"role": "user"})
	close(resume)
	disabled := <-done
	s.db.Callback().Query().Remove("review104_user_snapshot")
	if promoted.Code != 200 || demoted.Code != 200 {
		t.Fatalf("setup: promote=%d demote=%d", promoted.Code, demoted.Code)
	}
	if n := s.adminCount(s.db); n == 0 {
		t.Fatalf("stale user role bypassed guard: promote=%d demote=%d disable=%d enabled admins=%d", promoted.Code, demoted.Code, disabled, n)
	}
}
