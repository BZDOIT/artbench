package main

// M4：风格库一键热更新 + 应用自更新
//
// 风格库热更新：从上游 GitHub 拉 master zip，把桌面版真正消费的文件覆盖进本地
//   （styles.json / colors.json / layouts.json / 版式提示词 / images/），然后热重载。
//   桌面版只读这些数据文件，不需要打 Python 脚本补丁 —— 天然比命令行版省心。
// 应用自更新：检查 update_url 的 latest.json → 下载新 exe → 改名旧版 → 换新 → 重启。

import (
	"archive/zip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const APP_VERSION = "1.0.0"

var httpCli = &http.Client{Timeout: 120 * time.Second}

const UPSTREAM_ZIP = "https://codeload.github.com/yang0/handraw-style/zip/refs/heads/master"

// 上游版本文件的三个通道（大陆网络下 raw 经常被墙，依次回退）。
// 版本号在 version.json（上游 frontmatter 没有 version 字段），SKILL.md 只作兜底。
var upstreamVersionURLs = []string{
	"https://api.github.com/repos/yang0/handraw-style/contents/version.json?ref=master",
	"https://raw.githubusercontent.com/yang0/handraw-style/master/version.json",
	"https://cdn.jsdelivr.net/gh/yang0/handraw-style@master/version.json",
}

// fetchUpstreamText 依次尝试三个通道拿上游文本文件
func fetchUpstreamText(urls []string) (string, error) {
	var lastErr error
	for _, u := range urls {
		resp, err := httpCli.Get(u)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("HTTP %d (%s)", resp.StatusCode, shortHost(u))
			continue
		}
		text := string(body)
		// GitHub API 通道：JSON 里 content 是 base64
		if strings.Contains(u, "api.github.com") {
			var j map[string]any
			if json.Unmarshal(body, &j) == nil {
				if c, ok := j["content"].(string); ok {
					text = b64Decode(c)
				}
			}
		}
		return text, nil
	}
	return "", lastErr
}

func shortHost(u string) string {
	if i := strings.Index(u, "//"); i > 0 {
		if j := strings.Index(u[i+2:], "/"); j > 0 {
			return u[i+2 : i+2+j]
		}
	}
	return u
}

func b64Decode(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
	out := make([]byte, len(s)*3/4+3)
	n, err := base64.StdEncoding.Decode(out, []byte(s))
	if err != nil {
		return ""
	}
	return string(out[:n])
}

// upstreamVersion 抓上游版本号（version.json 优先，SKILL.md frontmatter 兜底）
func upstreamVersion() (string, error) {
	text, err := fetchUpstreamText(upstreamVersionURLs)
	if err == nil {
		var j map[string]any
		if json.Unmarshal([]byte(text), &j) == nil {
			if v, ok := j["version"].(string); ok && v != "" {
				return v, nil
			}
		}
	}
	// 兜底：SKILL.md frontmatter 的 version:
	skill, err2 := fetchUpstreamText([]string{
		"https://api.github.com/repos/yang0/handraw-style/contents/skills/handdraw-style-prompter/SKILL.md?ref=master",
	})
	if err2 != nil {
		if err != nil {
			return "", err
		}
		return "", err2
	}
	for _, line := range strings.Split(skill, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "version:")), nil
		}
	}
	return "", fmt.Errorf("上游没找到 version 字段")
}

// localVersion 本地技能根 version.json（兜底 SKILL.md frontmatter）
func localVersion() string {
	if raw, err := os.ReadFile(filepath.Join(gCfg.StyleLibPath, "version.json")); err == nil {
		var j map[string]any
		if json.Unmarshal(raw, &j) == nil {
			if v, ok := j["version"].(string); ok && v != "" {
				return v
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(gCfg.StyleLibPath, "SKILL.md"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "version:"))
		}
	}
	return ""
}

// libCheckUpdate 比对上游版本
func libCheckUpdate() map[string]any {
	local := localVersion()
	up, err := upstreamVersion()
	if err != nil {
		return map[string]any{"ok": false, "error": "查上游失败：" + err.Error(), "local": local}
	}
	return map[string]any{
		"ok": true, "local": local, "upstream": up,
		"update_available": up != "" && up != local,
	}
}

// libSyncNow 拉上游 master zip → 覆盖桌面版消费的数据文件 → 热重载
func libSyncNow() map[string]any {
	logf("风格库热更新：开始拉取上游 master …")
	t0 := time.Now()
	resp, err := httpCli.Get(UPSTREAM_ZIP)
	if err != nil {
		return map[string]any{"ok": false, "error": "下载失败：" + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return map[string]any{"ok": false, "error": fmt.Sprintf("下载失败：HTTP %d", resp.StatusCode)}
	}
	tmpZip, err := os.CreateTemp("", "handraw-*.zip")
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	defer os.Remove(tmpZip.Name())
	n, err := io.Copy(tmpZip, resp.Body)
	tmpZip.Close()
	if err != nil {
		return map[string]any{"ok": false, "error": "保存失败：" + err.Error()}
	}
	logf("风格库热更新：已下载 %.1f MB，解包覆盖中…", float64(n)/1024/1024)

	zr, err := zip.OpenReader(tmpZip.Name())
	if err != nil {
		return map[string]any{"ok": false, "error": "解压失败：" + err.Error()}
	}
	defer zr.Close()

	// 定位 zip 里的根目录名（handraw-style-master/）和技能子目录
	var rootPrefix, skillPrefix string
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.HasSuffix(name, "/skills/handdraw-style-prompter/") {
			rootPrefix = name[:strings.Index(name, "skills/")]
			skillPrefix = name
			break
		}
	}
	if skillPrefix == "" {
		return map[string]any{"ok": false, "error": "zip 里找不到 skills/handdraw-style-prompter 目录"}
	}
	// 本地实际结构：StyleLibPath(D:\skills\handdraw-style-prompter) 是技能根（版本元数据），
	// 数据文件在嵌套层 StyleLibPath\handdraw-style-prompter\references\ —— loadStyleLib 的 WalkDir 消费的就是它
	loSkill := filepath.Join(gCfg.StyleLibPath, "handdraw-style-prompter")
	loRoot := gCfg.StyleLibPath

	// ---- 第一遍：构建同步计划（src zip 路径 → dest 本地路径）----
	type planItem struct {
		file *zip.File
		dest string
	}
	var plan []planItem
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if f.FileInfo().IsDir() {
			continue
		}
		var dest string
		if strings.HasPrefix(name, skillPrefix) {
			rel := name[len(skillPrefix):]
			if rel == "" {
				continue
			}
			// 跳过装饰图目录 assets/（与技能行为无关）
			if strings.HasPrefix(rel, "assets/") {
				continue
			}
			dest = filepath.Join(loSkill, filepath.FromSlash(rel))
		} else if strings.HasPrefix(name, rootPrefix) {
			rel := name[len(rootPrefix):]
			if rel == "" || strings.HasPrefix(rel, "skills/") || strings.HasPrefix(rel, "assets/") ||
				strings.HasPrefix(rel, "website/") || strings.HasPrefix(rel, ".git/") {
				continue
			}
			dest = filepath.Join(loRoot, filepath.FromSlash(rel))
		} else {
			continue
		}
		plan = append(plan, planItem{file: f, dest: dest})
	}
	if len(plan) == 0 {
		return map[string]any{"ok": false, "error": "同步计划为空（zip 结构不认识）"}
	}

	// ---- 镜像清理：删除「本次同步涉及的顶层目录」里上游已不存在的旧文件 ----
	// 范围硬限定在 plan 涉及的顶层目录内，外层其他技能目录（article-illustration-planner 等）绝不波及。
	// 删除走回收站（recycleFiles），可还原。
	destSet := map[string]bool{}
	topDirs := map[string]bool{} // 相对 loRoot 的顶层目录（如 images、handdraw-style-prompter）
	for _, it := range plan {
		destSet[strings.ToLower(it.dest)] = true
		rel, err := filepath.Rel(loRoot, it.dest)
		if err == nil && !strings.HasPrefix(rel, "..") {
			topDirs[strings.SplitN(rel, string(filepath.Separator), 2)[0]] = true
		}
	}
	var stale []string
	for top := range topDirs {
		base := filepath.Join(loRoot, top)
		if !fileExists(base) {
			continue
		}
		_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !destSet[strings.ToLower(p)] {
				stale = append(stale, p)
			}
			return nil
		})
	}
	staleDeleted := 0
	if len(stale) > 0 {
		// 先把核心数据备份到 data/backup/<时间>/，删错可救
		bakDir := filepath.Join(dataDir(), "backup", time.Now().Format("20060102_150405"))
		for _, keep := range []string{
			filepath.Join(loSkill, "references"),
			filepath.Join(loRoot, "version.json"),
			filepath.Join(loRoot, "SKILL.md"),
		} {
			if !fileExists(keep) {
				continue
			}
			dst := filepath.Join(bakDir, "handdraw-style-prompter", strings.TrimPrefix(strings.TrimPrefix(keep, loRoot), string(filepath.Separator)))
			if fileExists(keep) && isDir(keep) {
				copyDir(keep, dst)
			} else if fileExists(keep) {
				_ = os.MkdirAll(filepath.Dir(dst), 0o755)
				_, _ = copyFile(keep, dst)
			}
		}
		okN, _ := recycleFiles(stale)
		staleDeleted = okN
		logf("风格库镜像清理：删除上游已不存在的旧文件 %d/%d 个（已备份到 %s）", okN, len(stale), bakDir)
	}

	// ---- 第二遍：写入 ----
	overwritten := 0
	var changedJSON bool
	for _, it := range plan {
		if err := extractZipFile(it.file, it.dest); err != nil {
			return map[string]any{"ok": false, "error": fmt.Sprintf("写入 %s 失败：%v", filepath.Base(it.dest), err)}
		}
		overwritten++
		if strings.HasSuffix(it.dest, "styles.json") || strings.HasSuffix(it.dest, "colors.json") || strings.HasSuffix(it.dest, "layouts.json") {
			changedJSON = true
		}
	}

	// 热重载
	var stats map[string]any
	if changedJSON {
		newLib := loadStyleLib(gCfg.StyleLibPath)
		if len(newLib.Styles) > 0 {
			gLib = newLib
		}
		stats = map[string]any{
			"styles": len(gLib.Styles), "trap": gLib.TrapCount(),
			"colors": gLib.ColorCount, "layouts": gLib.LayoutCount,
		}
	}
	ver := localVersion()
	logf("风格库热更新完成：覆盖 %d 个文件，镜像清理 %d 个旧文件，版本 %s，用时 %.1f 秒", overwritten, staleDeleted, ver, time.Since(t0).Seconds())
	return map[string]any{
		"ok": true, "overwritten": overwritten, "version": ver, "elapsed": time.Since(t0).Seconds(),
		"stale_deleted": staleDeleted,
		"stats":         stats,
	}
}

// ---- 镜像同步辅助 ----

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// copyFile 返回写入字节数
func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	return io.Copy(out, in)
}

// copyDir 递归复制目录
func copyDir(src, dst string) {
	_ = filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == src {
			return nil
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			_ = os.MkdirAll(target, 0o755)
			return nil
		}
		_, _ = copyFile(p, target)
		return nil
	})
}

func extractZipFile(f *zip.File, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	w, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = io.Copy(w, rc)
	return err
}

// ---- 应用自更新 ----

// getAppUpdateURL 从 settings 里读（update_url 字段；空 = 未配置）
func getAppUpdateURL() string {
	return strings.TrimSpace(str(map[string]any(gSettingsToMap()), "update_url"))
}

func gSettingsToMap() map[string]any {
	b, _ := json.Marshal(gSettings)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

// appCheckUpdate 拉 <update_url>/latest.json：{"version":"1.0.1","url":"...exe"}
func appCheckUpdate() map[string]any {
	u := getAppUpdateURL()
	if u == "" {
		return map[string]any{"ok": true, "update_available": false,
			"note": "还没配置更新源（settings.json 里加 update_url 指向托管 latest.json 的地址）"}
	}
	resp, err := httpCli.Get(strings.TrimRight(u, "/") + "/latest.json")
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	defer resp.Body.Close()
	var latest struct {
		Version string `json:"version"`
		URL     string `json:"url"`
		Note    string `json:"note"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&latest); err != nil {
		return map[string]any{"ok": false, "error": "latest.json 解析失败：" + err.Error()}
	}
	return map[string]any{
		"ok": true, "current": APP_VERSION, "latest": latest.Version,
		"url": latest.URL, "note": latest.Note,
		"update_available": latest.Version != "" && latest.Version != APP_VERSION,
	}
}

// appUpdateNow 下载新 exe → 当前 exe 改名 .old → 放新 exe → 重启
func appUpdateNow(downloadURL string) map[string]any {
	if downloadURL == "" {
		return map[string]any{"ok": false, "error": "没有更新包地址"}
	}
	exePath, err := os.Executable()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	logf("自更新：下载 %s …", downloadURL)
	resp, err := httpCli.Get(downloadURL)
	if err != nil {
		return map[string]any{"ok": false, "error": "下载失败：" + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return map[string]any{"ok": false, "error": fmt.Sprintf("下载失败：HTTP %d", resp.StatusCode)}
	}
	tmp := exePath + ".new"
	f, err := os.Create(tmp)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return map[string]any{"ok": false, "error": "保存失败：" + err.Error()}
	}
	if n < 1024*1024 {
		os.Remove(tmp)
		return map[string]any{"ok": false, "error": fmt.Sprintf("下载的文件只有 %.1f MB，不像完整 exe，放弃", float64(n)/1024/1024)}
	}
	old := exePath + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exePath, old); err != nil {
		os.Remove(tmp)
		return map[string]any{"ok": false, "error": "旧版改名失败（exe 正在被占用？）：" + err.Error()}
	}
	if err := os.Rename(tmp, exePath); err != nil {
		_ = os.Rename(old, exePath) // 回滚
		os.Remove(tmp)
		return map[string]any{"ok": false, "error": "新文件就位失败：" + err.Error()}
	}
	logf("自更新：新版本已就位（%.1f MB），重启应用", float64(n)/1024/1024)
	// 启动新 exe 再退出自己
	go func() {
		time.Sleep(500 * time.Millisecond)
		exec.Command("cmd", "/c", "start", "", exePath).Start()
		os.Exit(0)
	}()
	return map[string]any{"ok": true, "note": "新版本已就位，应用即将重启"}
}
