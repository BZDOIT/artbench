package main

// AGNES 文本推理通道（chat/completions + /v1/models 探测）
// 官方 wiki：平台支持"文本生成与推理"，OpenAI 兼容，与生图共用 base_url + 同一个 key 池。
// text_model 留空 = 不启用推荐/SC-021 动态解析功能。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// textModel 当前文本/推理模型名（空 = 未启用）
func textModel() string {
	s := snapshotSettings()
	return strings.TrimSpace(s.TextModel)
}

// chatComplete 调 chat/completions，复用生图 key 池轮询 + 429 冷却。
// 实测（2026-09-28）：该平台非流式请求会被服务器 hold 住不回响应头（180s+ 超时），
// 必须走 SSE 流式，收完 delta 拼接全文。system/user 两段消息；失败换 key 重试 2 次。
func chatComplete(system, user string, onLog func(string)) (string, error) {
	ag := getAgnes()
	tm := textModel()
	if tm == "" {
		return "", fmt.Errorf("未配置文本/推理模型（设置里填「文本/推理模型名」后才启用）")
	}
	if !ag.OK {
		return "", fmt.Errorf("没读到 API key，检查 %s 里的 AGNES_API_KEY", gCfg.EnvFile)
	}
	base := ag.Base
	useKey := ag.nextKey()

	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	body := map[string]any{
		"model":       tm,
		"messages":    []msg{{Role: "system", Content: system}, {Role: "user", Content: user}},
		"temperature": 0.7,
		"stream":      true,
	}
	data, _ := json.Marshal(body)
	url := base + "/chat/completions"

	// 流式整程上限 10 分钟（首包实测 20-25s，大输入完整生成可能数分钟）
	client := &http.Client{Timeout: 600 * time.Second}
	var lastErr string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(data))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+useKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err.Error()
			if onLog != nil {
				onLog(fmt.Sprintf("文本推理网络异常（第 %d 次）：%s", attempt+1, lastErr))
			}
			continue
		}
		if resp.StatusCode == 429 {
			resp.Body.Close()
			coolKey(useKey, 60*time.Second)
			nxt := ag.nextKey()
			if onLog != nil {
				onLog(fmt.Sprintf("文本推理 key %s 撞限流，换 %s 重试", ktail(useKey), ktail(nxt)))
			}
			useKey = nxt
			lastErr = "HTTP 429"
			continue
		}
		if resp.StatusCode != 200 {
			payload, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
			resp.Body.Close()
			snippet := string(payload)
			lastErr = fmt.Sprintf("HTTP %d %s", resp.StatusCode, snippet)
			if onLog != nil {
				onLog(fmt.Sprintf("文本推理失败（第 %d 次）：%s", attempt+1, lastErr))
			}
			// 5xx 换 key 再试，4xx（参数/模型名错误）直接报
			if resp.StatusCode < 500 {
				return "", fmt.Errorf("%s", lastErr)
			}
			useKey = ag.nextKey()
			continue
		}
		// ---- SSE 流式读取，拼 choices[0].delta.content ----
		out, serr := readSSE(resp.Body, onLog)
		resp.Body.Close()
		if serr != nil {
			lastErr = serr.Error()
			if onLog != nil {
				onLog(fmt.Sprintf("文本推理流中断（第 %d 次）：%s", attempt+1, lastErr))
			}
			continue
		}
		if out == "" {
			lastErr = "模型输出为空"
			if onLog != nil {
				onLog(lastErr)
			}
			continue
		}
		return out, nil
	}
	if lastErr == "" {
		lastErr = "未知错误"
	}
	return "", fmt.Errorf("%s", lastErr)
}

// readSSE 逐行读 data: {...}，拼接 delta.content，直到 [DONE] 或 EOF
func readSSE(r io.Reader, onLog func(string)) (string, error) {
	type chunk struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	var b strings.Builder
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var c chunk
		if err := json.Unmarshal([]byte(payload), &c); err != nil {
			continue // 忽略无法解析的心跳/杂行
		}
		if c.Error != nil && c.Error.Message != "" {
			return "", fmt.Errorf("流内报错：%s", c.Error.Message)
		}
		for _, ch := range c.Choices {
			b.WriteString(ch.Delta.Content)
		}
	}
	if err := sc.Err(); err != nil {
		if b.Len() > 0 {
			if onLog != nil {
				onLog(fmt.Sprintf("流读取中断（已收 %d 字）：%v", utf8.RuneCountInString(b.String()), err))
			}
			return b.String(), nil // 收到一半断流：有内容就先用
		}
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

// listAgnesModels 探测 GET /v1/models，返回真实模型 id 清单
func listAgnesModels() ([]string, error) {
	ag := getAgnes()
	if !ag.OK {
		return nil, fmt.Errorf("key 池是空的，先加 key")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequest("GET", ag.Base+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+ag.Keys[0])
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		snippet := string(payload)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, snippet)
	}
	var pl struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &pl); err != nil {
		return nil, fmt.Errorf("响应解析失败：%v", err)
	}
	var out []string
	for _, m := range pl.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out, nil
}

// ---- 图文卡 AI 推荐 ----

// styleDigest 风格摘要（排除陷阱：陷阱没有正向特征，文本锚不住，推荐只会帮倒忙）。
// 实测全量 traits（42 字/条 × 321 条 ≈ 15K 字符）让 flash 模型推荐耗时 50-116 秒，
// 瘦身到 20 字特征后输入砍半，实测 30-50 秒；风格名本身（英文描述性）语义足够判断意境。
func styleDigest() string {
	var b strings.Builder
	for _, s := range gLib.Styles {
		if s.Trap {
			continue
		}
		traits := s.PosTraits
		r := []rune(traits)
		if len(r) > 20 {
			r = r[:20]
			traits = string(r) + "…"
		}
		b.WriteString(fmt.Sprintf("#%s %s（%s）%s\n", s.Number, s.GenerationName, s.Group, traits))
	}
	return b.String()
}

// colorDigest 36 配色清单
func colorDigest() string {
	var b strings.Builder
	for _, c := range gLib.Colors {
		pz := c.PromptZh
		r := []rune(pz)
		if len(r) > 60 {
			r = r[:60]
			pz = string(r) + "…"
		}
		b.WriteString(fmt.Sprintf("%s %s：%s\n", c.ID, c.Name, pz))
	}
	return b.String()
}

const recommendSystem = `你是中文插画项目的风格搭配顾问。用户会给你一个图文卡主题、可选风格库摘要（带编号）和配色库清单（带编号）。
你的任务：选出最贴合主题意境的一种风格和一套配色，也可以判断某项不指定（让生图模型自由发挥）。
只输出一个 JSON 对象，不要 markdown 代码块、不要任何解释文字，格式：
{"style_no":"三位数字编号如018，不指定填空字符串","color_id":"C-xx，不指定填空字符串","reason":"60字以内说明理由"}`

// cardRecommend 图文卡 AI 推荐：主题 + 风格摘要 + 配色清单 → 文本模型 → style/color/reason
func cardRecommend(theme string, onLog func(string)) (styleNo, colorID, reason string, err error) {
	user := fmt.Sprintf("【图文卡主题】\n%s\n\n【风格库摘要】（#编号 名称（分组）特征，共 %d 款，陷阱编号已剔除）\n%s\n\n【配色库清单】\n%s",
		theme, len(gLib.Styles)-gLib.TrapCount(), styleDigest(), colorDigest())
	raw, err := chatComplete(recommendSystem, user, onLog)
	if err != nil {
		return "", "", "", err
	}
	var out struct {
		StyleNo string `json:"style_no"`
		ColorID string `json:"color_id"`
		Reason  string `json:"reason"`
	}
	if err := parseLooseJSON(raw, &out); err != nil {
		return "", "", "", fmt.Errorf("模型输出不是合法 JSON：%v（原文：%s）", err, truncateRunes(raw, 200))
	}
	styleNo = normalizeStyleNo(strings.TrimSpace(strings.TrimPrefix(out.StyleNo, "#")))
	if styleNo != "000" && gLib.ByNo[styleNo] != nil && gLib.ByNo[styleNo].Trap {
		// 模型挑了陷阱编号 → 拒绝重选一次
		return "", "", "", fmt.Errorf("模型推荐了陷阱编号 #%s（没有正向特征，锚不住风格），请重试", styleNo)
	}
	if styleNo == "000" || gLib.ByNo[styleNo] == nil {
		styleNo = ""
	}
	colorID = strings.TrimSpace(out.ColorID)
	if colorID != "" && gLib.Colors[colorID] == nil {
		colorID = ""
	}
	reason = strings.TrimSpace(out.Reason)
	return styleNo, colorID, reason, nil
}

// parseLooseJSON 宽松解析：剥掉 ``` 代码围栏，取第一个 { 到最后一个 }
func parseLooseJSON(raw string, v any) error {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			s = s[i : j+1]
		}
	}
	return json.Unmarshal([]byte(s), v)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ---- SC-021 动态解析 ----

// SC021_PLAYSTYLES 上游 SC-021 五大核心玩法（严格 Round-Robin 轮替，严禁连续重复）
var SC021_PLAYSTYLES = []string{
	"越界破框型：在纯白底色中设置单枚低饱和几何色块/容器，主体大部分在色块内，但关键部位（头部/手部/枝叶）自然突破色块边缘伸出，形成空间穿透感",
	"微缩浮岛型：主体大幅微缩为占画面 10%–25% 的精致纸面小景，仅由一抹清淡色带或基座承托，四周呈现大片静谧空旷留白",
	"记忆图谱平铺型：打破单一场景，居中放置主体，并在留白中错落平铺 3–6 枚原图记忆特征贴纸（带 NO. 编号），间距透光",
	"经典悬浮落地型：主体尺度适中（占 40%–55%），居中或微偏心站立，底部仅用一条极简地面摩擦线提示空间",
	"正负形咬合型：主体高度提炼为几何剪影，与背景正负形相互转换咬合",
}

const sc021System = `你是小红书社媒卡的提示词工程师，负责按上游 SC-021「自适应双拼照片转译社媒卡」版式规则，把给定的构图玩法写成一段单义、排他的中文执行提示词。
硬性规则：
1. 只使用指定的那一种构图玩法，绝不提及其他玩法，绝不"多选平铺"；
2. 必须包含背景净化要求：艺术转译区严禁照抄摄影区杂乱背景，四周边缘保留 40%–80% 干净负空间/呼吸留白底色（暖象牙白、米白棉麻纸纹），主体与文字向内部收拢、不贴边不爆框；
3. 必须包含画幅守恒要求：拼接时严禁修改或拉伸原图比例，竖版原图左右 1:1 双拼（总宽为原图两倍），横版原图/默认上下 1:1 双拼（总高为原图两倍），两区中线无缝拼接、无多余边框；
4. 必须包含"彻底去写实"要求：转译区只在摄影区提取五官标志特征、姿态神态与场景大轮廓，按所选风格从零重绘为纯粹 2D 手绘插画，坚决杜绝照片套滤镜；
5. 输出一段 150–260 字的连贯中文执行指令，可直接嵌入生图提示词；不要编号、不要 markdown、不要解释。`

// sc021StatePath 轮替状态文件（记上次用到的玩法序号，跨重启不连续重复）
func sc021StatePath() string { return filepath.Join(dataDir(), "sc021_state.json") }

// peekPlaystyle 读当前轮替序号（不推进）——预览用，保证预览看到的玩法和接下来生成的一致
func peekPlaystyle() int {
	last := -1
	if raw, err := os.ReadFile(sc021StatePath()); err == nil {
		var st struct {
			Last int `json:"last"`
		}
		if json.Unmarshal(raw, &st) == nil {
			last = st.Last
		}
	}
	return (last + 1) % len(SC021_PLAYSTYLES) // last=-1 时得 0，首次从玩法一开始
}

// advancePlaystyle 生成后推进轮替：写入刚用掉的序号
func advancePlaystyle(used int) {
	if b, err := json.Marshal(map[string]int{"last": used}); err == nil {
		_ = os.MkdirAll(dataDir(), 0o755)
		_ = os.WriteFile(sc021StatePath(), b, 0o644)
	}
}

// sc021PromptForPlaystyle 指定玩法序号生成单义执行提示词（预览和生成共用一条路径）
func sc021PromptForPlaystyle(theme string, style *Style, idx int, onLog func(string)) (string, error) {
	play := SC021_PLAYSTYLES[idx]
	styleTxt := "（未指定风格，转译区画风由生图模型按主题自主搭配）"
	if style != nil {
		styleTxt = fmt.Sprintf("%s。核心特征：%s", style.GenerationName, style.PosTraits)
	}
	user := fmt.Sprintf("【图文卡主题】%s\n【艺术转译区指定画风】%s\n【本次轮替到的构图玩法（唯一允许使用）】玩法%d：%s\n\n请按系统规则输出单义执行提示词。",
		theme, styleTxt, idx+1, play)
	if onLog != nil {
		onLog(fmt.Sprintf("SC-021 动态解析：本次轮替到玩法%d（%s）", idx+1, truncateRunes(play, 24)))
	}
	out, err := chatComplete(sc021System, user, onLog)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	out = strings.Trim(out, "\"“”")
	// 单行化：嵌入提示词更稳
	out = strings.ReplaceAll(out, "\r", " ")
	out = strings.ReplaceAll(out, "\n", " ")
	if utf8.RuneCountInString(out) < 40 {
		return "", fmt.Errorf("模型输出过短（%d 字），疑似异常", utf8.RuneCountInString(out))
	}
	return out, nil
}

// sc021DynamicPrompt 生成 + 推进轮替（生成路径用）
func sc021DynamicPrompt(theme string, style *Style, onLog func(string)) (string, error) {
	idx := peekPlaystyle()
	out, err := sc021PromptForPlaystyle(theme, style, idx, onLog)
	if err == nil {
		advancePlaystyle(idx) // 只有真用上了才推进，失败不跳号
	}
	return out, err
}
