package main

// 提示词拼装：设定表 / 图文卡 / 自由出图
// 迁移自 Python app.py 的 cn_num / split_list / _period / build_prompt / build_card_prompt / build_free_prompt

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var CN_DIGITS = []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九"}

func cnNum(n int) string {
	if n <= 0 {
		return strconv.Itoa(n)
	}
	if n < 10 {
		return CN_DIGITS[n]
	}
	if n == 10 {
		return "十"
	}
	if n < 20 {
		return "十" + CN_DIGITS[n%10]
	}
	head := "十"
	if n/10 > 1 {
		head = CN_DIGITS[n/10] + "十"
	}
	tail := ""
	if n%10 > 0 {
		tail = CN_DIGITS[n%10]
	}
	return head + tail
}

// splitList 接受 list 或「、,，;；\n」分隔的字符串
func splitList(v any) []string {
	var items []string
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				items = append(items, s)
			}
		}
	case []string:
		items = t
	case string:
		items = splitAny(t, "、,，;；\n")
	}
	var out []string
	for _, x := range items {
		x = strings.TrimSpace(x)
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

const DEFAULT_EXPRESSIONS = "平静、开心、惊讶、困惑、怀疑、困倦、震惊"
const DEFAULT_VIEWS = "正面、四分之三正面、侧面、四分之三背面、背面"

const _END_CHARS = "。！？!?.;；」』）)"

// period 补句号，但不重复补
func period(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return t
	}
	r := []rune(t)
	last := r[len(r)-1]
	for _, c := range _END_CHARS {
		if last == c {
			return t
		}
	}
	return t + "。"
}

// pstr 从请求 body 取字符串字段
func pstr(p map[string]any, key string) string {
	if v, ok := p[key].(string); ok {
		return v
	}
	return ""
}

// buildPrompt 设定表两段式拼装：风格头 + 结构段（6 区块）
func buildPrompt(p map[string]any, style *Style, lib *StyleLib) string {
	gen := strings.TrimSpace(style.GenerationName)
	feats := strings.TrimSpace(pstr(p, "style_feature"))
	if feats == "" {
		feats = style.PosTraits
	}

	name := strings.TrimSpace(pstr(p, "char_name"))
	if name == "" {
		name = "无名角色"
	}
	bg := strings.TrimSpace(pstr(p, "bg"))
	if bg == "" {
		bg = "米白色"
	}
	mode := strings.TrimSpace(pstr(p, "mode"))
	if mode == "" {
		mode = "full"
	}

	var lines []string
	// 上游规则：提示词内部严禁包含任何编号（#018 / SC-021 / C-01），防止生图模型把编号画进画面；编号只在界面上显示
	lines = append(lines, fmt.Sprintf("风格名称：%s。", gen))
	if feats != "" {
		lines = append(lines, "风格特征："+period(feats))
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("一张横向的角色设定表，%s背景，杂志式分格排版。", bg))
	lines = append(lines, "")

	// --- 01 档案
	seg := []string{"名字 " + name}
	if v := strings.TrimSpace(pstr(p, "identity")); v != "" {
		seg = append(seg, "身份 "+v)
	}
	if v := strings.TrimSpace(pstr(p, "age_form")); v != "" {
		seg = append(seg, "年龄形态 "+v)
	}
	if v := strings.TrimSpace(pstr(p, "personality")); v != "" {
		seg = append(seg, "性格 "+v)
	}
	if v := strings.TrimSpace(pstr(p, "world")); v != "" {
		seg = append(seg, "世界观题材 "+v)
	}
	card := strings.Join(seg, "，") + "。"
	if v := strings.TrimSpace(pstr(p, "slogan")); v != "" {
		v = strings.TrimRight(v, "。")
		card += fmt.Sprintf("人设标语「%s」", v)
	}

	type block struct {
		key, title, content string
	}
	blocks := []block{{"档案", "角色档案", card}}

	// --- 02 转面
	views := splitList(p["views"])
	if len(views) == 0 {
		views = splitList(DEFAULT_VIEWS)
	}
	vTxt := strings.Join(views, "、")
	if anchor := strings.TrimSpace(pstr(p, "consistency")); anchor != "" {
		vTxt += fmt.Sprintf("。%s完全一致", anchor)
	}
	blocks = append(blocks, block{"转面", fmt.Sprintf("转面图一行，共%s个视角", cnNum(len(views))), vTxt})

	// --- 03 表情
	exprs := splitList(p["expressions"])
	if len(exprs) == 0 {
		exprs = splitList(DEFAULT_EXPRESSIONS)
	}
	blocks = append(blocks, block{"表情", fmt.Sprintf("面部表情一行，共%s种", cnNum(len(exprs))), strings.Join(exprs, "、")})

	// --- 04 动作
	if acts := splitList(p["actions"]); len(acts) > 0 {
		blocks = append(blocks, block{"动作", fmt.Sprintf("动作姿势一行，共%s个", cnNum(len(acts))), strings.Join(acts, "、")})
	}

	// --- 05 细节
	if dets := splitList(p["details"]); len(dets) > 0 {
		blocks = append(blocks, block{"细节", fmt.Sprintf("细节特写，共%s处", cnNum(len(dets))), strings.Join(dets, "、")})
	}

	// --- 06 体型对比
	if h := strings.TrimSpace(pstr(p, "height")); h != "" {
		rh := strings.TrimSpace(pstr(p, "ref_height"))
		if rh == "" {
			rh = "180"
		}
		blocks = append(blocks, block{"体型", "体型对比",
			fmt.Sprintf("与一位 %s 厘米的成年男性并排站立，这位%s身高约 %s 厘米", rh, name, h)})
	}

	if mode == "lite" {
		keep := map[string]bool{"档案": true, "细节": true}
		var filtered []block
		for _, b := range blocks {
			if keep[b.key] {
				filtered = append(filtered, b)
			}
		}
		blocks = filtered
	}

	for i, b := range blocks {
		lines = append(lines, fmt.Sprintf("%02d %s：%s", i+1, b.title, period(b.content)))
	}

	// 配色条不占编号 —— 实测占编号会让模型把最后一个编号挪用给上一格
	palette := strings.TrimSpace(pstr(p, "palette"))
	extra := strings.TrimSpace(pstr(p, "extra"))
	colorID := strings.TrimSpace(pstr(p, "color_id"))
	var col *Color
	if colorID != "" && lib != nil {
		col = lib.Colors[colorID]
	}
	if extra != "" || palette != "" || col != nil {
		lines = append(lines, "")
		if extra != "" {
			lines = append(lines, extra)
		}
		if col != nil {
			lines = append(lines, col.PromptZh)
		}
		if palette != "" {
			lines = append(lines, "配色条："+period(palette))
		}
	}

	lastNo := len(blocks)
	var nums []string
	for i := 1; i <= lastNo; i++ {
		nums = append(nums, fmt.Sprintf("%02d", i))
	}
	lines = append(lines, "")
	lines = append(lines,
		"排版要求：每个分格左上角只标注一个编号，按顺序出现 "+strings.Join(nums, "、")+"，编号不重复、不增加额外文字。")
	lines = append(lines, "角色形象在所有分格中必须完全一致。")
	if neg := strings.TrimSpace(pstr(p, "negatives")); neg != "" {
		lines = append(lines, neg)
	} else {
		lines = append(lines, "不要红色印章、不要角落签章、不要水印、不要乱码文字、不要文字堆叠。")
	}

	return strings.Join(lines, "\n")
}

// GRAPHIC_TEXT_SUFFIX 图文卡收尾指令（与上游 prompt_style.py 同一份文案）
const GRAPHIC_TEXT_SUFFIX = ("【如果主题直白包含画面元素那就按主题出图，文案由你来升华，但是不要直接描述画面。 " +
	"如果主题比较概念化，那么文案和主题尽量保持一致，如果文案较长由你提炼，" +
	"由你先设计画面隐喻（人类和非人类都行）再出图   。    文字参与构图，图文一体】")

// buildCardPrompt 图文卡：版式 + 配色 + 主题 + 风格
func buildCardPrompt(p map[string]any, style *Style, lib *StyleLib) string {
	theme := strings.TrimSpace(pstr(p, "card_theme"))
	var lay *Layout
	var col *Color
	if lib != nil {
		lay = lib.Layouts[strings.TrimSpace(pstr(p, "layout_id"))]
		col = lib.Colors[strings.TrimSpace(pstr(p, "color_id"))]
	}

	var lines []string
	if lay != nil {
		lines = append(lines, fmt.Sprintf("图型：%s。", lay.Name))
	}
	if col != nil {
		lines = append(lines, col.PromptZh)
	}
	lines = append(lines, "主题："+period(theme))
	if lay != nil && lay.Zh != "" {
		lines = append(lines, "排版要求："+period(lay.Zh))
	}
	if style != nil {
		gen := strings.TrimSpace(style.GenerationName)
		lines = append(lines, fmt.Sprintf("风格名称：%s。参考作者/风格名称：%s。",
			gen, strings.TrimSpace(style.Reference)))
		feats := strings.TrimSpace(pstr(p, "style_feature"))
		if feats == "" {
			feats = style.PosTraits
		}
		if feats != "" {
			lines = append(lines, "核心风格特征："+period(feats))
		}
	}
	lines = append(lines, GRAPHIC_TEXT_SUFFIX)
	return strings.Join(lines, "\n")
}

// sc021FixedLayoutLine buildCardPrompt 给 SC-021 拼出的固定「排版要求」整行。
// 生成时若文本模型可用，runJob 会把这一整行替换成动态解析出的单义执行提示词。
func sc021FixedLayoutLine() string {
	if gLib == nil {
		return ""
	}
	lay := gLib.Layouts["SC-021"]
	if lay == nil || lay.Zh == "" {
		return ""
	}
	return "排版要求：" + period(lay.Zh)
}

// buildFreePrompt 自由出图：用户写什么就是什么，只补风格段 + 通用防乱码负向尾巴
func buildFreePrompt(p map[string]any, style *Style) string {
	var lines []string
	lines = append(lines, strings.TrimSpace(pstr(p, "free_text")))
	if style != nil {
		gen := strings.TrimSpace(style.GenerationName)
		lines = append(lines, fmt.Sprintf("风格：%s。参考作者/风格名称：%s。",
			gen, strings.TrimSpace(style.Reference)))
		feats := strings.TrimSpace(pstr(p, "style_feature"))
		if feats == "" {
			feats = style.PosTraits
		}
		if feats != "" {
			lines = append(lines, "核心风格特征："+period(feats))
		}
	}
	lines = append(lines, "不要红色印章、不要角落签章、不要水印、不要乱码文字、不要文字堆叠。")
	return strings.Join(lines, "\n")
}

// safeName 出图文件名净化：re.sub(r'[\\/:*?"<>|\s]+', "_", s)[:60]
var unsafeRe = regexp.MustCompile(`[\\/:*?"<>|\s]+`)

func safeName(s string) string {
	s = unsafeRe.ReplaceAllString(s, "_")
	r := []rune(s)
	if len(r) > 60 {
		r = r[:60]
	}
	return string(r)
}
