package main

import (
	"embed"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	initLog()
	gCfg = loadConfig()
	gLib = loadStyleLib(gCfg.StyleLibPath)
	gSettings = loadSettings()
	gAgnes = buildAgnes(gSettings)
	gOut = gCfg.outDir()
	_ = os.MkdirAll(gOut, 0o755)
	_ = os.MkdirAll(dataDir(), 0o755)
	autoBackupData() // 启动滚动备份 data 关键文件（保留最近 5 份）
	dbPath = filepath.Join(dataDir(), "history.db")
	charsPath = filepath.Join(dataDir(), "characters.json")
	startWorkers()
	startVideoWorkers()
	// 自测开关：exe 同目录存在 data/cdp.flag 时开启 DevTools 端口（仅本地回环）。
	// 关闭态强制 unset —— 防止系统环境变量残留（曾被 setx 污染过）意外打开调试端口
	cdpOn := false
	if _, err := os.Stat(filepath.Join(dataDir(), "cdp.flag")); err == nil {
		cdpOn = true
		os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--remote-debugging-port=9222")
		logf("CDP 调试端口已开启 (9222)")
	} else {
		os.Unsetenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS")
	}
	FFMPEG = findFFmpeg("ffmpeg", iniStr("video", "ffmpeg"))
	FFPROBE = findFFprobe(iniStr("video", "ffprobe"))

	logf("桌面版 v%s 启动：风格 %d / 陷阱 %d / 配色 %d / 版式 %d / key %d 把 / 素材根 %s / ffmpeg %s",
		APP_VERSION, len(gLib.Styles), gLib.TrapCount(), gLib.ColorCount, gLib.LayoutCount,
		gAgnes.KeyCount(), gOut, orEmpty(FFMPEG))

	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "角色设定表工作台 · 桌面版",
		Width:     1440,
		Height:    900,
		MinWidth:  980,
		MinHeight: 640,
		// 启动即最大化：屏幕多小都能铺满，内容按窗口自适应排版
		WindowStartState: options.Maximised,
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: &resHandler{},
		},
		BackgroundColour: &options.RGBA{R: 12, G: 14, B: 19, A: 1},
		// CDP 自测时置 true，让 Wails 不传任何浏览器参数，loader 才会读取 WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS
		EnableFraudulentWebsiteDetection: cdpOn,
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

// resHandler 静态资源：/img/ /sheet/ /color-img/ /layout-img/ /out/
// 对应 Python 版 do_GET 的图片路由。嵌入资源里没有的路径都会落到这里。
type resHandler struct{}

func (h *resHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	// 浏览器会把 %XX 解码后发过来，r.URL.Path 已是解码后的
	if strings.HasPrefix(p, "img/") {
		no := padNo(strings.TrimSpace(p[len("img/"):]))
		fp, ok := gLib.ImgMap[no]
		if !ok {
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, fp, "public, max-age=86400")
		return
	}
	if strings.HasPrefix(p, "sheet/") {
		name := filepath.Base(p[len("sheet/"):])
		fp, ok := gLib.SheetMap[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, fp, "public, max-age=86400")
		return
	}
	if strings.HasPrefix(p, "color-img/") {
		c, ok := gLib.Colors[strings.TrimSpace(p[len("color-img/"):])]
		if !ok || c.Img == "" {
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, c.Img, "public, max-age=86400")
		return
	}
	if strings.HasPrefix(p, "layout-img/") {
		l, ok := gLib.Layouts[strings.TrimSpace(p[len("layout-img/"):])]
		if !ok || l.Img == "" {
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, l.Img, "public, max-age=86400")
		return
	}
	if strings.HasPrefix(p, "out/") {
		rel := filepath.FromSlash(p[len("out/"):])
		fp := filepath.Join(gOut, rel)
		// 防目录穿越
		if !strings.HasPrefix(fp, gOut) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		serveFile(w, r, fp, "no-store")
		return
	}
	if strings.HasPrefix(p, "shots-img/") {
		fp := filepath.Join(dataDir(), "shot_imgs", filepath.Base(p[len("shots-img/"):]))
		serveFile(w, r, fp, "no-store")
		return
	}
	if strings.HasPrefix(p, "lib-img/") {
		// 配色/版式图片按本地绝对路径伺服（前端传 %ENCODED% 路径，必须落在风格库里）
		dec, err := url.PathUnescape(p[len("lib-img/"):])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		fp := filepath.Clean(dec)
		if !strings.HasPrefix(strings.ToLower(fp), strings.ToLower(gLib.Dir)) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		serveFile(w, r, fp, "public, max-age=86400")
		return
	}
	// ---- 上游风格画廊（gallery/）与风格库图片（images/）----
	// gallery/index.html 里的图片用相对路径 ../../../images/... —— /images/ 路由落在
	// gLib.Dir/images 下，相对路径即可解析；gallery 跟随风格库热更新自动换新。
	if strings.HasPrefix(p, "gallery/") {
		rel := filepath.FromSlash(strings.TrimPrefix(p, "gallery/"))
		if rel == "" || strings.Contains(rel, "..") {
			http.NotFound(w, r)
			return
		}
		fp := filepath.Join(gLib.Dir, "handdraw-style-prompter", "gallery", rel)
		if !fileExists(fp) {
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, fp, "no-store")
		return
	}
	if strings.HasPrefix(p, "images/") {
		rel := filepath.FromSlash(strings.TrimPrefix(p, "images/"))
		if rel == "" || strings.Contains(rel, "..") {
			http.NotFound(w, r)
			return
		}
		fp := filepath.Join(gLib.Dir, "images", rel)
		if !fileExists(fp) {
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, fp, "public, max-age=86400")
		return
	}
	http.NotFound(w, r)
}

// iniStr 读 config.ini 某节的某个值
func iniStr(section, key string) string {
	ini := readIni(filepath.Join(APP_DIR, "config.ini"))
	return strings.TrimSpace(ini[section][key])
}

func orEmpty(s string) string {
	if s == "" {
		return "未找到"
	}
	return s
}

func findFFprobe(override string) string { return findFFmpeg("ffprobe", override) }

func serveFile(w http.ResponseWriter, r *http.Request, path, cache string) {
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("Content-Type", mimeOf(path))
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}

func mimeOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".mp4":
		return "video/mp4"
	case ".html":
		return "text/html; charset=utf-8"
	}
	return "application/octet-stream"
}
