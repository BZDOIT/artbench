package main

// App：Wails 绑定层 —— 前端通过 window.go.main.App.* 直接调用这些方法
// 对应 Python 版 HTTP API：/api/config /api/styles /api/colors /api/layouts /api/prompt
// /api/generate /api/job/<id> /api/history /api/characters

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// GetConfig 对应 /api/config
func (a *App) GetConfig() map[string]any {
	ag := getAgnes()
	st := snapshotSettings()
	return map[string]any{
		"ok":            true,
		"model":         ag.Model,
		"base_url":      ag.Base,
		"size":          st.Size,
		"ratio":         st.Ratio,
		"default_count": st.DefaultCount,
		"key_ready":     ag.OK,
		"key_count":     ag.KeyCount(),
		"style_count":   len(gLib.Styles),
		"image_count":   len(gLib.ImgMap),
		"color_count":   gLib.ColorCount,
		"layout_count":  gLib.LayoutCount,
		"trap_count":    gLib.TrapCount(),
		"lib_dir":       gCfg.StyleLibPath,
		"out_dir":       gOut,
		"env_file":      gCfg.EnvFile,
	}
}

// GetStyles 对应 /api/styles
func (a *App) GetStyles() map[string]any {
	items := make([]map[string]any, 0, len(gLib.Styles))
	for _, s := range gLib.Styles {
		items = append(items, map[string]any{
			"no":     s.Number,
			"group":  s.Group,
			"ref":    s.Reference,
			"gen":    s.GenerationName,
			"traits": s.Traits,
			"pos":    s.PosTraits,
			"trap":   s.Trap,
			"img":    s.HasImg,
			"sheet":  s.Sheet,
		})
	}
	return map[string]any{
		"ok":     true,
		"count":  len(items),
		"groups": gLib.Groups,
		"styles": items,
	}
}

// GetColors 对应 /api/colors
func (a *App) GetColors() map[string]any {
	colors := make([]*Color, 0, len(gLib.Colors))
	for _, c := range gLib.Colors {
		colors = append(colors, c)
	}
	sort.Slice(colors, func(i, j int) bool { return colors[i].ID < colors[j].ID })
	return map[string]any{"ok": true, "count": len(colors), "colors": colors}
}

// GetLayouts 对应 /api/layouts
func (a *App) GetLayouts() map[string]any {
	layouts := make([]*Layout, 0, len(gLib.Layouts))
	for _, l := range gLib.Layouts {
		layouts = append(layouts, l)
	}
	sort.Slice(layouts, func(i, j int) bool {
		if layouts[i].Cat != layouts[j].Cat {
			return layouts[i].Cat < layouts[j].Cat
		}
		return layouts[i].ID < layouts[j].ID
	})
	return map[string]any{"ok": true, "count": len(layouts), "layouts": layouts}
}

// BuildPrompt 对应 /api/prompt（sheet / card / free 三种模式）
func (a *App) BuildPrompt(body map[string]any) map[string]any {
	mode := strings.TrimSpace(str(body, "mode"))
	if mode == "card" {
		no, st := lookupStyle(body)
		if bodyHasStyle(body) && st == nil {
			return map[string]any{"error": fmt.Sprintf("没有编号 #%s", no)}
		}
		if strings.TrimSpace(str(body, "card_theme")) == "" {
			return map[string]any{"error": "图文卡要先写主题"}
		}
		resp := map[string]any{"ok": true, "mode": "card", "prompt": buildCardPrompt(body, st, gLib)}
		if st != nil {
			resp["style"] = map[string]any{"no": no, "gen": st.GenerationName, "trap": st.Trap}
		}
		return resp
	}
	if mode == "free" {
		no, st := lookupStyle(body)
		if bodyHasStyle(body) && st == nil {
			return map[string]any{"error": fmt.Sprintf("没有编号 #%s", no)}
		}
		if strings.TrimSpace(str(body, "free_text")) == "" {
			return map[string]any{"error": "先写一句提示词"}
		}
		resp := map[string]any{"ok": true, "mode": "free", "prompt": buildFreePrompt(body, st)}
		if st != nil {
			resp["style"] = map[string]any{"no": no, "gen": st.GenerationName, "trap": st.Trap}
		}
		return resp
	}
	// sheet
	if strings.TrimSpace(str(body, "style_no")) == "" && strings.TrimSpace(str(body, "prompt")) == "" {
		return map[string]any{"error": "先选一个风格编号（或在自由出图页直接写提示词）"}
	}
	no := normalizeStyleNo(str(body, "style_no"))
	st := gLib.ByNo[no]
	if st == nil {
		return map[string]any{"error": fmt.Sprintf("没有编号 #%s", no)}
	}
	return map[string]any{
		"ok":     true,
		"prompt": buildPrompt(body, st, gLib),
		"style": map[string]any{
			"no": no, "gen": st.GenerationName, "ref": st.Reference,
			"group": st.Group, "trap": st.Trap,
		},
	}
}

func bodyHasStyle(body map[string]any) bool {
	return strings.TrimSpace(str(body, "style_no")) != ""
}

func lookupStyle(body map[string]any) (string, *Style) {
	no := normalizeStyleNo(str(body, "style_no"))
	return no, gLib.ByNo[no]
}

// Generate 对应 /api/generate：校验 → 建任务 → 入队。mode=sheet（默认）/ free。
// free 模式：free_text 必填，风格可选（点了风格卡就带风格头）；陷阱编号不拦截（自由出图以用户文本为主）。
func (a *App) Generate(body map[string]any) map[string]any {
	mode := strings.TrimSpace(str(body, "mode"))
	no := ""
	if strings.TrimSpace(str(body, "style_no")) != "" {
		no = normalizeStyleNo(str(body, "style_no"))
	}
	var st *Style
	if no != "" {
		st = gLib.ByNo[no]
		if st == nil {
			return map[string]any{"error": fmt.Sprintf("没有编号 #%s", no)}
		}
	}
	prompt := strings.TrimSpace(str(body, "prompt"))
	if prompt == "" {
		switch mode {
		case "free":
			if strings.TrimSpace(str(body, "free_text")) == "" {
				return map[string]any{"error": "先写一句提示词"}
			}
			prompt = buildFreePrompt(body, st)
		case "card":
			// 图文卡：只要求主题；风格/配色/版式都可选（对齐上游 SKILL.md 规则）
			if strings.TrimSpace(str(body, "card_theme")) == "" {
				return map[string]any{"error": "图文卡要先写主题"}
			}
			prompt = buildCardPrompt(body, st, gLib)
		default:
			if st == nil {
				return map[string]any{"error": "先选一个风格编号"}
			}
			prompt = buildPrompt(body, st, gLib)
		}
	}
	if mode != "free" && st != nil && st.Trap && !truthy(body["force"]) {
		return map[string]any{
			"error": fmt.Sprintf("#%s 的 traits（风格特征）是空的——这种编号界面上都标着「陷阱」，"+
				"没法用文本锚定风格，只能垫参考图而垫图会毁角色一致性。建议换编号。", no),
			"trap": true,
		}
	}

	refs := strSlice(body["refs"])
	if len(refs) > 6 {
		return map[string]any{"error": fmt.Sprintf("垫图最多 6 张，你传了 %d 张", len(refs))}
	}

	st0 := snapshotSettings()
	count := clamp(asInt(body["count"], st0.DefaultCount), 1, 6)

	size := strings.TrimSpace(str(body, "size"))
	if size == "" {
		size = st0.Size
	}
	charName := strings.TrimSpace(str(body, "char_name"))
	if charName == "" && mode == "free" {
		charName = "自由出图"
	}
	styleName := ""
	if st != nil {
		styleName = st.GenerationName
	}
	meta := map[string]any{
		"style_no":   no,
		"style_name": styleName,
		"char_name":  charName,
		"mode":       mode,
		"prompt":     prompt,
		"size":       size,
		"ratio":      strings.TrimSpace(str(body, "ratio")),
		"refs":       refs,
		"count":      count,
	}
	if mode == "card" {
		// SC-021 动态解析要用的字段（固定行在入队时快照，防风格库热更后错位）
		layoutID := strings.TrimSpace(str(body, "layout_id"))
		meta["layout_id"] = layoutID
		meta["card_theme"] = strings.TrimSpace(str(body, "card_theme"))
		if layoutID == "SC-021" {
			meta["sc021_fixed"] = sc021FixedLayoutLine()
		}
	}
	job := newJob(meta)
	taskQ <- job.ID
	logf("新任务 %s：%s #%s / 角色 %s / %d 张", job.ID, mode, no, orUnnamed(charName), count)
	return map[string]any{"ok": true, "job_id": job.ID, "count": count}
}

func orUnnamed(s string) string {
	if s == "" {
		return "未命名"
	}
	return s
}

func truthy(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

// GetJob 对应 /api/job/<id>
func (a *App) GetJob(id string) map[string]any {
	job := getJob(id)
	if job == nil {
		return map[string]any{"error": "job 不存在"}
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	logs := job.Logs
	if len(logs) > 40 {
		logs = logs[len(logs)-40:]
	}
	return map[string]any{
		"ok":        true,
		"id":        job.ID,
		"status":    job.Status,
		"log":       logs,
		"urls":      job.URLs,
		"files":     job.Files,
		"error":     job.Error,
		"elapsed":   job.Elapsed,
		"style_no":  str(job.Meta, "style_no"),
		"char_name": str(job.Meta, "char_name"),
		"count":     asInt(job.Meta["count"], 1),
	}
}

// ListHistory 对应 /api/history
func (a *App) ListHistory(limit int) map[string]any {
	return map[string]any{"ok": true, "history": listHistory(limit)}
}

// DeleteHistory 对应 /api/history/delete
func (a *App) DeleteHistory(id int) map[string]any {
	deleteHistory(id)
	return map[string]any{"ok": true, "history": listHistory(60)}
}

// GetCharacters 对应 GET /api/characters
func (a *App) GetCharacters() []map[string]any {
	return loadCharacters()
}

// SaveCharacter 对应 POST /api/characters
func (a *App) SaveCharacter(data map[string]any) map[string]any {
	name := strings.TrimSpace(str(data, "char_name"))
	if name == "" {
		return map[string]any{"error": "角色名不能为空"}
	}
	return map[string]any{"ok": true, "characters": saveCharacter(data)}
}

// DeleteCharacter 对应 POST /api/characters/delete
func (a *App) DeleteCharacter(name string) map[string]any {
	return map[string]any{"ok": true, "characters": deleteCharacter(name)}
}

// OpenPath 用系统默认程序打开文件
func (a *App) OpenPath(path string) error {
	return exec.Command("cmd", "/c", "start", "", path).Start()
}

// RevealPath 在资源管理器中定位文件
func (a *App) RevealPath(path string) error {
	return exec.Command("explorer", "/select,", path).Start()
}

// ---- 设置面板：key / 模型 / 接口地址 ----

// GetSettings 返回设置（key 只回打码尾部，全文永不出后端）
func (a *App) GetSettings() map[string]any {
	s := snapshotSettings()
	keys := make([]map[string]any, 0, len(s.Keys))
	for i, k := range s.Keys {
		keys = append(keys, map[string]any{"index": i, "tail": ktailSec(k)})
	}
	return map[string]any{
		"ok":             true,
		"keys":           keys,
		"base_url":       s.BaseURL,
		"model":          s.Model,
		"size":           s.Size,
		"ratio":          s.Ratio,
		"timeout":        s.Timeout,
		"retries":        s.Retries,
		"default_count":  s.DefaultCount,
		"update_url":     s.UpdateURL,
		"video_base_url": s.VideoBaseURL,
		"video_model":    s.VideoModel,
		"text_model":     s.TextModel,
		"settings_file":  settingsFile,
		"image_sizes":    IMAGE_SIZES,
		"image_ratios":   IMAGE_RATIOS,
	}
}

// AddKey 增加一把 key（全文只在入参出现一次，不落日志）
func (a *App) AddKey(key string) map[string]any {
	if err := settingsAddKey(key); err != nil {
		return map[string]any{"error": err.Error()}
	}
	setAgnes(buildAgnes(gSettings))
	logf("key 池更新：新增一把，现有 %d 把", getAgnes().KeyCount())
	return a.GetSettings()
}

// RemoveKey 按 GetSettings 返回的 index 删 key
func (a *App) RemoveKey(index int) map[string]any {
	if err := settingsRemoveKey(index); err != nil {
		return map[string]any{"error": err.Error()}
	}
	setAgnes(buildAgnes(gSettings))
	logf("key 池更新：删除一把，现有 %d 把", getAgnes().KeyCount())
	return a.GetSettings()
}

// SaveSettings 保存模型 / 接口地址 / 参数，即刻重建客户端，不用重启
func (a *App) SaveSettings(body map[string]any) map[string]any {
	if err := settingsUpdate(body); err != nil {
		return map[string]any{"error": err.Error()}
	}
	setAgnes(buildAgnes(gSettings))
	s := snapshotSettings()
	logf("设置已更新：model=%s base=%s keys=%d", s.Model, s.BaseURL, len(s.Keys))
	return a.GetSettings()
}

// TestImageAPI 连通性测试：发一个极短请求看 HTTP 状态（不发图，省额度——用错误响应也能判断通不通）
func (a *App) TestImageAPI() map[string]any {
	ag := getAgnes()
	if !ag.OK {
		return map[string]any{"ok": false, "msg": "key 池是空的，先加 key"}
	}
	start := time.Now()
	// 用一个必然 400 的空 prompt 探活：能收到 HTTP 状态码 = 链路通
	cli := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequest("POST", ag.Base+"/images/generations",
		strings.NewReader(`{"model":"`+ag.Model+`","prompt":"","size":"1024x768"}`))
	req.Header.Set("Authorization", "Bearer "+ag.Keys[0])
	req.Header.Set("Content-Type", "application/json")
	resp, err := cli.Do(req)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]any{"ok": false, "msg": fmt.Sprintf("连不上：%v（%d ms）", err, ms)}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return map[string]any{"ok": false, "msg": fmt.Sprintf("链路通（%d ms），但 key 无效（401）", ms)}
	}
	return map[string]any{"ok": true, "msg": fmt.Sprintf("连通正常，HTTP %d，耗时 %d ms", resp.StatusCode, ms)}
}

// ListTextModels 探测 GET /v1/models，返回真实可用模型清单（设置弹窗下拉用）
func (a *App) ListTextModels() map[string]any {
	models, err := listAgnesModels()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	return map[string]any{"ok": true, "models": models, "count": len(models)}
}

// CopyText 写入系统剪贴板（WebView2 的 http 源不是安全上下文，navigator.clipboard 不可用，走后端）
func (a *App) CopyText(text string) error {
	return wruntime.ClipboardSetText(a.ctx, text)
}

// Sc021State SC-021 轮替进度（peek 只读，不推进）：前端在按钮旁显示「下次玩法 N/5」
func (a *App) Sc021State() map[string]any {
	idx := peekPlaystyle()
	return map[string]any{
		"next":     idx + 1,
		"total":    len(SC021_PLAYSTYLES),
		"name":     strings.SplitN(SC021_PLAYSTYLES[idx], "：", 2)[0],
		"hasModel": textModel() != "",
	}
}

// KeyStatus key 池状态（脱敏尾号 + 冷却剩余秒数），设置弹窗状态条用
func (a *App) KeyStatus() []map[string]any {
	out := []map[string]any{}
	if gAgnes == nil {
		return out
	}
	for _, k := range gAgnes.Keys {
		cooling, left := keyCooldownLeft(k)
		out = append(out, map[string]any{
			"key":    ktail(k),
			"cooling": cooling,
			"left":   int(left),
		})
	}
	return out
}

// CardRecommend 图文卡 AI 推荐：主题 + 风格摘要 + 配色清单 → 文本模型 → 风格编号 + 配色 + 理由
func (a *App) CardRecommend(body map[string]any) map[string]any {
	theme := strings.TrimSpace(str(body, "card_theme"))
	if theme == "" {
		return map[string]any{"error": "先写主题再点 AI 推荐"}
	}
	if textModel() == "" {
		return map[string]any{"error": "还没配置文本/推理模型：打开设置，点「探测可用模型」后从真实清单里选一个"}
	}
	start := time.Now()
	onLog := func(m string) { logf("[推荐] %s", m) }
	no, cid, reason, err := cardRecommend(theme, onLog)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	resp := map[string]any{
		"ok":       true,
		"style_no": no,
		"color_id": cid,
		"reason":   reason,
		"elapsed":  time.Since(start).Seconds(),
	}
	if st := gLib.ByNo[no]; st != nil {
		resp["style_name"] = st.GenerationName
	}
	if c := gLib.Colors[cid]; c != nil {
		resp["color_name"] = c.Name
	}
	return resp
}

// Sc021Preview SC-021 解析预览：peek 当前轮替玩法（不推进），生成时才推进——预览即所得
func (a *App) Sc021Preview(body map[string]any) map[string]any {
	theme := strings.TrimSpace(str(body, "card_theme"))
	if theme == "" {
		return map[string]any{"error": "先写主题再预览"}
	}
	if textModel() == "" {
		return map[string]any{"error": "还没配置文本/推理模型（设置里点「探测可用模型」选一个）"}
	}
	lay := gLib.Layouts["SC-021"]
	if lay == nil {
		return map[string]any{"error": "风格库里没有 SC-021 版式"}
	}
	idx := peekPlaystyle()
	var st *Style
	if no := strings.TrimSpace(str(body, "style_no")); no != "" {
		st = gLib.ByNo[normalizeStyleNo(no)]
	}
	start := time.Now()
	onLog := func(m string) { logf("[SC021预览] %s", m) }
	dyn, err := sc021PromptForPlaystyle(theme, st, idx, onLog)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{
		"ok": true, "playstyle": idx + 1,
		"playstyle_name": strings.SplitN(SC021_PLAYSTYLES[idx], "：", 2)[0],
		"prompt":         dyn,
		"elapsed":        time.Since(start).Seconds(),
	}
}

// ==================== M2：素材库 / 素材根 ====================

// ScanLibrary 扫素材（kind: all/img/video）
func (a *App) ScanLibrary(kind string, limit int) map[string]any {
	items := scanLibrary(kind, limit)
	return map[string]any{"ok": true, "count": len(items), "items": items, "out_dir": getOut()}
}

// DeleteFiles 批量删到回收站（可还原）
func (a *App) DeleteFiles(paths []string) map[string]any {
	if len(paths) == 0 {
		return map[string]any{"error": "没有要删除的文件"}
	}
	ok, failed := recycleFiles(paths)
	r := map[string]any{"ok": ok > 0, "deleted": ok, "failed": failed}
	if len(failed) > 0 {
		r["note"] = fmt.Sprintf("成功 %d 个，失败 %d 个（失败的都是不存在或不在素材目录里的）", ok, len(failed))
	}
	return r
}

// SetOutputDir 改素材根（即刻生效 + 写回 config.ini）
func (a *App) SetOutputDir(dir string) map[string]any {
	dirs, err := setOutputDir(dir)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"ok": true, "out_dir": dirs["out_dir"], "note": "已生效并写回 config.ini"}
}

// ==================== M3：分镜成片 / 垫图出片 ====================

// GetVideoInfo 视频通道信息
func (a *App) GetVideoInfo() map[string]any {
	ready := false
	for _, c := range gVideoClients {
		if c.OK {
			ready = true
			break
		}
	}
	model := ""
	if len(gVideoClients) > 0 {
		model = gVideoClients[0].Model
	}
	return map[string]any{
		"ok": true, "ffmpeg": FFMPEG, "ffprobe": FFPROBE,
		"video_model": model, "video_ready": ready, "video_workers": len(gVideoClients),
		"key_ready": getAgnes().OK, "key_count": getAgnes().KeyCount(),
		"aspects": ASPECTS, "aspect_hint": ASPECT_HINT, "image_ratios": IMAGE_RATIOS,
		"cameras": cameraNames(), "modes": MODE_LABEL,
		"clip_modes": CLIP_MODES, "ref_max": REF_MAX,
		"queue": len(vq),
	}
}

func cameraNames() []string {
	return []string{"推", "拉", "摇", "移", "跟拍", "固定", "环绕"}
}

// GetFilm 分镜表（带实时进度）
func (a *App) GetFilm() map[string]any {
	film, shots := loadFilm()
	if film == nil {
		film = map[string]any{}
	}
	mvURL, mvFile := resultURL(str(film, "movie"))
	if mvURL != "" && mvFile == "" {
		mvURL = ""
	}
	film["movie"] = mvURL
	film["movie_file"] = mvFile

	prog := progSnapshot()
	seqImg := map[int]string{} // seq -> img（判断尾帧来源）
	for _, s := range shots {
		seqImg[asInt(s["seq"], 0)] = str(s, "img")
	}
	for _, s := range shots {
		s["prog"] = prog[progKey(asInt(s["id"], 0))]
		s["img_ok"] = resolveImg(str(s, "img")) != ""
		s["img2_ok"] = resolveImg(str(s, "img2")) != ""
		resURL, resFile := resultURL(str(s, "result"))
		s["result"] = resURL
		s["result_file"] = resFile
		if strings.TrimSpace(str(s, "img2")) != "" {
			s["last_from"] = "手动指定"
		} else {
			nxt := seqImg[asInt(s["seq"], 0)+1]
			if nxt != "" {
				s["last_from"] = fmt.Sprintf("下一镜 #%02d", asInt(s["seq"], 0)+1)
			} else {
				s["last_from"] = ""
			}
		}
	}
	vqueuedMu.Lock()
	q := len(vq)
	vqueuedMu.Unlock()
	return map[string]any{"ok": true, "film": film, "shots": shots, "queue": q}
}

// SaveFilm 整表覆盖保存
func (a *App) SaveFilm(payload map[string]any) map[string]any {
	film, shots := saveFilm(payload)
	return map[string]any{"ok": true, "film": film, "shots": shots}
}

// GenerateShots 批量入队（ids 空 = 全部 pending/error 的镜）
func (a *App) GenerateShots(ids []int) map[string]any {
	_, shots := loadFilm()
	queued := 0
	if len(ids) == 0 {
		for _, s := range shots {
			st := str(s, "status")
			if st == "pending" || st == "error" {
				if enqueueShot(asInt(s["id"], 0), false) {
					queued++
				}
			}
		}
	} else {
		for _, id := range ids {
			if enqueueShot(id, false) {
				queued++
			}
		}
	}
	return map[string]any{"ok": true, "queued": queued}
}

// RetryShot 重抽一镜：清产出再入队
func (a *App) RetryShot(sid int) map[string]any {
	setShot(sid, map[string]any{"status": "pending", "video_id": "", "result": "", "error": "", "elapsed": 0, "aspect": ""})
	enqueueShot(sid, false)
	return map[string]any{"ok": true}
}

// DeleteShot 删一镜
func (a *App) DeleteShot(sid int) map[string]any {
	dbMu.Lock()
	conn, err := openDB()
	if err == nil {
		_, _ = conn.Exec("DELETE FROM shots WHERE id=?", sid)
		conn.Close()
	}
	dbMu.Unlock()
	film, shots := loadFilm()
	return map[string]any{"ok": true, "film": film, "shots": shots}
}

// ResumePending 重启后续跑：未完成的镜/片段重新入队
func (a *App) ResumePending() map[string]any {
	return resumePending()
}

// ConcatShots 拼接选中镜头
func (a *App) ConcatShots(ids []int) map[string]any {
	r, err := concatShots(ids)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return r
}

// UploadShotImg 上传关键帧（dataURL base64）→ data/shot_imgs，返回 URL
func (a *App) UploadShotImg(dataURL string) map[string]any {
	const prefix = "base64,"
	i := strings.Index(dataURL, prefix)
	if !strings.HasPrefix(dataURL, "data:image/") || i < 0 {
		return map[string]any{"error": "只支持图片 dataURL"}
	}
	b64 := dataURL[i+len(prefix):]
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return map[string]any{"error": "base64 解码失败：" + err.Error()}
	}
	dir := filepath.Join(dataDir(), "shot_imgs")
	_ = os.MkdirAll(dir, 0o755)
	fn := fmt.Sprintf("kf_%s.png", time.Now().Format("20060102_150405"))
	fp := filepath.Join(dir, fn)
	if err := os.WriteFile(fp, raw, 0o644); err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"ok": true, "url": "/shots-img/" + fn, "file": fp}
}

// GetClips 垫图任务列表
func (a *App) GetClips() map[string]any {
	vqueuedMu.Lock()
	q := len(vq)
	vqueuedMu.Unlock()
	return map[string]any{
		"ok": true, "clips": clipPayloads(80), "queue": q,
		"defaults": map[string]any{
			"mode": "reference", "seconds": 5, "aspect": "16:9",
			"aspects": ASPECTS, "aspect_hint": ASPECT_HINT,
			"max_refs": REF_MAX, "modes": CLIP_MODES, "pic_token": "<Picture {n}>",
		},
	}
}

// SaveClip 存草稿
func (a *App) SaveClip(payload map[string]any) map[string]any {
	c := saveClip(payload)
	if c == nil {
		return map[string]any{"error": "保存失败"}
	}
	return map[string]any{"ok": true, "clip": clipPayload(c), "clips": clipPayloads(80)}
}

// DeleteClipID 删片段
func (a *App) DeleteClipID(cid int) map[string]any {
	deleteClip(cid)
	return map[string]any{"ok": true, "clips": clipPayloads(80)}
}

// GenerateClip 存草稿 + 入队
func (a *App) GenerateClip(payload map[string]any) map[string]any {
	c := saveClip(payload)
	if c == nil {
		return map[string]any{"error": "保存失败"}
	}
	cid := asInt(c["id"], 0)
	// 就绪检查
	mode := str(c, "mode")
	if _, ok := CLIP_MODES[mode]; !ok {
		mode = "reference"
	}
	if strings.TrimSpace(str(c, "prompt")) == "" {
		return map[string]any{"error": "提示词是空的", "clip": clipPayload(c), "clips": clipPayloads(80)}
	}
	if mode == "reference" {
		refs := clipRefs(c["refs"])
		if len(refs) == 0 {
			return map[string]any{"error": "垫图模式至少要 1 张参考图", "clip": clipPayload(c), "clips": clipPayloads(80)}
		}
		for i, u := range refs {
			if resolveImg(u) == "" {
				return map[string]any{"error": fmt.Sprintf("第 %d 张参考图找不到了", i+1),
					"clip": clipPayload(c), "clips": clipPayloads(80)}
			}
		}
	}
	setClip(cid, map[string]any{"status": "pending", "video_id": "", "result": "", "error": "", "elapsed": 0})
	enqueueClip(cid, false)
	logf("垫图出片：片段#%d 入队", cid)
	return map[string]any{"ok": true, "id": cid, "clip": clipPayload(getClip(cid)), "clips": clipPayloads(80)}
}

// ==================== M4：风格库热更新 / 应用自更新 ====================

// GetAppVersion 当前版本号
func (a *App) GetAppVersion() map[string]any {
	return map[string]any{"ok": true, "version": APP_VERSION, "lib_version": localVersion()}
}

// LibCheckUpdate 查上游风格库有没有新版本
func (a *App) LibCheckUpdate() map[string]any {
	return libCheckUpdate()
}

// LibSyncNow 一键热更新风格库（拉上游 → 覆盖 → 热重载）
func (a *App) LibSyncNow() map[string]any {
	return libSyncNow()
}

// AppCheckUpdate 检查应用自更新
func (a *App) AppCheckUpdate() map[string]any {
	return appCheckUpdate()
}

// AppUpdateNow 下载并换装新版本（成功后应用自动重启）
func (a *App) AppUpdateNow(downloadURL string) map[string]any {
	return appUpdateNow(downloadURL)
}
