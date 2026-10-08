package main

// Agnes 视频客户端 + 帧工具 + 拼接（迁移自 Python app.py 的 VideoClient / find_exe / probe_wh / norm_frame / _ff / concat_shots）
//
// 视频是「服务端异步任务」：创建 → video_id → 轮询 → 下载。
// video_id 落库（filmstore.go），重启后 resume 直接续轮询，不浪费额度。

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ---- ffmpeg / ffprobe ----

var (
	FFMPEG  string
	FFPROBE string
)

// findFFmpeg 找「功能完整」的可执行文件：config 指定 > winget 固定路径 > winget Packages > PATH
func findFFmpeg(name, override string) string {
	if override != "" {
		if st, err := os.Stat(override); err == nil && !st.IsDir() {
			return override
		}
	}
	local := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WinGet")
	cands := []string{
		filepath.Join(local, "Links", name+".EXE"),
		filepath.Join(local, "Links", name+".exe"),
	}
	pkgs, _ := filepath.Glob(filepath.Join(local, "Packages", "Gyan.FFmpeg*", "**", "bin", name+".exe"))
	cands = append(cands, pkgs...)
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// probeWH 用 ffprobe 拿宽高（失败返回 0,0）
func probeWH(path string) (int, int) {
	if FFPROBE == "" {
		return 0, 0
	}
	ctx := exec.Command(FFPROBE, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=p=0", path)
	out, err := ctx.Output()
	if err != nil {
		return 0, 0
	}
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ",")
	if len(parts) < 2 {
		return 0, 0
	}
	var w, h int
	fmt.Sscanf(parts[0], "%d", &w)
	fmt.Sscanf(parts[1], "%d", &h)
	return w, h
}

var normSafeRe = regexp.MustCompile(`[^0-9A-Za-z_-]`)

// normFrame 把关键帧图归一化到 Agnes 接受的尺寸（宽高各 256–5760）后返回 bytes。
// 产物落 data/_norm/，按「源文件名+mtime」命名复用。
func normFrame(path string, logFn func(string)) ([]byte, error) {
	w, h := probeWH(path)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if w == 0 || h == 0 {
		return raw, nil
	}
	if w >= 256 && w <= 5760 && h >= 256 && h <= 5760 {
		return raw, nil
	}
	if FFMPEG == "" {
		return nil, fmt.Errorf("关键帧图 %dx%d 超出 256–5760 范围，但没找到 ffmpeg 无法自动修正", w, h)
	}
	stem := normSafeRe.ReplaceAllString(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), "_")
	if len(stem) > 40 {
		stem = stem[:40]
	}
	if stem == "" {
		stem = "frame"
	}
	var stamp int64
	if st, err := os.Stat(path); err == nil {
		stamp = st.ModTime().UnixNano()
	}
	normDir := filepath.Join(dataDir(), "_norm")
	_ = os.MkdirAll(normDir, 0o755)
	tmp := filepath.Join(normDir, fmt.Sprintf("%s_%d.jpg", stem, stamp))
	if _, err := os.Stat(tmp); os.IsNotExist(err) {
		vf := "scale=512:-2"
		if w > h {
			vf = "scale=-2:512"
		}
		cmd := exec.Command(FFMPEG, "-y", "-loglevel", "error", "-i", path, "-vf", vf, tmp)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("ffmpeg 归一化关键帧失败：%s", string(out)[:min2(200, len(out))])
		}
	}
	nw, nh := probeWH(tmp)
	if logFn != nil {
		logFn(fmt.Sprintf("关键帧尺寸 %dx%d 不在 256–5760 内，已归一化为 %dx%d", w, h, nw, nh))
	}
	return os.ReadFile(tmp)
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---- 视频常量 ----

var ASPECTS = []string{"16:9", "9:16", "4:3", "3:4", "1:1", "21:9"}

var ASPECT_HINT = map[string]string{
	"16:9": "横屏 · 常规视频号 / B 站",
	"9:16": "竖屏 · 抖音 / 视频号 / 快手",
	"4:3":  "复古方横屏",
	"3:4":  "竖版方屏 · 小红书",
	"1:1":  "正方形 · 朋友圈 / 封面",
	"21:9": "超宽 · 电影感",
}

// 图片接口支持的 8 种比例（官方文档：size 档位 + ratio 配合）
var IMAGE_RATIOS = []string{"1:1", "3:4", "4:3", "16:9", "9:16", "2:3", "3:2", "21:9"}
var IMAGE_SIZES = []string{"1K", "2K", "3K", "4K"}

const REF_MAX = 5 // 垫图上限（flash 免费档取保守值）

var CLIP_MODES = map[string]string{"reference": "垫图 + 提示词", "text": "纯提示词（不用图）"}

var CAMERA_MAP = map[string]string{
	"推":  "镜头缓慢平稳推进。",
	"拉":  "镜头缓慢平稳拉远。",
	"摇":  "镜头水平缓慢摇移。",
	"移":  "镜头平行横移跟拍。",
	"跟拍": "镜头跟随主体运动。",
	"固定": "镜头固定不动。",
	"环绕": "镜头围绕主体缓慢环绕。",
}

var MODE_LABEL = map[string]string{"cut": "硬切", "dual": "双关键帧"}

func aspectOK(a string) bool {
	for _, x := range ASPECTS {
		if x == a {
			return true
		}
	}
	return false
}

// ---- 视频客户端（每把 key 一个实例，串行）----

type VideoClient struct {
	Key     string
	Pool    []string
	Base    string
	Model   string
	PollURL string
	OK      bool

	borrowRR int
	borrowMu sync.Mutex
}

func newVideoClients(s *Settings) []*VideoClient {
	base := s.BaseURL
	pollURL := base
	if strings.HasSuffix(pollURL, "/v1") {
		pollURL = pollURL[:len(pollURL)-3]
	}
	pollURL += "/agnesapi"
	model := s.VideoModel
	if model == "" {
		model = "agnes-video-2.5-flash"
	}
	var out []*VideoClient
	for _, k := range s.Keys {
		out = append(out, &VideoClient{
			Key: k, Pool: s.Keys, Base: base, Model: model, PollURL: pollURL, OK: k != "",
		})
	}
	if len(out) == 0 {
		out = append(out, &VideoClient{Pool: s.Keys, Base: base, Model: model, PollURL: pollURL})
	}
	return out
}

// altKey 从池里挑一把不在冷却中的别的 key
func (c *VideoClient) altKey() string {
	c.borrowMu.Lock()
	defer c.borrowMu.Unlock()
	var cands []string
	for _, k := range c.Pool {
		if k != c.Key && !keyCooling(k) {
			cands = append(cands, k)
		}
	}
	if len(cands) == 0 {
		return ""
	}
	k := cands[c.borrowRR%len(cands)]
	c.borrowRR++
	return k
}

// req 429/503 长退避（20/40/…/100s）；429 时把 key 冷却并借干净的立刻重试
func (c *VideoClient) req(url string, data any, method string, timeout int, tries int, label string, onLog func(string)) (map[string]any, error) {
	var last string
	curKey := c.Key
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	for i := 0; i < tries; i++ {
		var body io.Reader
		if data != nil {
			b, _ := json.Marshal(data)
			body = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, url, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+curKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				var m map[string]any
				if json.Unmarshal(b, &m) != nil {
					return nil, fmt.Errorf("响应解析失败：%s", truncateStr(string(b), 200))
				}
				return m, nil
			}
			last = fmt.Sprintf("HTTP %d %s", resp.StatusCode, truncateStr(string(b), 400))
		} else {
			last = err.Error()
		}
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		if err == nil && (code == 429 || code == 500 || code == 502 || code == 503 || code == 504) && i < tries-1 {
			if code == 429 && curKey == c.Key {
				coolKey(curKey, 60*time.Second)
				alt := c.altKey()
				if alt != "" {
					if onLog != nil {
						onLog(fmt.Sprintf("[%s] key %s 撞限流（429），借 %s 重试", label, ktail(curKey), ktail(alt)))
					}
					curKey = alt
					time.Sleep(2 * time.Second)
					continue
				}
			}
			wait := min2(20*(i+1), 100)
			if onLog != nil {
				onLog(fmt.Sprintf("[%s] 接口限流/队列满（%d），%d 秒后重试（%d/%d）", label, code, wait, i+1, tries))
			}
			logf("视频 [%s] %s", label, last)
			time.Sleep(time.Duration(wait) * time.Second)
			continue
		}
		if err != nil && i < tries-1 {
			if onLog != nil {
				onLog(fmt.Sprintf("[%s] 网络异常，8 秒后重试（%d/%d）", label, i+1, tries))
			}
			time.Sleep(8 * time.Second)
			continue
		}
		if err == nil && resp != nil && resp.StatusCode == 200 {
			continue
		}
		return nil, fmt.Errorf("%s", last)
	}
	return nil, fmt.Errorf("%s", last)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Create 创建视频任务，返回 video_id。mode: text / keyframe / reference
func (c *VideoClient) Create(prompt, mode string, seconds int, first, last []byte, refs [][]byte, aspect string, onLog func(string), tries, timeout int) (string, error) {
	if !c.OK {
		return "", fmt.Errorf("没读到 API key，先在设置里加 key")
	}
	if !aspectOK(aspect) {
		aspect = "16:9"
	}
	body := map[string]any{
		"model":        c.Model,
		"prompt":       prompt,
		"mode":         mode,
		"seconds":      fmt.Sprintf("%d", seconds), // ⚠️ 必须字符串
		"size":         "720P",
		"aspect_ratio": aspect,
	}
	if mode == "reference" {
		var imgs []string
		for _, b := range refs {
			if len(b) > 0 {
				imgs = append(imgs, base64.StdEncoding.EncodeToString(b))
			}
		}
		if len(imgs) == 0 {
			return "", fmt.Errorf("垫图模式至少要有一张参考图")
		}
		body["images"] = imgs
	} else {
		if first != nil {
			body["first_frame"] = base64.StdEncoding.EncodeToString(first)
		}
		if last != nil {
			body["last_frame"] = base64.StdEncoding.EncodeToString(last)
		}
	}
	resp, err := c.req(c.Base+"/videos", body, "POST", timeout, tries, "create", onLog)
	if err != nil {
		return "", err
	}
	vid := str(resp, "video_id")
	if vid == "" {
		vid = str(resp, "task_id")
	}
	if vid == "" {
		vid = str(resp, "id")
	}
	if vid == "" {
		b, _ := json.Marshal(resp)
		return "", fmt.Errorf("创建视频任务没拿到 video_id：%s", truncateStr(string(b), 300))
	}
	return vid, nil
}

// Poll 查任务状态。⚠️ 必须带 model_name，2.5 的 keyframe/reference 不带会失败。
func (c *VideoClient) Poll(videoID string) (map[string]any, error) {
	q := fmt.Sprintf("%s?video_id=%s&model_name=%s", c.PollURL, url.QueryEscape(videoID), url.QueryEscape(c.Model))
	return c.req(q, nil, "GET", 60, 4, "poll", nil)
}

// Wait 阻塞轮询到完成/失败。4 秒一次（更密会 429 too many video status queries）。
func (c *VideoClient) Wait(videoID string, onLog func(string), onProg func(stage string, prog any, secs int), timeoutSec int) (map[string]any, error) {
	t0 := time.Now()
	interval := 4 * time.Second
	for time.Since(t0) < time.Duration(timeoutSec)*time.Second {
		time.Sleep(interval)
		d, err := c.Poll(videoID)
		if err != nil {
			if onLog != nil {
				onLog(fmt.Sprintf("轮询出错（%s），继续等", truncateStr(err.Error(), 80)))
			}
			continue
		}
		st := strings.ToLower(str(d, "status"))
		if onProg != nil {
			secs := int(time.Since(t0).Seconds())
			onProg(st, d["progress"], secs)
		}
		if st == "completed" || st == "succeeded" || st == "success" {
			return d, nil
		}
		if st == "failed" || st == "error" {
			b, _ := json.Marshal(d)
			return nil, fmt.Errorf("服务端任务失败：%s", truncateStr(string(b), 300))
		}
	}
	return nil, fmt.Errorf("轮询超时（%d 秒）", timeoutSec)
}

// Download 结果 URL 在响应顶层 url 字段
func (c *VideoClient) Download(data map[string]any, dest string, onLog func(string)) error {
	u := str(data, "url")
	if u == "" {
		if md, ok := data["metadata"].(map[string]any); ok {
			u = str(md, "url")
		}
	}
	if u == "" {
		vid := str(data, "video_id")
		if vid == "" {
			vid = str(data, "id")
		}
		if vid != "" {
			prefix := c.Model
			if i := strings.LastIndex(prefix, "-flash"); i > 0 {
				prefix = prefix[:i]
			}
			u = fmt.Sprintf("https://platform-outputs.agnes-ai.space/videos/%s/%s.mp4", prefix, vid)
		}
	}
	if u == "" {
		b, _ := json.Marshal(data)
		return fmt.Errorf("完成但响应里没有 url：%s", truncateStr(string(b), 300))
	}
	if onLog != nil {
		onLog("下载成片…")
	}
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	cli := &http.Client{Timeout: 300 * time.Second}
	resp, err := cli.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("下载失败：HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	written, err := io.Copy(bufio.NewWriter(f), resp.Body)
	if err != nil {
		return err
	}
	logf("下载完成：%s（%.1f MB）", dest, float64(written)/1024/1024)
	return nil
}
