package main

// 分镜成片（films/shots）+ 垫图出片（clips）存储与执行
// 迁移自 Python app.py 的 _load_film / save_film / run_shot / run_clip / concat_shots 等

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ---- 进度表 + 视频队列 ----

var (
	vprog     = map[string]map[string]any{}
	vprogMu   sync.Mutex
	vqueued   = map[string]bool{}
	vqueuedMu sync.Mutex
	vq        = make(chan string, 200)

	gVideoClients []*VideoClient
)

func progKey(shotID int) string { return fmt.Sprintf("shot:%d", shotID) }
func clipKey(cid int) string    { return fmt.Sprintf("clip:%d", cid) }

func progSnapshot() map[string]map[string]any {
	vprogMu.Lock()
	defer vprogMu.Unlock()
	out := map[string]map[string]any{}
	for k, v := range vprog {
		m := map[string]any{}
		for k2, v2 := range v {
			m[k2] = v2
		}
		out[k] = m
	}
	return out
}

func progSet(key, stage string, kv map[string]any) {
	vprogMu.Lock()
	defer vprogMu.Unlock()
	d := vprog[key]
	if d == nil {
		d = map[string]any{}
		vprog[key] = d
	}
	d["stage"] = stage
	for k, v := range kv {
		d[k] = v
	}
}

func progPop(key string) {
	vprogMu.Lock()
	delete(vprog, key)
	vprogMu.Unlock()
}

func vlog(prefix, msg string) {
	logf("[视频%s] %s", prefix, msg)
}

// ---- 自动重试：可重试错误（限流/上游抖动/网络/超时）失败后按 60s/180s/300s 递增重试，最多 3 次 ----

const videoRetryMax = 3

var (
	videoRetryMu  sync.Mutex
	videoRetryCnt = map[string]int{} // "shot:3" / "clip:5" → 已自动重试次数（手动重做/成功后清零）
)

// retriableVideoErr 判断是否值得自动重试：参数类错误（没配图等）不重试，等人工处理
func retriableVideoErr(s string) bool {
	if s == "" {
		return false
	}
	low := strings.ToLower(s)
	for _, kw := range []string{"还没选首帧图", "没有参考图", "找不到", "不存在", "至少要", "无效", "invalid", "参数"} {
		if strings.Contains(low, kw) {
			return false
		}
	}
	for _, kw := range []string{
		"429", "500", "502", "503", "504", "timeout", "超时", "deadline",
		"connection", "connect", "network", "网络", "reset", "eof", "tls", "broken pipe",
		"upstream", "reach", "do_request", "failed to", "too many", "限流", "rate", "busy", "繁忙", "retry",
	} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

// videoRetryDelay 第 attempt 次重试前的等待
func videoRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 60 * time.Second
	case 2:
		return 180 * time.Second
	default:
		return 300 * time.Second
	}
}

// maybeScheduleVideoRetry 失败后调度自动重试。返回 (是否已安排, 等待时长)。
// video_id 在失败前已落库，重试走 resume 续轮询/续下载，不重复创建、不浪费额度。
func maybeScheduleVideoRetry(item, errText string) (bool, time.Duration) {
	if !retriableVideoErr(errText) {
		return false, 0
	}
	videoRetryMu.Lock()
	cnt := videoRetryCnt[item] + 1
	if cnt > videoRetryMax {
		videoRetryCnt[item] = 0
		videoRetryMu.Unlock()
		return false, 0
	}
	videoRetryCnt[item] = cnt
	videoRetryMu.Unlock()
	delay := videoRetryDelay(cnt)
	vlog("", fmt.Sprintf("%s 失败（%s），%s 后自动重试（第 %d/%d 次）",
		item, truncateStr(errText, 80), delay, cnt, videoRetryMax))
	time.AfterFunc(delay, func() {
		if strings.HasPrefix(item, "shot:") {
			var sid int
			fmt.Sscanf(item, "shot:%d", &sid)
			enqueueShot(sid, true)
		} else if strings.HasPrefix(item, "clip:") {
			var cid int
			fmt.Sscanf(item, "clip:%d", &cid)
			enqueueClip(cid, true)
		}
	})
	return true, delay
}

func videoRetryReset(item string) {
	videoRetryMu.Lock()
	delete(videoRetryCnt, item)
	videoRetryMu.Unlock()
}

func nowStr() string { return time.Now().Format("2006-01-02 15:04:05") }

// startVideoWorkers 一把 key 一个 worker，单个 worker 严格串行
func startVideoWorkers() {
	gVideoClients = newVideoClients(gSettings)
	for i, cli := range gVideoClients {
		if !cli.OK {
			continue
		}
		go func(idx int, c *VideoClient) {
			tag := fmt.Sprintf("%d号", idx+1)
			for item := range vq {
				var err error
				if strings.HasPrefix(item, "clip:") {
					var cid int
					fmt.Sscanf(item, "clip:%d", &cid)
					err = runClip(cid, c)
				} else if strings.HasPrefix(item, "shot:") {
					var sid int
					fmt.Sscanf(item, "shot:%d", &sid)
					err = runShot(sid, c)
				}
				if err != nil {
					vlog(tag, "任务异常："+err.Error())
				}
				vqueuedMu.Lock()
				delete(vqueued, item)
				vqueuedMu.Unlock()
				progPop(item)
			}
		}(i, cli)
	}
}

func rebuildVideoClients() {
	gVideoClients = newVideoClients(gSettings)
}

// ---- films / shots CRUD ----

func loadFilm() (map[string]any, []map[string]any) {
	dbMu.Lock()
	defer dbMu.Unlock()
	conn, err := openDB()
	if err != nil {
		return nil, nil
	}
	defer conn.Close()
	var fid int
	var title, mode, created, updated string
	var seconds int
	var aspect, movie sql.NullString
	row := conn.QueryRow("SELECT id,title,mode,seconds,aspect,movie,created,updated FROM films ORDER BY id DESC LIMIT 1")
	if err := row.Scan(&fid, &title, &mode, &seconds, &aspect, &movie, &created, &updated); err != nil {
		return nil, nil
	}
	film := map[string]any{
		"id": fid, "title": title, "mode": mode, "seconds": seconds,
		"aspect": aspect.String, "movie": movie.String, "created": created, "updated": updated,
	}
	rows, err := conn.Query(
		"SELECT id,seq,img,img2,action,camera,seconds,prompt,status,video_id,result,error,elapsed,aspect,updated FROM shots WHERE film_id=? ORDER BY seq ASC, id ASC", fid)
	if err != nil {
		return film, nil
	}
	defer rows.Close()
	shots := []map[string]any{}
	for rows.Next() {
		var sid, seq int
		var img, img2, action, camera, prompt, status, videoID, result, errS, aspectS, upd sql.NullString
		var secs int
		var elapsed sql.NullFloat64
		if err := rows.Scan(&sid, &seq, &img, &img2, &action, &camera, &secs, &prompt,
			&status, &videoID, &result, &errS, &elapsed, &aspectS, &upd); err != nil {
			continue
		}
		shots = append(shots, map[string]any{
			"id": sid, "seq": seq, "img": img.String, "img2": img2.String,
			"action": action.String, "camera": camera.String, "seconds": secs,
			"prompt": prompt.String, "status": status.String, "video_id": videoID.String,
			"result": result.String, "error": errS.String, "elapsed": elapsed.Float64,
			"aspect": aspectS.String, "updated": upd.String,
		})
	}
	return film, shots
}

// saveFilm 整表覆盖保存（按 id 增量对齐，保住已有状态/video_id）
func saveFilm(payload map[string]any) (map[string]any, []map[string]any) {
	title := strings.TrimSpace(str(payload, "title"))
	if title == "" {
		title = "未命名分镜"
	}
	mode := str(payload, "mode")
	if mode != "cut" && mode != "dual" {
		mode = "dual"
	}
	seconds := clamp(asInt(payload["seconds"], 5), 4, 12)
	wantAspect := strings.TrimSpace(str(payload, "aspect"))
	shotsIn, _ := payload["shots"].([]any)

	dbMu.Lock()
	conn, err := openDB()
	if err != nil {
		dbMu.Unlock()
		return loadFilm()
	}
	defer conn.Close()

	// 比例：传了合法值就用，没传就沿用旧值
	if !aspectOK(wantAspect) {
		var old sql.NullString
		_ = conn.QueryRow("SELECT aspect FROM films ORDER BY id DESC LIMIT 1").Scan(&old)
		wantAspect = old.String
		if !aspectOK(wantAspect) {
			wantAspect = "16:9"
		}
	}

	var fid int
	var have sql.NullInt64
	_ = conn.QueryRow("SELECT id FROM films ORDER BY id DESC LIMIT 1").Scan(&have)
	now := nowStr()
	if have.Valid {
		fid = int(have.Int64)
		_, _ = conn.Exec("UPDATE films SET title=?,mode=?,seconds=?,aspect=?,updated=? WHERE id=?",
			title, mode, seconds, wantAspect, now, fid)
	} else {
		res, err := conn.Exec("INSERT INTO films(title,mode,seconds,aspect,created,updated) VALUES(?,?,?,?,?,?)",
			title, mode, seconds, wantAspect, now, now)
		if err != nil {
			dbMu.Unlock()
			return loadFilm()
		}
		fid64, _ := res.LastInsertId()
		fid = int(fid64)
	}

	keep := map[int]bool{}
	for i, raw := range shotsIn {
		s, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		sid := asInt(s["id"], 0)
		img := strings.TrimSpace(str(s, "img"))
		img2 := strings.TrimSpace(str(s, "img2"))
		action := strings.TrimSpace(str(s, "action"))
		camera := strings.TrimSpace(str(s, "camera"))
		ssecs := clamp(asInt(s["seconds"], seconds), 4, 12)
		prompt := strings.TrimSpace(str(s, "prompt"))
		if sid > 0 {
			var exists int
			if err := conn.QueryRow("SELECT 1 FROM shots WHERE id=?", sid).Scan(&exists); err == nil {
				_, _ = conn.Exec(
					"UPDATE shots SET film_id=?,seq=?,img=?,img2=?,action=?,camera=?,seconds=?,prompt=?,updated=? WHERE id=?",
					fid, i+1, img, img2, action, camera, ssecs, prompt, now, sid)
				keep[sid] = true
				continue
			}
			sid = 0
		}
		res, err := conn.Exec(
			"INSERT INTO shots(film_id,seq,img,img2,action,camera,seconds,prompt,status,updated) VALUES(?,?,?,?,?,?,?,?,'pending',?)",
			fid, i+1, img, img2, action, camera, ssecs, prompt, now)
		if err == nil {
			if lid, err := res.LastInsertId(); err == nil {
				keep[int(lid)] = true
			}
		}
	}
	// 删掉不在 keep 里的
	rows, err := conn.Query("SELECT id FROM shots WHERE film_id=?", fid)
	if err == nil {
		var toDel []int
		for rows.Next() {
			var id int
			if rows.Scan(&id) == nil && !keep[id] {
				toDel = append(toDel, id)
			}
		}
		rows.Close()
		for _, id := range toDel {
			_, _ = conn.Exec("DELETE FROM shots WHERE id=?", id)
		}
	}
	dbMu.Unlock()
	return loadFilm()
}

func setShot(sid int, fields map[string]any) {
	fields["updated"] = nowStr()
	dbMu.Lock()
	defer dbMu.Unlock()
	conn, err := openDB()
	if err != nil {
		return
	}
	defer conn.Close()
	setFields(conn, "shots", sid, fields)
}

func setFields(conn *sql.DB, table string, id int, fields map[string]any) {
	cols := []string{"updated=?"}
	vals := []any{fields["updated"]}
	for k, v := range fields {
		if k == "updated" {
			continue
		}
		cols = append(cols, k+"=?")
		vals = append(vals, v)
	}
	vals = append(vals, id)
	_, _ = conn.Exec(fmt.Sprintf("UPDATE %s SET %s WHERE id=?", table, strings.Join(cols, ",")), vals...)
}

// resolveImg 把 img 字段（/out/…、/shots-img/… 或绝对路径）解析回磁盘路径
func resolveImg(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	u = strings.ReplaceAll(u, "\\", "/")
	if dec, err := url.PathUnescape(u); err == nil {
		u = dec
	}
	if strings.HasPrefix(u, "/out/") {
		p := filepath.Join(gOut, filepath.FromSlash(u[len("/out/"):]))
		if fileExists(p) {
			return p
		}
		return ""
	}
	if strings.HasPrefix(u, "/shots-img/") {
		p := filepath.Join(dataDir(), "shot_imgs", filepath.Base(u))
		if fileExists(p) {
			return p
		}
		return ""
	}
	if fileExists(u) {
		return u
	}
	return ""
}

// resultURL 把 result 值统一成 (播放URL, 磁盘路径)
func resultURL(v string) (string, string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", ""
	}
	if strings.HasPrefix(v, "/out/") {
		p := resolveImg(v)
		if p == "" {
			return "", ""
		}
		return v, p
	}
	if fileExists(v) {
		rel, err := filepath.Rel(gOut, v)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return "/out/" + urlEscape(filepath.ToSlash(rel)), v
		}
		return v, v
	}
	return v, ""
}

var vidSafeRe = regexp.MustCompile(`[^0-9A-Za-z_-]`)

// buildShotPrompt 自动拼装 + 手改覆盖
func buildShotPrompt(shot map[string]any) string {
	if manual := strings.TrimSpace(str(shot, "prompt")); manual != "" {
		return manual
	}
	action := strings.TrimSpace(str(shot, "action"))
	cam := CAMERA_MAP[strings.TrimSpace(str(shot, "camera"))]
	var lines []string
	if action != "" {
		lines = append(lines, period(action))
	}
	if cam != "" {
		lines = append(lines, cam)
	}
	lines = append(lines, "画面风格与首帧完全一致，角色外观、服饰、面部特征保持不变，动作自然连续，无变形无闪烁。")
	return strings.Join(lines, "\n")
}

// enqueueShot 入队一镜
func enqueueShot(sid int, resume bool) bool {
	key := progKey(sid)
	if !resume {
		videoRetryReset(key) // 手动触发（重做/生成）重置自动重试计数
	}
	vqueuedMu.Lock()
	if vqueued[key] {
		vqueuedMu.Unlock()
		return false
	}
	vqueued[key] = true
	vqueuedMu.Unlock()
	errMsg := ""
	if resume {
		errMsg = "等待恢复轮询"
	}
	setShot(sid, map[string]any{"status": "queued", "error": errMsg})
	progSet(key, "排队中", map[string]any{"secs": 0, "progress": nil, "resume": resume})
	vq <- key
	return true
}

// runShot 跑一镜。已有 video_id → 跳过创建直接续轮询。
func runShot(sid int, cli *VideoClient) error {
	film, shots := loadFilm()
	if film == nil {
		return nil
	}
	var shot map[string]any
	for _, s := range shots {
		if asInt(s["id"], 0) == sid {
			shot = s
			break
		}
	}
	if shot == nil {
		return nil
	}
	seq := asInt(shot["seq"], 0)
	label := fmt.Sprintf("#%02d", seq)
	filmID := asInt(film["id"], 0)
	mode := str(film, "mode")
	if mode != "cut" && mode != "dual" {
		mode = "dual"
	}
	aspect := str(film, "aspect")
	if !aspectOK(aspect) {
		aspect = "16:9"
	}
	key := progKey(sid)
	item := fmt.Sprintf("shot:%d", sid)

	t0 := time.Now()
	// fail：失败统一收口——落库 error，可重试的安排自动重试（重试期间进度条显示倒计时）
	fail := func(e error) {
		el := round1(time.Since(t0).Seconds())
		setShot(sid, map[string]any{"status": "error", "error": truncateStr(e.Error(), 500), "elapsed": el})
		if ok, delay := maybeScheduleVideoRetry(item, e.Error()); ok {
			progSet(key, fmt.Sprintf("%s 后自动重试", delay), map[string]any{"error": truncateStr(e.Error(), 200)})
			vlog(label, fmt.Sprintf("失败：%s（%s 后自动重试）", truncateStr(e.Error(), 300), delay))
		} else {
			progSet(key, "失败", map[string]any{"elapsed": el, "error": truncateStr(e.Error(), 200)})
			vlog(label, "失败："+truncateStr(e.Error(), 300))
		}
	}
	existing := strings.TrimSpace(str(shot, "video_id"))
	resume := existing != ""
	var vid string
	var err error

	if resume {
		progSet(key, "恢复轮询中", map[string]any{"resume": true})
		vlog(label, fmt.Sprintf("已有任务 %s，直接从轮询接手（不重新创建，不浪费额度）", existing))
		vid = existing
	} else {
		fp := resolveImg(str(shot, "img"))
		if fp == "" {
			err = fmt.Errorf("这一镜还没选首帧图")
		} else {
			var first, last []byte
			first, err = normFrame(fp, func(m string) { vlog(label, m) })
			if err == nil && mode == "dual" {
				var nxt map[string]any
				for _, s := range shots {
					if asInt(s["seq"], 0) == seq+1 {
						nxt = s
						break
					}
				}
				lfURL := strings.TrimSpace(str(shot, "img2"))
				if lfURL == "" && nxt != nil {
					lfURL = str(nxt, "img")
				}
				if lfp := resolveImg(lfURL); lfp != "" {
					last, err = normFrame(lfp, func(m string) { vlog(label, m) })
					if err != nil {
						last = nil
					}
				} else {
					vlog(label, "没有下一镜的图，本镜自动退化为单帧（硬切效果）")
				}
			}
			if err == nil {
				prompt := buildShotPrompt(shot)
				setShot(sid, map[string]any{"status": "running", "error": "", "result": "", "aspect": aspect})
				progSet(key, "创建任务中", map[string]any{"resume": false})
				secs := clamp(asInt(shot["seconds"], 5), 4, 12)
				vlog(label, fmt.Sprintf("创建任务（%s，%d 秒，%s）", MODE_LABEL[mode], secs, aspect))
				vid, err = cli.Create(prompt, "keyframe", secs, first, last, nil, aspect,
					func(m string) { vlog(label, m) }, 6, 180)
				if err == nil {
					setShot(sid, map[string]any{"video_id": vid})
					vlog(label, fmt.Sprintf("video_id = %s，开始轮询（4 秒一次）", vid))
				}
			}
		}
	}
	if err != nil {
		fail(err)
		return nil
	}

	progSet(key, "生成中", map[string]any{"resume": true})
	res, werr := cli.Wait(vid, func(m string) { vlog(label, m) },
		func(st string, pg any, secs int) {
			stage := "生成中"
			if st != "" {
				stage = fmt.Sprintf("生成中（%s）", st)
			}
			progSet(key, stage, map[string]any{"secs": secs, "progress": pg})
		}, 1800)
	if werr != nil {
		fail(werr)
		return nil
	}

	safeVid := vidSafeRe.ReplaceAllString(vid, "")
	if len(safeVid) > 14 {
		safeVid = safeVid[:14]
	}
	if safeVid == "" {
		safeVid = "vid"
	}
	out := filepath.Join(gOut, "films", fmt.Sprintf("shot_%d_%02d_%s.mp4", filmID, seq, safeVid))
	if derr := cli.Download(res, out, func(m string) { vlog(label, m) }); derr != nil {
		fail(derr)
		return nil
	}
	el := round1(time.Since(t0).Seconds())
	rel := "/out/films/" + urlEscape(filepath.Base(out))
	setShot(sid, map[string]any{"status": "done", "result": rel, "error": "", "elapsed": el, "video_id": vid})
	progSet(key, "完成", map[string]any{"elapsed": el})
	vlog(label, fmt.Sprintf("完成，用时 %.1f 秒", el))
	videoRetryReset(item)
	return nil
}

// ---- clips CRUD ----

func clipRefs(raw any) []string {
	arr, ok := raw.([]any)
	if !ok {
		if s, ok2 := raw.(string); ok2 {
			var v []any
			if json.Unmarshal([]byte(s), &v) == nil {
				arr = v
			} else {
				return nil
			}
		} else {
			return nil
		}
	}
	var out []string
	for _, x := range arr {
		if x == nil {
			continue
		}
		s := strings.TrimSpace(fmt.Sprintf("%v", x))
		if s != "" && s != "<nil>" {
			out = append(out, s)
		}
	}
	return out
}

func getClip(cid int) map[string]any {
	dbMu.Lock()
	defer dbMu.Unlock()
	conn, err := openDB()
	if err != nil {
		return nil
	}
	defer conn.Close()
	return scanClip(conn.QueryRow(
		"SELECT id,title,prompt,mode,seconds,aspect,refs,status,video_id,result,error,elapsed,created,updated FROM clips WHERE id=?", cid))
}

func scanClip(row *sql.Row) map[string]any {
	var cid int
	var title, mode string
	var seconds int
	var prompt, aspect, refs, status, videoID, result, errS, created, updated sql.NullString
	var elapsed sql.NullFloat64
	if err := row.Scan(&cid, &title, &prompt, &mode, &seconds, &aspect, &refs, &status,
		&videoID, &result, &errS, &elapsed, &created, &updated); err != nil {
		return nil
	}
	return map[string]any{
		"id": cid, "title": title, "prompt": prompt.String, "mode": mode, "seconds": seconds,
		"aspect": aspect.String, "refs": refs.String, "status": status.String,
		"video_id": videoID.String, "result": result.String, "error": errS.String,
		"elapsed": elapsed.Float64, "created": created.String, "updated": updated.String,
	}
}

func listClips(limit int) []map[string]any {
	dbMu.Lock()
	defer dbMu.Unlock()
	conn, err := openDB()
	if err != nil {
		return nil
	}
	defer conn.Close()
	rows, err := conn.Query(
		"SELECT id,title,prompt,mode,seconds,aspect,refs,status,video_id,result,error,elapsed,created,updated FROM clips ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		if c := scanClipRow(rows); c != nil {
			out = append(out, c)
		}
	}
	return out
}

func scanClipRow(rows *sql.Rows) map[string]any {
	var cid int
	var title, mode string
	var seconds int
	var prompt, aspect, refs, status, videoID, result, errS, created, updated sql.NullString
	var elapsed sql.NullFloat64
	if err := rows.Scan(&cid, &title, &prompt, &mode, &seconds, &aspect, &refs, &status,
		&videoID, &result, &errS, &elapsed, &created, &updated); err != nil {
		return nil
	}
	return map[string]any{
		"id": cid, "title": title, "prompt": prompt.String, "mode": mode, "seconds": seconds,
		"aspect": aspect.String, "refs": refs.String, "status": status.String,
		"video_id": videoID.String, "result": result.String, "error": errS.String,
		"elapsed": elapsed.Float64, "created": created.String, "updated": updated.String,
	}
}

func saveClip(payload map[string]any) map[string]any {
	cid := asInt(payload["id"], 0)
	title := strings.TrimSpace(str(payload, "title"))
	if title == "" {
		title = "未命名片段"
	}
	prompt := strings.TrimSpace(str(payload, "prompt"))
	mode := str(payload, "mode")
	if _, ok := CLIP_MODES[mode]; !ok {
		mode = "reference"
	}
	seconds := clamp(asInt(payload["seconds"], 5), 4, 12)
	aspect := strings.TrimSpace(str(payload, "aspect"))
	if !aspectOK(aspect) {
		aspect = "16:9"
	}
	refs := clipRefs(payload["refs"])
	if len(refs) > REF_MAX {
		refs = refs[:REF_MAX]
	}
	refsJSON, _ := json.Marshal(refs)

	dbMu.Lock()
	conn, err := openDB()
	if err == nil {
		now := nowStr()
		if cid > 0 {
			var exists int
			if conn.QueryRow("SELECT 1 FROM clips WHERE id=?", cid).Scan(&exists) == nil {
				_, _ = conn.Exec(
					"UPDATE clips SET title=?,prompt=?,mode=?,seconds=?,aspect=?,refs=?,updated=? WHERE id=?",
					title, prompt, mode, seconds, aspect, string(refsJSON), now, cid)
			} else {
				cid = 0
			}
		}
		if cid == 0 {
			res, e := conn.Exec(
				"INSERT INTO clips(title,prompt,mode,seconds,aspect,refs,status,created,updated) VALUES(?,?,?,?,?,?,'pending',?,?)",
				title, prompt, mode, seconds, aspect, string(refsJSON), now, now)
			if e == nil {
				if lid, e2 := res.LastInsertId(); e2 == nil {
					cid = int(lid)
				}
			}
		}
	}
	dbMu.Unlock()
	return getClip(cid)
}

func setClip(cid int, fields map[string]any) {
	fields["updated"] = nowStr()
	dbMu.Lock()
	defer dbMu.Unlock()
	conn, err := openDB()
	if err != nil {
		return
	}
	defer conn.Close()
	setFields(conn, "clips", cid, fields)
}

func deleteClip(cid int) {
	dbMu.Lock()
	defer dbMu.Unlock()
	conn, err := openDB()
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.Exec("DELETE FROM clips WHERE id=?", cid)
}

func clipPayload(c map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range c {
		out[k] = v
	}
	refs := clipRefs(c["refs"])
	refsAny := make([]any, 0, len(refs))
	var refsOK []bool
	for _, u := range refs {
		refsAny = append(refsAny, u)
		refsOK = append(refsOK, resolveImg(u) != "")
	}
	out["refs"] = refsAny
	out["refs_ok"] = refsOK
	resURL, resFile := resultURL(str(c, "result"))
	out["result"] = resURL
	out["result_file"] = resFile
	out["prog"] = progSnapshot()[clipKey(asInt(c["id"], 0))]
	return out
}

func clipPayloads(limit int) []map[string]any {
	out := []map[string]any{}
	for _, c := range listClips(limit) {
		out = append(out, clipPayload(c))
	}
	return out
}

func enqueueClip(cid int, resume bool) bool {
	key := clipKey(cid)
	if !resume {
		videoRetryReset(key) // 手动触发（重做/生成）重置自动重试计数
	}
	vqueuedMu.Lock()
	if vqueued[key] {
		vqueuedMu.Unlock()
		return false
	}
	vqueued[key] = true
	vqueuedMu.Unlock()
	errMsg := ""
	if resume {
		errMsg = "等待恢复轮询"
	}
	setClip(cid, map[string]any{"status": "queued", "error": errMsg})
	progSet(key, "排队中", map[string]any{"secs": 0, "progress": nil, "resume": resume})
	vq <- key
	return true
}

var picTokenRe = regexp.MustCompile(`@图\s*(\d+)`)

// runClip 跑一条垫图任务。@图N → <Picture N> 只在发请求这一刻转换。
func runClip(cid int, cli *VideoClient) error {
	c := getClip(cid)
	if c == nil {
		return nil
	}
	key := clipKey(cid)
	mode := str(c, "mode")
	if _, ok := CLIP_MODES[mode]; !ok {
		mode = "reference"
	}
	aspect := str(c, "aspect")
	if !aspectOK(aspect) {
		aspect = "16:9"
	}
	label := fmt.Sprintf("片段#%d", cid)
	item := fmt.Sprintf("clip:%d", cid)

	t0 := time.Now()
	// fail：失败统一收口——落库 error，可重试的安排自动重试
	fail := func(e error) {
		el := round1(time.Since(t0).Seconds())
		setClip(cid, map[string]any{"status": "error", "error": truncateStr(e.Error(), 400), "elapsed": el})
		if ok, delay := maybeScheduleVideoRetry(item, e.Error()); ok {
			progSet(key, fmt.Sprintf("%s 后自动重试", delay), map[string]any{"error": truncateStr(e.Error(), 200)})
			vlog(label, fmt.Sprintf("失败：%s（%s 后自动重试）", truncateStr(e.Error(), 200), delay))
		} else {
			progSet(key, "失败", map[string]any{"elapsed": el})
			vlog(label, "失败："+truncateStr(e.Error(), 200))
		}
	}
	existing := strings.TrimSpace(str(c, "video_id"))
	resume := existing != ""
	var vid string
	var err error

	if resume {
		progSet(key, "恢复轮询中", map[string]any{"resume": true})
		vlog(label, fmt.Sprintf("已有任务 %s，直接从轮询接手", existing))
		vid = existing
	} else {
		var refsB64 [][]byte
		if mode == "reference" {
			urls := clipRefs(c["refs"])
			if len(urls) == 0 {
				err = fmt.Errorf("垫图模式至少要有 1 张参考图")
			}
			for i, u := range urls {
				if err != nil {
					break
				}
				p := resolveImg(u)
				if p == "" {
					err = fmt.Errorf("第 %d 张参考图找不到了：%s", i+1, u)
					break
				}
				var b []byte
				b, err = normFrame(p, func(m string) { vlog(label, m) })
				if err == nil {
					refsB64 = append(refsB64, b)
				}
			}
		}
		if err == nil {
			prompt := strings.TrimSpace(str(c, "prompt"))
			if prompt == "" {
				err = fmt.Errorf("提示词是空的")
			} else {
				prompt = picTokenRe.ReplaceAllString(prompt, "<Picture $1>")
				setClip(cid, map[string]any{"status": "running", "error": "", "result": ""})
				progSet(key, "创建任务中", map[string]any{"resume": false})
				secs := clamp(asInt(c["seconds"], 5), 4, 12)
				vlog(label, fmt.Sprintf("创建任务（%s，%d 秒，%s，参考图 %d 张）",
					CLIP_MODES[mode], secs, aspect, len(refsB64)))
				vid, err = cli.Create(prompt, mode, secs, nil, nil, refsB64, aspect,
					func(m string) { vlog(label, m) }, 6, 180)
				if err == nil {
					setClip(cid, map[string]any{"video_id": vid})
					vlog(label, fmt.Sprintf("video_id = %s，开始轮询（4 秒一次）", vid))
				}
			}
		}
	}
	if err != nil {
		fail(err)
		return nil
	}

	progSet(key, "生成中", map[string]any{"resume": true})
	res, werr := cli.Wait(vid, func(m string) { vlog(label, m) },
		func(st string, pg any, secs int) {
			stage := "生成中"
			if st != "" {
				stage = fmt.Sprintf("生成中（%s）", st)
			}
			progSet(key, stage, map[string]any{"secs": secs, "progress": pg})
		}, 1800)
	if werr != nil {
		fail(werr)
		return nil
	}

	safe := vidSafeRe.ReplaceAllString(vid, "")
	if len(safe) > 14 {
		safe = safe[:14]
	}
	if safe == "" {
		safe = "vid"
	}
	out := filepath.Join(gOut, "films", fmt.Sprintf("clip_%d_%s.mp4", cid, safe))
	if derr := cli.Download(res, out, func(m string) { vlog(label, m) }); derr != nil {
		fail(derr)
		return nil
	}
	el := round1(time.Since(t0).Seconds())
	setClip(cid, map[string]any{"status": "done", "result": "/out/films/" + urlEscape(filepath.Base(out)),
		"error": "", "elapsed": el, "video_id": vid})
	progSet(key, "完成", map[string]any{"elapsed": el})
	vlog(label, fmt.Sprintf("完成，用时 %.1f 秒", el))
	videoRetryReset(item)
	return nil
}

// resumePending 扫库，把未完成的镜/片段重新入队
func resumePending() map[string]any {
	nPoll, nRedo := 0, 0
	vqueuedMu.Lock()
	busy := map[string]bool{}
	for k := range vqueued {
		busy[k] = true
	}
	vqueuedMu.Unlock()

	_, shots := loadFilm()
	for _, s := range shots {
		st := str(s, "status")
		if st != "queued" && st != "running" {
			continue
		}
		key := progKey(asInt(s["id"], 0))
		if busy[key] {
			continue
		}
		hasVid := strings.TrimSpace(str(s, "video_id")) != ""
		if enqueueShot(asInt(s["id"], 0), hasVid) {
			if hasVid {
				nPoll++
			} else {
				nRedo++
			}
		}
	}
	for _, c := range listClips(200) {
		st := str(c, "status")
		if st != "queued" && st != "running" {
			continue
		}
		key := clipKey(asInt(c["id"], 0))
		if busy[key] {
			continue
		}
		hasVid := strings.TrimSpace(str(c, "video_id")) != ""
		if enqueueClip(asInt(c["id"], 0), hasVid) {
			if hasVid {
				nPoll++
			} else {
				nRedo++
			}
		}
	}
	return map[string]any{"resume_poll": nPoll, "requeue": nRedo, "total": nPoll + nRedo}
}

// ---- 拼接 ----

func ff(args []string, timeout int) (int, string) {
	cmd := exec.Command(FFMPEG, append([]string{"-hide_banner"}, args...)...)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return -1, err.Error()
	}
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(time.Duration(timeout) * time.Second):
		cmd.Process.Kill()
		return -1, fmt.Sprintf("ffmpeg 超时（%d 秒）", timeout)
	case err := <-done:
		s := combined.String()
		if err != nil {
			return 1, s
		}
		return 0, s
	}
}

// concatShots 按顺序拼接选中镜头。各段分辨率必须一致。
func concatShots(ids []int) (map[string]any, error) {
	if FFMPEG == "" {
		return nil, fmt.Errorf("没找到 ffmpeg，拼接做不了")
	}
	_, shots := loadFilm()
	byID := map[int]map[string]any{}
	for _, s := range shots {
		byID[asInt(s["id"], 0)] = s
	}
	type picked_t struct {
		seq int
		p   string
	}
	var picked []picked_t
	for _, id := range ids {
		s := byID[id]
		if s == nil {
			continue
		}
		if str(s, "status") != "done" || strings.TrimSpace(str(s, "result")) == "" {
			return nil, fmt.Errorf("第 %02d 镜还没生成完，不能拼", asInt(s["seq"], 0))
		}
		p := resolveImg(str(s, "result"))
		if p == "" {
			return nil, fmt.Errorf("第 %02d 镜的文件找不到：%s", asInt(s["seq"], 0), str(s, "result"))
		}
		picked = append(picked, picked_t{asInt(s["seq"], 0), p})
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("没勾选任何一镜")
	}
	for i := 0; i < len(picked); i++ {
		for j := i + 1; j < len(picked); j++ {
			if picked[j].seq < picked[i].seq {
				picked[i], picked[j] = picked[j], picked[i]
			}
		}
	}

	// 各段分辨率必须一致
	dims := map[[2]int][]int{}
	for _, pk := range picked {
		w, h := probeWH(pk.p)
		if w != 0 && h != 0 {
			dims[[2]int{w, h}] = append(dims[[2]int{w, h}], pk.seq)
		}
	}
	if len(dims) > 1 {
		var detail string
		for wh, seqs := range dims {
			var ss []string
			for _, q := range seqs {
				ss = append(ss, fmt.Sprintf("%02d", q))
			}
			detail += fmt.Sprintf("%d×%d 的有第 %s 镜；", wh[0], wh[1], strings.Join(ss, "、"))
		}
		return nil, fmt.Errorf("这几段画幅不一样，拼出来会花屏或失败：%s。多半是中途改过「比例」——把对不上的那几镜点「重抽」，或把比例改回去再重抽。", detail)
	}

	stamp := time.Now().Format("20060102_150405")
	listDir := filepath.Join(gOut, "films", "_lists")
	_ = os.MkdirAll(listDir, 0o755)
	listfile := filepath.Join(listDir, fmt.Sprintf("concat_%s.txt", stamp))
	var lf strings.Builder
	for _, pk := range picked {
		posix := strings.ReplaceAll(pk.p, "\\", "/")
		lf.WriteString(fmt.Sprintf("file '%s'\n", posix))
	}
	if err := os.WriteFile(listfile, []byte(lf.String()), 0o644); err != nil {
		return nil, err
	}
	out := filepath.Join(gOut, "films", fmt.Sprintf("成片_%s.mp4", stamp))

	t0 := time.Now()
	code, errTxt := ff([]string{"-y", "-f", "concat", "-safe", "0", "-i", listfile, "-c", "copy", out}, 900)
	mode := "copy"
	if code != 0 {
		vlog("", "concat -c copy 失败（各段音轨/参数可能不一致），回退重编码")
		code, errTxt = ff([]string{"-y", "-f", "concat", "-safe", "0", "-i", listfile,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "20",
			"-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", out}, 1800)
		mode = "reencode"
		if code != 0 {
			return nil, fmt.Errorf("拼接失败：%s", truncateStr(errTxt, 400))
		}
	}
	el := round1(time.Since(t0).Seconds())
	rel := "/out/films/" + urlEscape(filepath.Base(out))
	var mb float64
	if st, err := os.Stat(out); err == nil {
		mb = float64(int(st.Size()/1024/1024*100)) / 100
	}
	dbMu.Lock()
	conn, err := openDB()
	if err == nil {
		_, _ = conn.Exec("UPDATE films SET movie=?, updated=? WHERE id=(SELECT id FROM films ORDER BY id DESC LIMIT 1)",
			rel, nowStr())
		conn.Close()
	}
	dbMu.Unlock()
	return map[string]any{"url": rel, "file": out, "mode": mode, "seconds": el, "count": len(picked), "mb": mb}, nil
}
