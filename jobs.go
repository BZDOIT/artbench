package main

// 任务队列（串行 + 重试）+ 历史（sqlite）+ 角色档案库（json）
// 迁移自 Python app.py 的 JOBS / new_job / worker_loop / run_job / save_history / list_history / delete_history / characters

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Job struct {
	ID         string         `json:"id"`
	Status     string         `json:"status"`
	CreatedStr string         `json:"created_str"`
	Meta       map[string]any `json:"-"`
	Logs       []string       `json:"log"`
	Files      []string       `json:"files"`
	URLs       []string       `json:"urls"`
	Error      string         `json:"error"`
	Elapsed    float64        `json:"elapsed"`

	started time.Time
	mu      sync.Mutex
}

func (j *Job) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	j.mu.Lock()
	j.Logs = append(j.Logs, msg)
	j.mu.Unlock()
	logf("[%s] %s", j.ID, msg)
}

var (
	jobs   = map[string]*Job{}
	jobsMu sync.Mutex
	taskQ  = make(chan string, 200)

	gLib   *StyleLib
	gCfg   *Config
	gAgnes *AgnesClient
	gOut   string // 素材根
)

// newJob
func newJob(meta map[string]any) *Job {
	now := time.Now()
	jid := fmt.Sprintf("%s-%04d", now.Format("150405"), now.UnixMilli()%10000)
	job := &Job{
		ID:         jid,
		Status:     "queued",
		CreatedStr: now.Format("2006-01-02 15:04:05"),
		Meta:       meta,
		Logs:       []string{},
		Files:      []string{},
		URLs:       []string{},
	}
	jobsMu.Lock()
	jobs[jid] = job
	if len(jobs) > 200 {
		for k, j := range jobs {
			if len(jobs) <= 100 {
				break
			}
			if j.Status == "done" || j.Status == "error" {
				delete(jobs, k)
			}
		}
	}
	jobsMu.Unlock()
	return job
}

func getJob(id string) *Job {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	return jobs[id]
}

func startWorkers() {
	go func() {
		for jid := range taskQ {
			job := getJob(jid)
			if job == nil {
				continue
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						logf("任务执行异常：%v", r)
					}
				}()
				runJob(job)
			}()
		}
	}()
}

func runJob(job *Job) {
	meta := job.Meta
	job.mu.Lock()
	job.Status = "running"
	job.started = time.Now()
	ag := getAgnes() // 任务全程用同一份客户端快照
	job.mu.Unlock()

	defer func() {
		if err := saveHistory(job); err != nil {
			logf("写历史失败：%v", err)
		}
	}()

	prompt := str(meta, "prompt")
	if prompt == "" {
		job.fail("提示词为空")
		return
	}
	// SC-021 动态解析：文本模型按上游规则（五构图轮替 / 背景净化 / 画幅守恒）
	// 生成单义执行提示词，替换 buildCardPrompt 里的固定排版要求段；失败回退固定模板不阻断出图
	if str(meta, "mode") == "card" && str(meta, "layout_id") == "SC-021" {
		if tm := textModel(); tm != "" {
			job.logf("SC-021 版式：调文本模型（%s）动态解析执行提示词…", tm)
			var st *Style
			if styleNo := str(meta, "style_no"); styleNo != "" {
				st = gLib.ByNo[styleNo]
			}
			jlog := func(m string) { job.logf("%s", m) }
			if dyn, err := sc021DynamicPrompt(str(meta, "card_theme"), st, jlog); err == nil && dyn != "" {
				fixed := str(meta, "sc021_fixed")
				if fixed != "" && strings.Contains(prompt, fixed) {
					prompt = strings.Replace(prompt, fixed, "排版要求："+period(dyn), 1)
					job.logf("SC-021 固定模板已替换为动态提示词（%d 字）", utf8.RuneCountInString(dyn))
				} else {
					job.logf("SC-021 动态提示词已生成，但没在提示词里定位到固定段，沿用固定模板")
				}
			} else {
				job.logf("SC-021 动态解析失败，回退固定模板：%v", err)
			}
		}
	}
	styleNo := str(meta, "style_no")
	charName := str(meta, "char_name")
	if charName == "" {
		charName = "角色"
	}
	count := clamp(asInt(meta["count"], 1), 1, 6)
	if count > 1 {
		nk := ag.KeyCount()
		if nk == 0 {
			nk = 1
		}
		par := clamp(count, 1, nk*PER_KEY_WORKERS)
		job.logf("开始请求 Agnes（模型 %s，%d 张候选 · 并发 %d 路，单张约 30–140 秒）…", ag.Model, count, par)
	} else {
		job.logf("开始请求 Agnes（模型 %s，单张约 30–140 秒）…", ag.Model)
	}

	jlog := func(m string) { job.logf("%s", m) }
	blobs, errs := ag.generateMany(prompt, count, str(meta, "size"), str(meta, "ratio"), strSlice(meta["refs"]), jlog)
	if len(blobs) == 0 {
		joined := ""
		for i, e := range errs {
			if i > 0 {
				joined += "；"
			}
			joined += e
			if len(joined) > 300 {
				joined = joined[:300]
			}
		}
		job.fail("候选全部失败：" + joined)
		return
	}
	if count > 1 && len(blobs) < count {
		job.logf("注意：要 %d 张，实际只拿到 %d 张", count, len(blobs))
	}

	day := time.Now().Format("20060102")
	dest := filepath.Join(gOut, day)
	_ = os.MkdirAll(dest, 0o755)
	stamp := time.Now().Format("150405")
	safe := safeName(stamp + "_" + styleNo + "_" + charName)

	var files, urls []string
	for i, b := range blobs {
		fname := fmt.Sprintf("%s_%02d.png", safe, i+1)
		if len(blobs) == 1 {
			fname = safe + ".png"
		}
		fp := filepath.Join(dest, fname)
		if err := os.WriteFile(fp, b, 0o644); err != nil {
			job.fail("写文件失败：" + err.Error())
			return
		}
		files = append(files, fp)
		urls = append(urls, "/out/"+day+"/"+urlEscape(fname))
	}

	job.mu.Lock()
	job.Files = files
	job.URLs = urls
	job.Elapsed = round1(time.Since(job.started).Seconds())
	job.Status = "done"
	job.mu.Unlock()
	job.logf("完成，用时 %.1f 秒，出图 %d 张", job.Elapsed, len(files))
}

func (j *Job) fail(msg string) {
	j.mu.Lock()
	started := j.started
	if !started.IsZero() {
		j.Elapsed = round1(time.Since(started).Seconds())
	}
	j.Status = "error"
	j.Error = msg
	j.mu.Unlock()
	j.logf("失败：%s", msg)
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

var urlEscapeRe = regexp.MustCompile(`[^A-Za-z0-9\-_.~]`)

// urlEscape 文件名 percent-encode（文件名含中文时必须，否则非浏览器客户端报 ascii 错）
func urlEscape(s string) string {
	out := ""
	for _, b := range []byte(s) {
		if b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' ||
			b == '-' || b == '_' || b == '.' || b == '~' {
			out += string(rune(b))
		} else {
			out += fmt.Sprintf("%%%02X", b)
		}
	}
	return out
}

func asInt(v any, def int) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		n := 0
		for _, r := range t {
			if r < '0' || r > '9' {
				return def
			}
			n = n*10 + int(r-'0')
		}
		return n
	}
	return def
}

func strSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
