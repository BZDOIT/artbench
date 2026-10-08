package main

// Agnes 生图客户端：key 池（多账号）+ 429 冷却换 key + 退避重试 + 按 key 并发上限
// 迁移自 Python app.py 的 KEY_COOLDOWN / cool_key / key_cooling / ktail / AgnesClient

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---- key 冷却表（发号时自动避开撞过 429 的 key）----

var (
	keyCooldown = map[string]time.Time{}
	kcdMu       sync.Mutex
)

func coolKey(k string, d time.Duration) {
	if k == "" {
		return
	}
	kcdMu.Lock()
	keyCooldown[k] = time.Now().Add(d)
	kcdMu.Unlock()
}

func keyCooling(k string) bool {
	kcdMu.Lock()
	defer kcdMu.Unlock()
	if until, ok := keyCooldown[k]; ok {
		if until.After(time.Now()) {
			return true
		}
		delete(keyCooldown, k) // 冷却到期，顺手清掉
	}
	return false
}

// keyCooldownLeft 只读查询：key 是否冷却中 + 剩余秒数（不动冷却表）
func keyCooldownLeft(k string) (bool, float64) {
	kcdMu.Lock()
	defer kcdMu.Unlock()
	if until, ok := keyCooldown[k]; ok && until.After(time.Now()) {
		return true, until.Sub(time.Now()).Seconds()
	}
	return false, 0
}

func ktail(k string) string {
	if k == "" {
		return "?"
	}
	if len(k) <= 4 {
		return "…" + k
	}
	return "…" + k[len(k)-4:]
}

// ---- 客户端 ----

type AgnesClient struct {
	Keys    []string
	Base    string
	Model   string
	Timeout int // 秒
	Retries int
	DefSize string
	OK      bool
	BuiltAt time.Time // 构建时间（设置变更即重建）

	rr    int
	rrMu  sync.Mutex
	sems  map[string]chan struct{} // 每把 key 一个并发闸
	semMu sync.Mutex
}

const PER_KEY_WORKERS = 3 // 实测单 key 3 路总耗时 ≈ 单张；再多反而排队变慢

func newAgnesClient(cfg *Config) *AgnesClient {
	env := loadEnv(cfg.EnvFile)
	keys := collectKeys(env)
	base := env["AGNES_BASE_URL"]
	if base == "" {
		base = env["OPENAI_BASE_URL"]
	}
	if base == "" {
		base = "https://apihub.agnes-ai.com/v1"
	}
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	model := env["AGNES_IMAGE_MODEL"]
	if model == "" {
		model = cfg.Model
	}
	return &AgnesClient{
		Keys:    keys,
		Base:    base,
		Model:   model,
		Timeout: cfg.Timeout,
		Retries: cfg.Retries,
		DefSize: cfg.Size,
		OK:      len(keys) > 0,
		sems:    map[string]chan struct{}{},
		BuiltAt: time.Now(),
	}
}

func (c *AgnesClient) KeyCount() int { return len(c.Keys) }

func (c *AgnesClient) sem(k string) chan struct{} {
	c.semMu.Lock()
	defer c.semMu.Unlock()
	ch, ok := c.sems[k]
	if !ok {
		ch = make(chan struct{}, PER_KEY_WORKERS)
		c.sems[k] = ch
	}
	return ch
}

// nextKey 轮询发号：优先发不在冷却期的 key；全冷却就照旧轮询，别空转
func (c *AgnesClient) nextKey() string {
	if len(c.Keys) == 0 {
		return ""
	}
	c.rrMu.Lock()
	defer c.rrMu.Unlock()
	for i := 0; i < len(c.Keys); i++ {
		k := c.Keys[c.rr%len(c.Keys)]
		c.rr++
		if !keyCooling(k) {
			return k
		}
	}
	k := c.Keys[c.rr%len(c.Keys)]
	c.rr++
	return k
}

// Generate 调生图，返回图片 bytes 列表。失败返回 error。
// size：档位（1K/2K/3K/4K，官方推荐，配合 ratio）或精确尺寸（1024x768，可能被服务端标准化）。
func (c *AgnesClient) generate(prompt, size, ratio string, refs []string, onLog func(string), key string) ([][]byte, error) {
	if !c.OK {
		return nil, fmt.Errorf("没读到 API key，检查 %s 里的 AGNES_API_KEY", gCfg.EnvFile)
	}
	useKey := key
	if useKey == "" {
		useKey = c.nextKey()
	}
	if size == "" {
		size = c.DefSize
	}
	url := c.Base + "/images/generations"
	body := map[string]any{
		"model":           c.Model,
		"prompt":          prompt,
		"size":            size,
		"response_format": "b64_json",
	}
	// 官方文档：档位式 size 配 ratio 用（1:1/3:4/4:3/16:9/9:16/2:3/3:2/21:9）；精确尺寸不需要 ratio
	if ratio != "" && isTierSize(size) {
		body["ratio"] = ratio
	}
	if len(refs) > 0 {
		// 实测：垫图字段放这里（SDK 的 extra_body 就是把它塞进请求体）
		body["image"] = refs
	}
	data, _ := json.Marshal(body)

	client := &http.Client{Timeout: time.Duration(c.Timeout) * time.Second}
	var lastErr string
	for attempt := 0; attempt <= c.Retries; attempt++ {
		req, err := http.NewRequest("POST", url, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+useKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == 200 {
			payload, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr != nil {
				lastErr = rerr.Error()
				continue
			}
			var pl map[string]any
			if jerr := json.Unmarshal(payload, &pl); jerr != nil {
				return nil, fmt.Errorf("响应解析失败：%v", jerr)
			}
			return extractImages(pl)
		}
		if err == nil {
			// HTTP 错误
			txt := ""
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if len(b) > 500 {
				txt = string(b[:500])
			} else {
				txt = string(b)
			}
			code := resp.StatusCode
			lastErr = fmt.Sprintf("HTTP %d %s", code, txt)
			logf("生图失败（第 %d 次）：%s", attempt+1, lastErr)
			if (code == 429 || code == 500 || code == 502 || code == 503 || code == 504) && attempt < c.Retries {
				// 429 = 这把 key 被限流 → 冷却 60 秒，立刻换下一把 key，不干等
				if code == 429 && len(c.Keys) > 1 {
					coolKey(useKey, 60*time.Second)
					nxt := c.nextKey()
					if !keyCooling(nxt) {
						if onLog != nil {
							onLog(fmt.Sprintf("key %s 撞限流（429），冷却 60 秒，换 %s 重试", ktail(useKey), ktail(nxt)))
						}
						useKey = nxt
						time.Sleep(1 * time.Second)
						continue
					}
					if onLog != nil {
						onLog(fmt.Sprintf("key %s 撞限流，其它 key 也在冷却中，退避等待…", ktail(useKey)))
					}
				}
				wait := 15 * (attempt + 1)
				if onLog != nil {
					onLog(fmt.Sprintf("接口限流/不稳（%d），%d 秒后重试…", code, wait))
				}
				time.Sleep(time.Duration(wait) * time.Second)
				continue
			}
			return nil, errors.New(lastErr)
		}
		// 网络异常
		lastErr = err.Error()
		logf("生图异常（第 %d 次）：%s", attempt+1, lastErr)
		if attempt < c.Retries {
			wait := 10 * (attempt + 1)
			if onLog != nil {
				onLog(fmt.Sprintf("网络异常，%d 秒后重试…", wait))
			}
			time.Sleep(time.Duration(wait) * time.Second)
			continue
		}
		return nil, errors.New(lastErr)
	}
	if lastErr == "" {
		lastErr = "未知错误"
	}
	return nil, errors.New(lastErr)
}

// isTierSize 档位式尺寸（1K/2K/3K/4K）—— 官方推荐写法，配合 ratio；精确尺寸如 1024x768 返回 false
func isTierSize(s string) bool {
	switch strings.ToUpper(s) {
	case "1K", "2K", "3K", "4K":
		return true
	}
	return false
}

// extractImages 响应 → 图片 bytes（b64_json 或 url）
func extractImages(payload map[string]any) ([][]byte, error) {
	var out [][]byte
	items, _ := payload["data"].([]any)
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if b64, ok := m["b64_json"].(string); ok && b64 != "" {
			if b, err := base64.StdEncoding.DecodeString(b64); err == nil {
				out = append(out, b)
				continue
			}
		}
		if u, ok := m["url"].(string); ok && u != "" {
			cli := &http.Client{Timeout: 120 * time.Second}
			if resp, err := cli.Get(u); err == nil {
				if b, err := io.ReadAll(resp.Body); err == nil {
					out = append(out, b)
				}
				resp.Body.Close()
			}
		}
	}
	if len(out) == 0 {
		snippet, _ := json.Marshal(payload)
		s := string(snippet)
		if len(s) > 400 {
			s = s[:400]
		}
		return nil, fmt.Errorf("响应里没有图片数据：%s", s)
	}
	return out, nil
}

// GenerateMany 一次要 N 张候选，按 key 池平摊。
// 接口的 n 硬限制为 1（传 4 直接 400），多张只能发多次请求。
// 返回 (blobs, errs)。
func (c *AgnesClient) generateMany(prompt string, count int, size, ratio string, refs []string, onLog func(string)) ([][]byte, []string) {
	count = clamp(count, 1, 6)
	if !c.OK {
		return nil, []string{fmt.Sprintf("没读到 API key，检查 %s 里的 AGNES_API_KEY", gCfg.EnvFile)}
	}
	if count == 1 {
		blobs, err := c.generate(prompt, size, ratio, refs, onLog, "")
		if err != nil {
			return nil, []string{err.Error()}
		}
		return blobs, nil
	}

	nk := len(c.Keys)
	if nk == 0 {
		nk = 1
	}
	workers := clamp(count, 1, nk*PER_KEY_WORKERS)
	if onLog != nil && nk > 1 {
		onLog(fmt.Sprintf("key 池 %d 把 → 并发 %d 路（单 key 上限 %d）", nk, workers, PER_KEY_WORKERS))
	}

	var (
		mu    sync.Mutex
		blobs [][]byte
		errs  []string
		wg    sync.WaitGroup
	)
	for i := 1; i <= count; i++ {
		idx := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			useKey := c.nextKey() // 先抢号再执行：相邻请求落到不同 key 上，负载自然平摊
			sem := c.sem(useKey)
			sem <- struct{}{}
			b, err := c.generate(prompt, size, ratio, refs, onLog, useKey)
			<-sem
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Sprintf("候选 %d 失败：%s", idx, err.Error()))
				if onLog != nil {
					msg := err.Error()
					if len(msg) > 120 {
						msg = msg[:120]
					}
					onLog(fmt.Sprintf("候选 %d 失败：%s", idx, msg))
				}
				return
			}
			blobs = append(blobs, b...)
			if onLog != nil {
				onLog(fmt.Sprintf("候选 %d 完成（%d/%d）", idx, idx, count))
			}
		}()
	}
	wg.Wait()
	return blobs, errs
}
