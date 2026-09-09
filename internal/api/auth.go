package api

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"yzapi/internal/config"
	"yzapi/internal/crypto"
	"yzapi/internal/model"
)

const (
	ctxUser        = "user"
	maxFailedLogin = 5
	tokenTTL       = 7 * 24 * time.Hour
)

type authService struct {
	db     *gorm.DB
	secret []byte
	cache  sync.Map // uid -> cachedUser
	// gen is bumped by every invalidation. A lookup records it before reading the
	// database and only fills the cache if nothing was invalidated meanwhile, so a
	// read that started before a logout / disable / password change cannot write a
	// stale session_version back after the invalidation ran.
	gen atomic.Uint64
}

type cachedUser struct {
	gen uint64 // invalidation generation the entry was read under
	u   model.User
	exp time.Time
}

type claims struct {
	UID  uint   `json:"uid"`
	Ver  int    `json:"ver"`
	Role string `json:"role"`
	jwt.RegisteredClaims
}

func newAuthService(cfg *config.Config, db *gorm.DB) (*authService, error) {
	secret := []byte(cfg.JWTSecret)
	if len(secret) == 0 {
		dir := filepath.Join(cfg.DataDir, "data", "security")
		_ = os.MkdirAll(dir, 0o700)
		path := filepath.Join(dir, "jwt.key")
		b, err := os.ReadFile(path)
		if err != nil {
			b = []byte(crypto.RandomToken(48))
			if err := os.WriteFile(path, b, 0o600); err != nil {
				return nil, err
			}
		}
		secret = b
	}
	return &authService{db: db, secret: secret}, nil
}

func (a *authService) issue(u *model.User) (string, error) {
	now := time.Now()
	cl := claims{UID: u.ID, Ver: u.SessionVersion, Role: u.Role,
		RegisteredClaims: jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL))}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, cl).SignedString(a.secret)
}

func (a *authService) parse(tok string) (*claims, error) {
	cl := &claims{}
	t, err := jwt.ParseWithClaims(tok, cl, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("bad alg")
		}
		return a.secret, nil
	})
	if err != nil || !t.Valid {
		return nil, errors.New("invalid token")
	}
	return cl, nil
}

// user loads the user (cached briefly) and validates the session version.
func (a *authService) user(cl *claims) (*model.User, error) {
	if v, ok := a.cache.Load(cl.UID); ok {
		cu := v.(cachedUser)
		// An entry written under an older generation may have been filled by a read that
		// overlapped an invalidation (the check-then-store window); never trust it.
		if cu.gen == a.gen.Load() && time.Now().Before(cu.exp) {
			if cu.u.SessionVersion != cl.Ver || !cu.u.Enabled {
				return nil, errors.New("session expired")
			}
			u := cu.u
			return &u, nil
		}
	}
	gen := a.gen.Load()
	var u model.User
	if err := a.db.Preload("Group").First(&u, cl.UID).Error; err != nil {
		return nil, err
	}
	if a.gen.Load() == gen {
		if authBeforeStoreHook != nil {
			authBeforeStoreHook() // test-only scheduling point; nil in production
		}
		// The entry carries the generation it was read under, so even a store that slips
		// in after an invalidation is rejected on the next lookup.
		a.cache.Store(cl.UID, cachedUser{u: u, exp: time.Now().Add(15 * time.Second), gen: gen})
	}
	if u.SessionVersion != cl.Ver || !u.Enabled {
		return nil, errors.New("session expired")
	}
	return &u, nil
}

// authBeforeStoreHook lets tests pause between the generation check and the cache
// store to exercise the invalidation race deterministically.
var authBeforeStoreHook func()

func (a *authService) invalidate(uid uint) {
	a.gen.Add(1)
	a.cache.Delete(uid)
}

func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			fail(c, 401, "unauthorized", "login required")
			return
		}
		cl, err := s.auth.parse(strings.TrimSpace(h[7:]))
		if err != nil {
			fail(c, 401, "unauthorized", "invalid or expired token")
			return
		}
		u, err := s.auth.user(cl)
		if err != nil {
			fail(c, 401, "unauthorized", "session expired, please login again")
			return
		}
		c.Set(ctxUser, u)
		c.Next()
	}
}

func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if cur(c).Role != model.RoleAdmin {
			fail(c, 403, "forbidden", "administrator privileges required")
			return
		}
		c.Next()
	}
}

func cur(c *gin.Context) *model.User {
	u, _ := c.Get(ctxUser)
	return u.(*model.User)
}

func userView(u *model.User) gin.H {
	gn := ""
	if u.Group != nil {
		gn = u.Group.Name
	}
	return gin.H{
		"id": u.ID, "username": u.Username, "role": u.Role, "group_id": u.GroupID, "group_name": gn,
		"enabled": u.Enabled, "locked": u.Locked, "must_change_password": u.MustChangePassword,
		"note": u.Note, "last_login_at": u.LastLoginAt, "created_at": u.CreatedAt,
	}
}

func (s *Server) login(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Username == "" || in.Password == "" {
		badRequest(c, "username and password are required")
		return
	}
	var u model.User
	if err := s.db.Preload("Group").Where("username = ?", in.Username).First(&u).Error; err != nil {
		// burn some time to equalise timing
		crypto.VerifyPassword(in.Password, "$argon2id$v=19$m=65536,t=2,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		fail(c, 401, "invalid_credentials", "用户名或密码错误")
		return
	}
	if u.Locked {
		fail(c, 423, "locked", "账号已锁定，请联系管理员解锁")
		return
	}
	if !u.Enabled {
		fail(c, 403, "disabled", "账号已被禁用")
		return
	}
	if !crypto.VerifyPassword(in.Password, u.PasswordHash) {
		// Count in the database, not from the value read above: concurrent failures
		// would otherwise overwrite each other and never reach the lock threshold.
		err := s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model.User{}).Where("id = ?", u.ID).Update("failed_logins", gorm.Expr("failed_logins + 1")).Error; err != nil {
				return err
			}
			return tx.Model(&model.User{}).Where("id = ? AND failed_logins >= ?", u.ID, maxFailedLogin).Update("locked", true).Error
		})
		if err != nil {
			slog.Error("recording failed login", "user", u.Username, "err", err)
		}
		fail(c, 401, "invalid_credentials", "用户名或密码错误")
		return
	}
	now := time.Now()
	s.db.Model(&u).Updates(map[string]any{"failed_logins": 0, "last_login_at": now})
	tok, err := s.auth.issue(&u)
	if err != nil {
		serverError(c, err)
		return
	}
	u.LastLoginAt = &now
	c.JSON(200, gin.H{"token": tok, "user": userView(&u)})
}

// logout invalidates every token of the user (session_version bump) so a leaked or
// cached token cannot be used after the user has logged out.
func (s *Server) logout(c *gin.Context) {
	u := cur(c)
	if err := s.db.Model(&model.User{}).Where("id = ?", u.ID).Update("session_version", gorm.Expr("session_version + 1")).Error; err != nil {
		serverError(c, err)
		return
	}
	s.auth.invalidate(u.ID)
	c.JSON(200, gin.H{})
}

func (s *Server) me(c *gin.Context) {
	c.JSON(200, userView(cur(c)))
}

func validPassword(pw string) string {
	n := len(pw)
	if n < 6 {
		return "密码长度至少 6 位"
	}
	if n > 128 {
		return "密码长度不能超过 128 字节"
	}
	return ""
}

func (s *Server) changePassword(c *gin.Context) {
	var in struct {
		Old string `json:"old_password"`
		New string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	u := cur(c)
	if !crypto.VerifyPassword(in.Old, u.PasswordHash) {
		fail(c, 400, "wrong_password", "旧密码不正确")
		return
	}
	if msg := validPassword(in.New); msg != "" {
		badRequest(c, msg)
		return
	}
	if in.Old == in.New {
		badRequest(c, "新密码不能与旧密码相同")
		return
	}
	h, err := crypto.HashPassword(in.New)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := s.db.Model(u).Updates(map[string]any{"password_hash": h, "must_change_password": false,
		"session_version": u.SessionVersion + 1}).Error; err != nil {
		serverError(c, err)
		return
	}
	s.auth.invalidate(u.ID)
	u.SessionVersion++
	u.MustChangePassword = false
	tok, _ := s.auth.issue(u)
	c.JSON(200, gin.H{"token": tok})
}

func goVersion() string { return runtime.Version() }
