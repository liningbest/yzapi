package api

import (
	"container/list"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"yzapi/internal/essink"
	"yzapi/internal/model"
	"yzapi/internal/settings"
	"yzapi/internal/vector"
)

const masked = "******"

func (s *Server) getSettings(c *gin.Context) {
	all := s.st.Get()
	es := all.Elasticsearch
	if es.APIKey != "" {
		es.APIKey = masked
	}
	if es.Password != "" {
		es.Password = masked
	}
	c.JSON(200, gin.H{"basic": all.Basic, "performance": all.Performance, "vector": all.Vector,
		"smart_route": all.SmartRoute, "compliance": all.Compliance, "elasticsearch": es, "pricing": all.Pricing})
}

func (s *Server) putBasic(c *gin.Context) {
	var in settings.Basic
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.BaseURL == "" || (!strings.HasPrefix(in.BaseURL, "http://") && !strings.HasPrefix(in.BaseURL, "https://")) {
		badRequest(c, "接入地址必须为 http(s):// 开头")
		return
	}
	if !strings.HasSuffix(in.BaseURL, "/v1") {
		badRequest(c, "接入地址需以 /v1 结尾")
		return
	}
	if in.LogRetentionDays < 1 || in.LogRetentionDays > 365 {
		badRequest(c, "日志保留天数范围 1-365")
		return
	}
	if strings.TrimSpace(in.SiteName) == "" {
		in.SiteName = "YZ AI Gateway"
	}
	if err := s.st.SetBasic(in); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, in)
}

func (s *Server) putPerformance(c *gin.Context) {
	var in settings.Performance
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if in.MaxConcurrency < 0 || in.QueueSize < 0 || in.QueueTimeoutSec < 0 || in.RequestTimeoutSec < 0 ||
		in.StreamIdleTimeout < 0 || in.MaxBodyKB < 0 || in.CooldownSec < 0 || in.MaxRetries < 0 || in.UpstreamConnTimeout < 0 ||
		in.MaxBodyMemoryMB < 0 || in.VectorMaxConcurrency < 0 || in.VectorTimeoutSec < 0 {
		badRequest(c, "数值不能为负数")
		return
	}
	if in.MaxRetries == 0 {
		in.MaxRetries = 3
	}
	if in.MaxBodyKB == 0 {
		in.MaxBodyKB = 20480
	}
	if in.MaxBodyMemoryMB == 0 {
		in.MaxBodyMemoryMB = 512
	}
	if in.VectorMaxConcurrency == 0 {
		in.VectorMaxConcurrency = 16
	}
	if in.VectorTimeoutSec == 0 {
		in.VectorTimeoutSec = 10
	}
	if err := s.st.SetPerformance(in); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, in)
}

func (s *Server) vectorClient(accountID uint, mdl string) (*vector.Client, string) {
	var a model.Account
	if err := s.db.First(&a, accountID).Error; err != nil {
		return nil, "向量账号不存在"
	}
	if a.Type != model.TypeEmbedding {
		return nil, "所选账号不是向量类型"
	}
	if !a.Enabled {
		return nil, "所选账号已禁用"
	}
	key, _ := s.cipher.Decrypt(a.APIKeyEnc)
	// The upstream model (test model fallback, then the account's mapping) comes from
	// the shared identity rule, so the runtime key and the upgrade migration agree.
	r, err := vector.Identity(s.db, a.ID, mdl)
	if err != nil || !r.Live {
		return nil, "向量账号无法解析"
	}
	return vector.New(r.BaseURL, key, r.Upstream, s.gw.HTTPClient()), ""
}

// vectorRuntime caches the embedding client and recent embeddings, and bounds the number
// of concurrent embedding calls. It is invalidated whenever vector settings or the
// underlying account change.
type vectorRuntime struct {
	mu      sync.Mutex
	key     string // resolved identity "<account>|<base url>|<upstream model>"
	client  *vector.Client
	gen     uint64 // bumped on every invalidation; stale calls must not fill the cache
	cache   *embedLRU
	limiter vecLimiter
}

// vecLimiter bounds concurrent embedding calls with a limit that can change at runtime
// without losing track of calls already in flight.
type vecLimiter struct {
	mu       sync.Mutex
	cond     *sync.Cond
	inflight int
	limit    int
}

func (l *vecLimiter) init() {
	if l.cond == nil {
		l.cond = sync.NewCond(&l.mu)
	}
}

func (l *vecLimiter) acquire(ctx context.Context, limit int) bool {
	l.mu.Lock()
	l.init()
	l.limit = limit
	stop := context.AfterFunc(ctx, func() { l.cond.Broadcast() })
	defer stop()
	for l.limit > 0 && l.inflight >= l.limit {
		if ctx.Err() != nil {
			l.mu.Unlock()
			return false
		}
		l.cond.Wait()
	}
	l.inflight++
	l.mu.Unlock()
	return true
}

func (l *vecLimiter) release() {
	l.mu.Lock()
	l.init()
	l.inflight--
	l.mu.Unlock()
	l.cond.Broadcast()
}

func (l *vecLimiter) current() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inflight
}

// Whenever the vector account, its mappings or the vector settings change, handlers
// call reloadRuntimes(c, "vector"): it drops the cached client/embeddings and
// re-evaluates sample compatibility in both engines, reporting a failed refresh.

func (s *Server) InvalidateVector() {
	s.vec.mu.Lock()
	s.vec.gen++
	s.vec.key, s.vec.client = "", nil
	if s.vec.cache != nil {
		s.vec.cache.clear()
	}
	s.vec.mu.Unlock()
}

// VectorInflight reports concurrent embedding calls (for /metrics).
func (s *Server) VectorInflight() int64 { return int64(s.vec.limiter.current()) }

// VectorIdentity resolves the embedding identity actually in use: account id, base URL
// and the mapped upstream model. Changing any of them invalidates stored vectors.
func (s *Server) VectorIdentity() string {
	v := s.st.Get().Vector
	if v.AccountID == 0 {
		return ""
	}
	cl, _ := s.resolveVectorClient(v)
	if cl == nil {
		return fmt.Sprintf("%d:%s", v.AccountID, v.Model)
	}
	return fmt.Sprintf("%d|%s|%s", v.AccountID, cl.BaseURL, cl.Model)
}

// vectorSnapshot is a consistent view of the vector runtime taken in one critical
// section: the client, its identity key, the generation it belongs to and the cache
// object. A call that embeds with this client may only fill this cache if the
// generation is still current when it finishes.
type vectorSnapshot struct {
	client *vector.Client
	key    string
	gen    uint64
	cache  *embedLRU
}

// snapshotVector resolves (creating if needed) the client and returns it together with
// the generation and cache under a single lock, so a request can never pair an old
// client with a newer generation.
func (s *Server) snapshotVector(v settings.Vector) (vectorSnapshot, string) {
	s.vec.mu.Lock()
	defer s.vec.mu.Unlock()
	if s.vec.client == nil {
		cl, msg := s.vectorClient(v.AccountID, v.Model)
		if cl == nil {
			return vectorSnapshot{}, msg
		}
		s.vec.client = cl
		s.vec.key = fmt.Sprintf("%d|%s|%s", v.AccountID, cl.BaseURL, cl.Model)
	}
	if s.vec.cache == nil {
		s.vec.cache = newEmbedLRU(4096, 10*time.Minute)
	}
	return vectorSnapshot{client: s.vec.client, key: s.vec.key, gen: s.vec.gen, cache: s.vec.cache}, ""
}

// resolveVectorClient returns the cached client for the current vector settings.
func (s *Server) resolveVectorClient(v settings.Vector) (*vector.Client, string) {
	snap, msg := s.snapshotVector(v)
	if snap.client == nil {
		return nil, msg
	}
	return snap.client, snap.key
}

// VectorEmbedFunc returns an embedding function bound to the current vector settings.
func (s *Server) VectorEmbedFunc() func(ctx context.Context, inputs []string) ([][]float32, error) {
	return func(ctx context.Context, inputs []string) ([][]float32, error) {
		v := s.st.Get().Vector
		perf := s.st.Get().Performance
		snap, msg := s.snapshotVector(v)
		if snap.client == nil {
			return nil, errVector(msg)
		}
		cl, key, gen, cache := snap.client, snap.key, snap.gen, snap.cache

		// Serve from cache when every input is known.
		out := make([][]float32, len(inputs))
		var missing []int
		for i, in := range inputs {
			if vec, ok := cache.get(key, in); ok {
				out[i] = vec
			} else {
				missing = append(missing, i)
			}
		}
		if len(missing) == 0 {
			return out, nil
		}
		timeout := time.Duration(max(perf.VectorTimeoutSec, 1)) * time.Second
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if !s.vec.limiter.acquire(ctx, max(perf.VectorMaxConcurrency, 1)) {
			return nil, errVector("embedding concurrency limit reached (timeout waiting)")
		}
		defer s.vec.limiter.release()
		batch := make([]string, len(missing))
		for j, i := range missing {
			batch[j] = inputs[i]
		}
		vecs, err := cl.Embed(ctx, batch)
		if err != nil {
			return nil, err
		}
		if len(vecs) != len(batch) {
			return nil, errVector("embedding returned wrong number of vectors")
		}
		for j, i := range missing {
			out[i] = vecs[j]
		}
		// Fill the cache only if nothing was invalidated while the call was in flight;
		// the check and the writes happen under the same lock so an invalidation cannot
		// slip in between them.
		s.vec.mu.Lock()
		if s.vec.gen == gen && s.vec.cache == cache {
			for j, i := range missing {
				cache.put(key, inputs[i], vecs[j])
			}
		}
		s.vec.mu.Unlock()
		return out, nil
	}
}

// embedLRU is a small TTL + capacity bounded cache of embeddings keyed by model and text.
type embedLRU struct {
	mu   sync.Mutex
	cap  int
	ttl  time.Duration
	m    map[string]*embedEntry
	list *list.List
}

type embedEntry struct {
	key  string
	vec  []float32
	exp  time.Time
	elem *list.Element
}

func newEmbedLRU(capacity int, ttl time.Duration) *embedLRU {
	return &embedLRU{cap: capacity, ttl: ttl, m: map[string]*embedEntry{}, list: list.New()}
}

func (c *embedLRU) clear() {
	c.mu.Lock()
	c.m = map[string]*embedEntry{}
	c.list.Init()
	c.mu.Unlock()
}

func (c *embedLRU) get(model, text string) ([]float32, bool) {
	k := model + "\x00" + text
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.exp) {
		c.list.Remove(e.elem)
		delete(c.m, k)
		return nil, false
	}
	c.list.MoveToFront(e.elem)
	return e.vec, true
}

func (c *embedLRU) put(model, text string, vec []float32) {
	if len(text) > 8192 {
		return // do not cache huge prompts
	}
	k := model + "\x00" + text
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[k]; ok {
		e.vec, e.exp = vec, time.Now().Add(c.ttl)
		c.list.MoveToFront(e.elem)
		return
	}
	e := &embedEntry{key: k, vec: vec, exp: time.Now().Add(c.ttl)}
	e.elem = c.list.PushFront(e)
	c.m[k] = e
	for c.list.Len() > c.cap {
		last := c.list.Back()
		c.list.Remove(last)
		delete(c.m, last.Value.(*embedEntry).key)
	}
}

type errVector string

func (e errVector) Error() string { return "向量服务不可用: " + string(e) }

func (s *Server) putVector(c *gin.Context) {
	var in settings.Vector
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if in.AccountID > 0 {
		if _, msg := s.vectorClient(in.AccountID, in.Model); msg != "" {
			badRequest(c, msg)
			return
		}
	}
	if err := s.st.SetVector(in); err != nil {
		serverError(c, err)
		return
	}
	if !s.reloadRuntimes(c, "vector") {
		return
	}
	c.JSON(200, in)
}

func (s *Server) testVector(c *gin.Context) {
	var in settings.Vector
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	cl, msg := s.vectorClient(in.AccountID, in.Model)
	if cl == nil {
		c.JSON(200, gin.H{"ok": false, "message": msg})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	t0 := time.Now()
	vs, err := cl.Embed(ctx, []string{"你好，世界"})
	if err != nil {
		c.JSON(200, gin.H{"ok": false, "message": err.Error(), "latency_ms": time.Since(t0).Milliseconds()})
		return
	}
	c.JSON(200, gin.H{"ok": true, "dim": len(vs[0]), "latency_ms": time.Since(t0).Milliseconds(), "message": "连接正常", "model": cl.Model})
}

func (s *Server) putSmartRoute(c *gin.Context) {
	var in settings.SmartRoute
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.VirtualModel = strings.TrimSpace(in.VirtualModel)
	if in.Enabled {
		if in.VirtualModel == "" {
			badRequest(c, "虚拟模型名称不能为空")
			return
		}
		if in.SimpleGroupID == 0 || in.ComplexGroupID == 0 {
			badRequest(c, "请选择简单与复杂请求模型组")
			return
		}
		var n int64
		s.db.Model(&model.ModelGroup{}).Where("id IN ? AND type = ?", []uint{in.SimpleGroupID, in.ComplexGroupID}, model.TypeText).Count(&n)
		if (in.SimpleGroupID == in.ComplexGroupID && n != 1) || (in.SimpleGroupID != in.ComplexGroupID && n != 2) {
			badRequest(c, "模型组不存在或不是文本类型")
			return
		}
		if s.st.Get().Vector.AccountID == 0 {
			badRequest(c, "请先在「向量服务」中配置向量账号")
			return
		}
	}
	if in.Threshold < 0 || in.Threshold > 1 || in.ConfidenceGap < 0 || in.ConfidenceGap > 1 {
		badRequest(c, "阈值范围 0-1")
		return
	}
	if in.TopK <= 0 {
		in.TopK = 5
	}
	if in.TopK > 50 {
		in.TopK = 50
	}
	if err := s.st.SetSmartRoute(in); err != nil {
		serverError(c, err)
		return
	}
	// The virtual model and the enabled flag live in the gateway's immutable routing
	// snapshot: a failed rebuild means requests keep routing by the old settings.
	if !s.reloadRuntimes(c, "gateway") {
		return
	}
	c.JSON(200, in)
}

func (s *Server) putCompliance(c *gin.Context) {
	var in settings.Compliance
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if in.SemanticThreshold < 0 || in.SemanticThreshold > 1 {
		badRequest(c, "语义阈值范围 0-1")
		return
	}
	if in.OnFailure != "block" {
		in.OnFailure = "allow"
	}
	if err := s.st.SetCompliance(in); err != nil {
		serverError(c, err)
		return
	}
	if !s.reloadRuntimes(c, "compliance") {
		return
	}
	c.JSON(200, in)
}

func (s *Server) putElasticsearch(c *gin.Context) {
	var in settings.Elasticsearch
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	curES := s.st.Get().Elasticsearch
	if in.APIKey == masked {
		in.APIKey = curES.APIKey
	}
	if in.Password == masked {
		in.Password = curES.Password
	}
	in.URL = strings.TrimRight(strings.TrimSpace(in.URL), "/")
	if in.Enabled && in.URL == "" {
		badRequest(c, "Elasticsearch 地址不能为空")
		return
	}
	if in.IndexPrefix == "" {
		in.IndexPrefix = "yzapi"
	}
	if err := s.st.SetElasticsearch(in); err != nil {
		serverError(c, err)
		return
	}
	out := in
	if out.APIKey != "" {
		out.APIKey = masked
	}
	if out.Password != "" {
		out.Password = masked
	}
	c.JSON(200, out)
}

func (s *Server) testElasticsearch(c *gin.Context) {
	var in settings.Elasticsearch
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	curES := s.st.Get().Elasticsearch
	if in.APIKey == masked {
		in.APIKey = curES.APIKey
	}
	if in.Password == masked {
		in.Password = curES.Password
	}
	if s.eng.ES == nil {
		c.JSON(200, gin.H{"ok": false, "message": "Elasticsearch 模块未启用"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	ver, err := s.eng.ES.Test(ctx, in)
	if err != nil {
		c.JSON(200, gin.H{"ok": false, "message": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true, "version": ver, "message": "连接正常"})
}

func (s *Server) esStatus(c *gin.Context) {
	if s.eng.ES == nil {
		c.JSON(200, essink.Status{})
		return
	}
	c.JSON(200, s.eng.ES.Status())
}
