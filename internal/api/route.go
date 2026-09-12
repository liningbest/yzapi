package api

import (
	"encoding/json"
	"fmt"
	"gorm.io/gorm/clause"
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/model"
)

func (s *Server) registerRouteAPI(r *gin.RouterGroup) {
	r.GET("/samples", s.listRouteSamples)
	r.POST("/samples", s.createRouteSample)
	r.POST("/samples/batch", s.batchRouteSamples)
	r.POST("/samples/build", s.buildRouteVectors)
	r.PUT("/samples/:id", s.updateRouteSample)
	r.DELETE("/samples/:id", s.deleteRouteSample)
	r.POST("/preview", s.routePreview)
	r.GET("/decisions", s.listDecisions)
	r.GET("/decisions/:id", s.getDecision)
	r.GET("/stats", s.routeStats)
}

func routeSampleView(x *model.RouteSample) gin.H {
	return gin.H{"id": x.ID, "label": x.Label, "text": x.Text, "threshold": x.Threshold, "note": x.Note,
		"vector_dim": x.VectorDim, "vectorized": x.VectorDim > 0, "created_at": x.CreatedAt, "updated_at": x.UpdatedAt}
}

func (s *Server) listRouteSamples(c *gin.Context) {
	pg := paging(c)
	q := s.db.Model(&model.RouteSample{})
	if v := c.Query("label"); v != "" {
		q = q.Where("label = ?", v)
	}
	if v, ok := queryBool(c, "vectorized"); ok {
		if v {
			q = q.Where("vector_dim > 0")
		} else {
			q = q.Where("vector_dim = 0")
		}
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		q = q.Where("text LIKE ? ESCAPE '\\' OR note LIKE ? ESCAPE '\\'", likeEscape(v), likeEscape(v))
	}
	var total int64
	q.Count(&total)
	var rows []model.RouteSample
	if err := q.Order("id DESC").Offset(pg.Offset).Limit(pg.Size).Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, routeSampleView(&rows[i]))
	}
	listResp(c, out, total)
}

func validLabel(l string) bool { return l == "simple" || l == "complex" }

func (s *Server) createRouteSample(c *gin.Context) {
	var in struct {
		Label       string  `json:"label"`
		Text        string  `json:"text"`
		Threshold   float64 `json:"threshold"`
		Note        string  `json:"note"`
		BuildVector bool    `json:"build_vector"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if !validLabel(in.Label) || in.Text == "" || len([]rune(in.Text)) > 65536 {
		badRequest(c, "标签必须为 simple/complex，样本文本不能为空且不超过 65536 字符")
		return
	}
	// (label, text hash) is the create idempotency key, enforced by the database. The
	// insert goes first; on a unique conflict the existing row is read and compared. An
	// identical create is the retry of one whose refresh failed: it still performs the
	// requested vector build on the existing row and reports it like a fresh create. A
	// different note or threshold is a 409. A read error is a 500.
	var x model.RouteSample
	retry := func() (done bool, existing bool) {
		var dup model.RouteSample
		if err := s.db.Where("label = ? AND text_hash = ?", in.Label, model.TextKey(in.Text)).First(&dup).Error; err != nil {
			serverError(c, err)
			return true, false
		}
		if dup.Text != in.Text { // hash collision: not the same sample
			c.JSON(409, gin.H{"code": "sample_exists", "error": "已有摘要相同的样本，请修改文本", "id": dup.ID})
			return true, false
		}
		if dup.Note != in.Note || dup.Threshold != in.Threshold {
			c.JSON(409, gin.H{"code": "sample_exists", "error": "同一标签下已有同一文本的样本且设置不同；请编辑既有条目", "id": dup.ID, "resource": routeSampleView(&dup)})
			return true, false
		}
		x = dup
		return false, true
	}
	x = model.RouteSample{Label: in.Label, Text: in.Text, Threshold: in.Threshold, Note: in.Note}
	if err := s.db.Create(&x).Error; err != nil {
		if !uniqueViolation(err) {
			serverError(c, err)
			return
		}
		if done, existing := retry(); done || !existing {
			if !done {
				serverError(c, err)
			}
			return
		}
	}
	var buildErr string
	if in.BuildVector && s.eng.Route != nil {
		if _, failed, err := s.eng.Route.BuildVectors(c.Request.Context(), []uint{x.ID}); err != nil {
			if buildReloadFailed(c, "route", err, gin.H{"id": x.ID, "resource": routeSampleView(&x)}) {
				return
			}
			buildErr = err.Error()
		} else if failed > 0 {
			buildErr = "向量构建失败，请检查向量服务"
		}
		s.db.First(&x, x.ID)
	}
	v := routeSampleView(&x)
	if buildErr != "" {
		v["build_error"] = buildErr
	}
	c.JSON(200, v)
}

func (s *Server) updateRouteSample(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var x model.RouteSample
	if err := s.db.First(&x, id).Error; err != nil {
		notFound(c)
		return
	}
	var in struct {
		Label       string  `json:"label"`
		Text        string  `json:"text"`
		Threshold   float64 `json:"threshold"`
		Note        string  `json:"note"`
		BuildVector bool    `json:"build_vector"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if !validLabel(in.Label) || in.Text == "" {
		badRequest(c, "标签必须为 simple/complex，样本文本不能为空")
		return
	}
	textChanged := in.Text != x.Text // before Updates(&x) overwrites x.Text
	upd := map[string]any{"label": in.Label, "text": in.Text, "text_hash": model.TextKey(in.Text), "threshold": in.Threshold, "note": in.Note}
	if textChanged {
		upd["vector"] = nil
		upd["vector_dim"] = 0
	}
	if err := s.db.Model(&x).Updates(upd).Error; err != nil {
		if uniqueViolation(err) {
			fail(c, 409, "sample_exists", "同一标签下已有同一文本的样本")
			return
		}
		serverError(c, err)
		return
	}
	var buildErr string
	if s.eng.Route != nil {
		if in.BuildVector && textChanged {
			_, failed, err := s.eng.Route.BuildVectors(c.Request.Context(), []uint{x.ID})
			if buildReloadFailed(c, "route", err, nil) {
				return
			}
			if err != nil {
				buildErr = err.Error()
			} else if failed > 0 {
				buildErr = "向量构建失败，请检查向量服务"
			}
		} else if !s.reloadRuntimes(c, "route") {
			return
		}
	}
	s.db.First(&x, id)
	out := routeSampleView(&x)
	if buildErr != "" {
		out["build_error"] = buildErr
	}
	c.JSON(200, out)
}

func (s *Server) deleteRouteSample(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.db.Delete(&model.RouteSample{}, id).Error; err != nil {
		serverError(c, err)
		return
	}
	if !s.reloadRuntimes(c, "route") {
		return
	}
	c.JSON(200, gin.H{})
}

// batchRouteMax bounds one import so the key queries stay within every database's
// parameter limits (chunks of batchRouteChunk keys).
const (
	batchRouteMax   = 2000
	batchRouteChunk = 200
)

// batchRouteSamples imports samples. It is idempotent and bounded: duplicates inside
// the batch are dropped; existing keys are preloaded in chunks (never one query per
// item); rows are inserted with ON CONFLICT DO NOTHING so a concurrent import cannot
// fail the batch; every planned key is then re-read in chunks and classified from what
// the database holds, so "created" is the real insert count and a row lost to a
// concurrent identical insert is still built. If that re-read fails after the insert
// committed, the answer is a committed 503 (resubmitting the same batch is safe: it
// is idempotent and completes the classification and build).
func (s *Server) batchRouteSamples(c *gin.Context) {
	var in struct {
		Items []struct {
			Label string `json:"label"`
			Text  string `json:"text"`
			Note  string `json:"note"`
		} `json:"items"`
		BuildVector bool `json:"build_vector"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if len(in.Items) > batchRouteMax {
		badRequest(c, fmt.Sprintf("一次最多导入 %d 条样本", batchRouteMax))
		return
	}
	type planned struct {
		label, hash, text, note string
	}
	var plan []planned
	seen := map[string]bool{}
	skipped := 0
	for _, it := range in.Items {
		t := strings.TrimSpace(it.Text)
		if !validLabel(it.Label) || t == "" {
			continue
		}
		h := model.TextKey(t)
		if seen[it.Label+"|"+h] {
			skipped++
			continue
		}
		seen[it.Label+"|"+h] = true
		plan = append(plan, planned{label: it.Label, hash: h, text: t, note: strings.TrimSpace(it.Note)})
	}
	if len(plan) == 0 {
		if skipped > 0 {
			c.JSON(200, gin.H{"created": 0, "existing": 0, "skipped": skipped, "conflicts": 0})
			return
		}
		badRequest(c, "没有有效的样本")
		return
	}
	// Chunked set lookup of the batch's keys.
	load := func(keys []planned) (map[string]model.RouteSample, error) {
		out := map[string]model.RouteSample{}
		for start := 0; start < len(keys); start += batchRouteChunk {
			end := min(start+batchRouteChunk, len(keys))
			tuples := make([][]any, 0, end-start)
			for _, p := range keys[start:end] {
				tuples = append(tuples, []any{p.label, p.hash})
			}
			var rows []model.RouteSample
			if err := s.db.Select("id", "label", "text_hash", "note").Where("(label, text_hash) IN ?", tuples).Find(&rows).Error; err != nil {
				return nil, err
			}
			for _, r := range rows {
				out[r.Label+"|"+r.TextHash] = r
			}
		}
		return out, nil
	}
	before, err := load(plan)
	if err != nil {
		serverError(c, err)
		return
	}
	var rows []model.RouteSample
	var toInsert []planned
	for _, p := range plan {
		if _, ok := before[p.label+"|"+p.hash]; ok {
			continue // classified after the insert together with the racing rows
		}
		rows = append(rows, model.RouteSample{Label: p.label, Text: p.text, Note: p.note})
		toInsert = append(toInsert, p)
	}
	created := 0
	if len(rows) > 0 {
		res := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "label"}, {Name: "text_hash"}}, DoNothing: true}).CreateInBatches(&rows, batchRouteChunk)
		if res.Error != nil {
			serverError(c, res.Error)
			return
		}
		created = int(res.RowsAffected)
	}
	// Classify every planned key from the database: pre-existing rows and rows that
	// won a race against this insert alike.
	after, err := load(plan)
	if err != nil {
		if created == 0 {
			serverError(c, err)
			return
		}
		slog.Error("route sample batch: rows inserted but the re-read failed; resubmit the same batch to finish", "created", created, "err", err)
		c.JSON(503, gin.H{"code": "batch_reclassify_failed", "committed": true, "created": created,
			"error": fmt.Sprintf("已写入 %d 条样本，但写入后的核对失败，未执行向量构建；同一批次可直接重新提交（导入是幂等的，会补做核对与构建）: %v", created, err)})
		return
	}
	var existing, conflicts int
	var buildIDs []uint
	for _, p := range plan {
		cur, ok := after[p.label+"|"+p.hash]
		if !ok {
			continue // cannot happen after a successful insert; counted as neither
		}
		_, preexisting := before[p.label+"|"+p.hash]
		if cur.Note != p.note {
			conflicts++
			continue
		}
		buildIDs = append(buildIDs, cur.ID)
		if preexisting || created < len(toInsert) && !insertedByUs(rows, cur.ID) {
			existing++
		}
	}
	resp := gin.H{"created": created, "existing": existing, "skipped": skipped, "conflicts": conflicts}
	if in.BuildVector && s.eng.Route != nil && len(buildIDs) > 0 {
		built, failed, err := s.eng.Route.BuildVectors(c.Request.Context(), buildIDs)
		resp["built"], resp["failed"] = built, failed
		if buildReloadFailed(c, "route", err, resp) {
			return
		}
		if err != nil {
			resp["error"] = err.Error()
		}
	}
	c.JSON(200, resp)
}

// insertedByUs reports whether an id was returned for one of this request's inserts.
// Drivers only return ids for rows they inserted, so an id that appears in rows was
// created here; ids of skipped rows never appear.
func insertedByUs(rows []model.RouteSample, id uint) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) buildRouteVectors(c *gin.Context) {
	var in struct {
		IDs []uint `json:"ids"`
		All bool   `json:"all"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if s.eng.Route == nil {
		fail(c, 503, "unavailable", "路由引擎未启用")
		return
	}
	ids := in.IDs
	if in.All {
		ids = nil
	} else if len(ids) == 0 {
		badRequest(c, "请选择样本")
		return
	}
	built, failed, err := s.eng.Route.BuildVectors(c.Request.Context(), ids)
	resp := gin.H{"built": built, "failed": failed}
	if buildReloadFailed(c, "route", err, resp) {
		return
	}
	if err != nil {
		resp["error"] = err.Error()
	}
	c.JSON(200, resp)
}

func (s *Server) routePreview(c *gin.Context) {
	var in struct {
		Text     string `json:"text"`
		MsgCount int    `json:"msg_count"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Text) == "" {
		badRequest(c, "请输入文本")
		return
	}
	if s.eng.Route == nil {
		fail(c, 503, "unavailable", "路由引擎未启用")
		return
	}
	if in.MsgCount <= 0 {
		in.MsgCount = 1
	}
	res, err := s.eng.Route.Preview(c.Request.Context(), in.Text, in.MsgCount)
	if err != nil {
		fail(c, 400, "preview_failed", err.Error())
		return
	}
	sr := s.st.Get().SmartRoute
	gid := res.GroupID
	if gid == 0 {
		if res.Label == "complex" {
			gid = sr.ComplexGroupID
		} else {
			gid = sr.SimpleGroupID
		}
	}
	var mg model.ModelGroup
	s.db.First(&mg, gid)
	var topk any = []any{}
	if len(res.TopK) > 0 {
		_ = json.Unmarshal(res.TopK, &topk)
	}
	c.JSON(200, gin.H{"label": res.Label, "source": res.Source, "confidence": res.Confidence, "group_id": gid,
		"group_name": mg.Name, "models": orEmpty(mg.Models), "top_k": topk, "normalized": res.Normalized, "latency_ms": res.LatencyMs})
}

func decisionView(d *model.RouteDecision) gin.H {
	var topk any = []any{}
	if len(d.TopK) > 0 {
		_ = json.Unmarshal(d.TopK, &topk)
	}
	return gin.H{"id": d.ID, "request_id": d.RequestID, "label": d.Label, "source": d.Source, "confidence": d.Confidence,
		"selected_model": d.SelectedModel, "model_group": d.ModelGroup, "normalized_text": d.NormalizedText, "top_k": topk,
		"request_type": d.RequestType, "total_tokens": d.TotalTokens, "latency_ms": d.LatencyMs, "failed": d.Failed, "created_at": d.CreatedAt}
}

func (s *Server) listDecisions(c *gin.Context) {
	pg := paging(c)
	from, to := timeRange(c)
	q := s.db.Model(&model.RouteDecision{}).Where("created_at >= ? AND created_at <= ?", from, to)
	if v := c.Query("label"); v != "" {
		q = q.Where("label = ?", v)
	}
	if v := c.Query("source"); v != "" {
		q = q.Where("source = ?", v)
	}
	if v := c.Query("request_type"); v != "" {
		q = q.Where("request_type = ?", v)
	}
	if v := c.Query("model"); v != "" {
		q = q.Where("selected_model = ?", v)
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		l := likeEscape(v)
		q = q.Where("request_id LIKE ? ESCAPE '\\' OR selected_model LIKE ? ESCAPE '\\' OR normalized_text LIKE ? ESCAPE '\\'", l, l, l)
	}
	var total int64
	q.Count(&total)
	var rows []model.RouteDecision
	if err := q.Order("id DESC").Offset(pg.Offset).Limit(pg.Size).Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, decisionView(&rows[i]))
	}
	listResp(c, out, total)
}

func (s *Server) getDecision(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var d model.RouteDecision
	if err := s.db.First(&d, id).Error; err != nil {
		notFound(c)
		return
	}
	c.JSON(200, decisionView(&d))
}

type kv struct {
	Key    string `json:"key"`
	Count  int64  `json:"count"`
	Tokens int64  `json:"tokens"`
}

func (s *Server) routeStats(c *gin.Context) {
	from, to := timeRange(c)
	base := func() *gorm.DB {
		return s.db.Model(&model.RouteDecision{}).Where("created_at >= ? AND created_at <= ? AND source <> 'preview'", from, to)
	}
	var sum struct {
		Decisions   int64
		Failed      int64
		TotalTokens int64
		LatencyMs   int64
		Requests    int64
	}
	base().Select("COUNT(*) AS decisions, SUM(CASE WHEN failed THEN 1 ELSE 0 END) AS failed, COALESCE(SUM(total_tokens),0) AS total_tokens, COALESCE(SUM(latency_ms),0) AS latency_ms").Scan(&sum)
	// real requests = call logs with a route label in the window
	s.db.Model(&model.CallLog{}).Where("created_at >= ? AND created_at <= ? AND route_label <> ''", from, to).Count(&sum.Requests)
	var avg int64
	if sum.Decisions > 0 {
		avg = sum.TotalTokens / sum.Decisions
	}
	group := func(col string) []kv {
		var rows []kv
		base().Select(col + " AS key, COUNT(*) AS count, COALESCE(SUM(total_tokens),0) AS tokens").Group(col).Order("count DESC").Scan(&rows)
		if rows == nil {
			rows = []kv{}
		}
		return rows
	}
	// token buckets
	var buckets []kv
	for _, b := range []struct {
		name   string
		lo, hi int64
	}{{"0-500", 0, 500}, {"500-2K", 500, 2000}, {"2K-8K", 2000, 8000}, {"8K-32K", 8000, 32000}, {"32K+", 32000, 1 << 60}} {
		var n int64
		base().Where("total_tokens >= ? AND total_tokens < ?", b.lo, b.hi).Count(&n)
		buckets = append(buckets, kv{Key: b.name, Count: n})
	}
	c.JSON(200, gin.H{"decisions": sum.Decisions, "requests": sum.Requests, "failed": sum.Failed, "total_tokens": sum.TotalTokens,
		"avg_tokens": avg, "latency_ms": sum.LatencyMs, "by_label": group("label"), "by_source": group("source"),
		"by_model": group("selected_model"), "by_token_bucket": buckets, "from": from, "to": to, "generated_at": time.Now()})
}
