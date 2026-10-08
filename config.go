package main

// 配置：config.ini（exe 同目录）+ .env（API keys，永不出后端）
// 迁移自 Python app.py 的 Config / load_env / collect_keys

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// APP_DIR：exe 所在目录（便携式，跟 Python 版一个思路）
var APP_DIR string

func init() {
	exe, err := os.Executable()
	if err != nil {
		APP_DIR, _ = os.Getwd()
		return
	}
	APP_DIR = filepath.Dir(exe)
}

type Config struct {
	StyleLibPath string // 风格库目录
	EnvFile      string // .env 路径（API keys）
	OutputDir    string // 素材根（空 = APP_DIR/out）
	Model        string
	Size         string
	Timeout      int // 秒
	Retries      int
	DefaultCount int
}

func loadConfig() *Config {
	c := &Config{
		StyleLibPath: `D:\skills\handdraw-style-prompter`,
		EnvFile:      `D:\skills\.env`,
		Model:        "agnes-image-2.1-flash",
		Size:         "1024x768",
		Timeout:      360,
		Retries:      3,
		DefaultCount: 3,
	}
	ini := readIni(filepath.Join(APP_DIR, "config.ini"))
	paths := ini["paths"]
	image := ini["image"]
	if v := strings.TrimSpace(paths["style_lib"]); v != "" {
		c.StyleLibPath = v
	}
	if v := strings.TrimSpace(paths["env_file"]); v != "" {
		c.EnvFile = v
	}
	if v := strings.TrimSpace(paths["output_dir"]); v != "" {
		c.OutputDir = v
	}
	if v := strings.TrimSpace(image["model"]); v != "" {
		c.Model = v
	}
	if v := strings.TrimSpace(image["size"]); v != "" {
		c.Size = v
	}
	if v := strings.TrimSpace(image["timeout_seconds"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Timeout = n
		}
	}
	if v := strings.TrimSpace(image["retry_times"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.Retries = n
		}
	}
	if v := strings.TrimSpace(image["default_count"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.DefaultCount = clamp(n, 1, 6)
		}
	}
	return c
}

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// readIni 极简 INI 解析：[section] + key = value，只读。
func readIni(path string) map[string]map[string]string {
	out := map[string]map[string]string{"": {}}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sec := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sec = strings.TrimSpace(line[1 : len(line)-1])
			if _, ok := out[sec]; !ok {
				out[sec] = map[string]string{}
			}
			continue
		}
		if i := strings.Index(line, "="); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.TrimSpace(line[i+1:])
			out[sec][k] = v
		}
	}
	return out
}

// outDir 素材根目录（可被 config 改；M1 固定启动时定，M2 加运行时改）
func (c *Config) outDir() string {
	if c.OutputDir != "" {
		return c.OutputDir
	}
	return filepath.Join(APP_DIR, "out")
}

func dataDir() string {
	return filepath.Join(APP_DIR, "data")
}

// ---- 日志 ----

var logFile *os.File

func initLog() {
	_ = os.MkdirAll(dataDir(), 0o755)
	f, err := os.OpenFile(filepath.Join(dataDir(), "工作台日志.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	logFile = f
	log.SetFlags(0)
}

func logf(format string, args ...any) {
	line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	log.Println(line)
	if logFile != nil {
		fmt.Fprintf(logFile, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	}
}

// ---- .env ----

// loadEnv 极简 .env 解析（只读，不回显）。UTF-8 BOM 兼容。
func loadEnv(path string) map[string]string {
	env := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return env
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, "=")
		if i < 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.TrimSpace(line[i+1:])
		v = strings.Trim(v, `"'`)
		env[k] = v
	}
	return env
}

// collectKeys 从 .env 里收集所有 Agnes key，支持多账号。
// 命名：AGNES_API_KEY（1 号）、AGNES_API_KEY_1 … _99；兜底 OPENAI_API_KEY。
// 保序 + 去重。
func collectKeys(env map[string]string) []string {
	var found []string
	seen := map[string]bool{}
	push := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			found = append(found, v)
		}
	}
	push(env["AGNES_API_KEY"])
	for i := 1; i < 100; i++ {
		push(env[fmt.Sprintf("AGNES_API_KEY_%d", i)])
	}
	// 兜底：AGNES_API_KEY_xxx 这类非纯数字后缀（map 遍历无序，但同一把 key 有 seen 去重，顺序无实质影响）
	for k, v := range env {
		if strings.HasPrefix(k, "AGNES_API_KEY_") {
			push(v)
		}
	}
	if len(found) == 0 {
		push(env["OPENAI_API_KEY"])
	}
	return found
}
