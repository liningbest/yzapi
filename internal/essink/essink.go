// Package essink ships call bodies to Elasticsearch in bulk, off the request path.
package essink

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// Record is one document to index.
type Record struct {
	RequestID         string    `json:"request_id"`
	Timestamp         time.Time `json:"@timestamp"`
	UserID            uint      `json:"user_id"`
	Username          string    `json:"username"`
	GroupName         string    `json:"group_name"`
	APIKeyName        string    `json:"api_key_name"`
	AccountName       string    `json:"account_name"`
	Provider          string    `json:"provider"`
	RequestModel      string    `json:"request_model"`
	UpstreamModel     string    `json:"upstream_model"`
	APIType           string    `json:"api_type"`
	Result            string    `json:"result"`
	StatusCode        int       `json:"status_code"`
	LatencyMs         int64     `json:"latency_ms"`
	TotalTokens       int64     `json:"total_tokens"`
	ClientIP          string    `json:"client_ip"`
	Request           string    `json:"request"`
	Response          string    `json:"response"`
	RequestTruncated  bool      `json:"request_truncated"`
	ResponseTruncated bool      `json:"response_truncated"`
}

type Sink struct {
	st      *settings.Store
	hc      *http.Client
	ch      chan *Record
	stop    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	qBytes  int64
	dropped atomic.Int64
	lastOK  atomic.Pointer[time.Time]
	failing atomic.Pointer[time.Time]
}

func New(st *settings.Store) *Sink {
	s := &Sink{st: st, hc: &http.Client{Timeout: 30 * time.Second}, ch: make(chan *Record, 4096), stop: make(chan struct{})}
	s.wg.Add(1)
	go s.loop()
	return s
}

// Enabled reports whether bodies should be captured.
func (s *Sink) Enabled() bool {
	es := s.st.Get().Elasticsearch
	return es.Enabled && es.URL != ""
}

// Limits returns request/response body caps in bytes.
func (s *Sink) Limits() (int, int) {
	es := s.st.Get().Elasticsearch
	return max(es.RequestKB, 1) * 1024, max(es.ResponseKB, 1) * 1024
}

// Record enqueues a document; never blocks.
func (s *Sink) Record(l *model.CallLog, reqBody, respBody []byte, reqTrunc, respTrunc bool) {
	if !s.Enabled() {
		return
	}
	r := &Record{RequestID: l.RequestID, Timestamp: l.CreatedAt, UserID: l.UserID, Username: l.Username, GroupName: l.GroupName,
		APIKeyName: l.APIKeyName, AccountName: l.AccountName, Provider: l.Provider, RequestModel: l.RequestModel,
		UpstreamModel: l.UpstreamModel, APIType: l.APIType, Result: l.Result, StatusCode: l.StatusCode, LatencyMs: l.LatencyMs,
		TotalTokens: l.TotalTokens, ClientIP: l.ClientIP, Request: string(reqBody), Response: string(respBody),
		RequestTruncated: reqTrunc, ResponseTruncated: respTrunc}
	select {
	case s.ch <- r:
		s.mu.Lock()
		s.qBytes += int64(len(reqBody) + len(respBody))
		s.mu.Unlock()
	default:
		s.dropped.Add(1)
	}
}

// Status is the sink's health for the settings page.
type Status struct {
	Configured   bool       `json:"configured"`
	QueueCount   int        `json:"queue_count"`
	QueueBytes   int64      `json:"queue_bytes"`
	Dropped      int64      `json:"dropped"`
	LastSuccess  *time.Time `json:"last_success_at"`
	FailingSince *time.Time `json:"failing_since"`
}

func (s *Sink) Status() Status {
	s.mu.Lock()
	qb := s.qBytes
	s.mu.Unlock()
	return Status{Configured: s.Enabled(), QueueCount: len(s.ch), QueueBytes: qb, Dropped: s.dropped.Load(), LastSuccess: s.lastOK.Load(), FailingSince: s.failing.Load()}
}

func authHeader(req *http.Request, cfg settings.Elasticsearch) {
	switch cfg.AuthType {
	case "basic":
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cfg.Username+":"+cfg.Password)))
	default:
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "ApiKey "+cfg.APIKey)
		}
	}
}

// Test pings the cluster and returns its version.
func (s *Sink) Test(ctx context.Context, cfg settings.Elasticsearch) (string, error) {
	url := strings.TrimRight(cfg.URL, "/")
	if url == "" {
		return "", errors.New("地址为空")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/", nil)
	if err != nil {
		return "", err
	}
	authHeader(req, cfg)
	resp, err := s.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var info struct {
		Version struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if err := json.Unmarshal(raw, &info); err != nil || info.Version.Number == "" {
		return "", errors.New("响应不是 Elasticsearch")
	}
	major := strings.SplitN(info.Version.Number, ".", 2)[0]
	if major != "8" && major != "9" {
		return info.Version.Number, fmt.Errorf("Elasticsearch 版本不受支持 (%s)，需要 8.x 或 9.x", info.Version.Number)
	}
	return info.Version.Number, nil
}

func (s *Sink) loop() {
	defer s.wg.Done()
	batch := make([]*Record, 0, 200)
	ticker := time.NewTicker(2 * time.Second)
	cleanup := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	defer cleanup.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.bulk(batch)
		var n int64
		for _, r := range batch {
			n += int64(len(r.Request) + len(r.Response))
		}
		s.mu.Lock()
		s.qBytes -= n
		if s.qBytes < 0 {
			s.qBytes = 0
		}
		s.mu.Unlock()
		batch = batch[:0]
	}
	for {
		select {
		case r := <-s.ch:
			batch = append(batch, r)
			if len(batch) >= 200 {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-cleanup.C:
			s.retention()
		case <-s.stop:
			for {
				select {
				case r := <-s.ch:
					batch = append(batch, r)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (s *Sink) indexName(cfg settings.Elasticsearch, t time.Time) string {
	return fmt.Sprintf("%s-calls-%s", cfg.IndexPrefix, t.UTC().Format("2006.01.02"))
}

func (s *Sink) bulk(batch []*Record) {
	cfg := s.st.Get().Elasticsearch
	if !cfg.Enabled || cfg.URL == "" {
		return
	}
	var buf bytes.Buffer
	for _, r := range batch {
		meta, _ := json.Marshal(map[string]any{"index": map[string]any{"_index": s.indexName(cfg, r.Timestamp), "_id": r.RequestID}})
		doc, _ := json.Marshal(r)
		buf.Write(meta)
		buf.WriteByte('\n')
		buf.Write(doc)
		buf.WriteByte('\n')
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.URL, "/")+"/_bulk", &buf)
	if err != nil {
		s.markFail()
		return
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	authHeader(req, cfg)
	resp, err := s.hc.Do(req)
	if err != nil {
		s.markFail()
		s.dropped.Add(int64(len(batch)))
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		s.markFail()
		s.dropped.Add(int64(len(batch)))
		return
	}
	now := time.Now()
	s.lastOK.Store(&now)
	s.failing.Store(nil)
}

func (s *Sink) markFail() {
	if s.failing.Load() == nil {
		now := time.Now()
		s.failing.Store(&now)
	}
}

// retention deletes daily indices older than the configured retention.
func (s *Sink) retention() {
	cfg := s.st.Get().Elasticsearch
	if !cfg.Enabled || cfg.URL == "" || cfg.RetentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays)
	for i := 0; i < 30; i++ {
		day := cutoff.AddDate(0, 0, -i)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(cfg.URL, "/")+"/"+s.indexName(cfg, day), nil)
		if err == nil {
			authHeader(req, cfg)
			if resp, err := s.hc.Do(req); err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}
		cancel()
	}
}

func (s *Sink) Close(ctx context.Context) {
	close(s.stop)
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
