// Command loadgen fires concurrent chat requests at an OpenAI-compatible endpoint and reports latency.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/v1/chat/completions", "endpoint")
	key := flag.String("key", "sk-mock", "api key")
	model := flag.String("model", "mini", "model")
	conc := flag.Int("c", 64, "concurrency")
	total := flag.Int("n", 2000, "total requests")
	stream := flag.Bool("stream", false, "use streaming")
	flag.Parse()

	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"benchmark hello world"}],"stream":%v}`, *model, *stream)
	tr := &http.Transport{MaxIdleConnsPerHost: *conc * 2, MaxIdleConns: *conc * 2}
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}

	var (
		wg      sync.WaitGroup
		next    atomic.Int64
		ok, bad atomic.Int64
		mu      sync.Mutex
		lat     []time.Duration
		ttfb    []time.Duration
	)
	start := time.Now()
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if int(next.Add(1)) > *total {
					return
				}
				t0 := time.Now()
				req, _ := http.NewRequest(http.MethodPost, *url, bytes.NewReader([]byte(body)))
				req.Header.Set("Authorization", "Bearer "+*key)
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				if err != nil {
					bad.Add(1)
					continue
				}
				var first time.Duration
				if *stream {
					rd := bufio.NewReader(resp.Body)
					for {
						line, err := rd.ReadBytes('\n')
						if first == 0 && len(bytes.TrimSpace(line)) > 0 {
							first = time.Since(t0)
						}
						if err != nil {
							break
						}
					}
				} else {
					_, _ = io.Copy(io.Discard, resp.Body)
					first = time.Since(t0)
				}
				resp.Body.Close()
				d := time.Since(t0)
				if resp.StatusCode == 200 {
					ok.Add(1)
				} else {
					bad.Add(1)
				}
				mu.Lock()
				lat = append(lat, d)
				ttfb = append(ttfb, first)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	sort.Slice(ttfb, func(i, j int) bool { return ttfb[i] < ttfb[j] })
	p := func(s []time.Duration, q float64) time.Duration {
		if len(s) == 0 {
			return 0
		}
		return s[int(float64(len(s)-1)*q)]
	}
	fmt.Printf("requests=%d ok=%d failed=%d concurrency=%d elapsed=%s\n", *total, ok.Load(), bad.Load(), *conc, elapsed.Round(time.Millisecond))
	fmt.Printf("throughput: %.0f req/s\n", float64(ok.Load())/elapsed.Seconds())
	fmt.Printf("latency  p50=%s p90=%s p99=%s max=%s\n", p(lat, .5), p(lat, .9), p(lat, .99), p(lat, 1))
	if *stream {
		fmt.Printf("ttfb     p50=%s p90=%s p99=%s\n", p(ttfb, .5), p(ttfb, .9), p(ttfb, .99))
	}
}
