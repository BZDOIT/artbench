package main

// 风格库：styles.json / colors.json / layouts.json + 参考图索引
// 迁移自 Python app.py 的 positive_traits / StyleLib

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var NEG_MARKERS = []string{"避免", "不要", "不准", "不能", "禁止", "别"}

type Style struct {
	Number         string `json:"no"`
	Group          string `json:"group"`
	Reference      string `json:"ref"`
	GenerationName string `json:"gen"`
	Traits         string `json:"traits"`
	PosTraits      string `json:"pos"`
	Trap           bool   `json:"trap"`
	HasImg         bool   `json:"img"`
	Sheet          string `json:"sheet"`
}

type Color struct {
	ID       string `json:"id"`
	Cat      string `json:"cat"`
	CatZh    string `json:"cat_zh"`
	Name     string `json:"name"`
	NameEn   string `json:"name_en"`
	Quote    string `json:"quote"`
	PromptZh string `json:"prompt_zh"`
	PromptEn string `json:"prompt_en"`
	Img      string `json:"img"`
}

type Layout struct {
	ID       string   `json:"id"`
	Cat      string   `json:"cat"`
	Name     string   `json:"name"`
	NameEn   string   `json:"name_en"`
	Keywords []string `json:"keywords"`
	Zh       string   `json:"zh"`
	En       string   `json:"en"`
	Img      string   `json:"img"`
}

type StyleLib struct {
	Dir         string
	Styles      []*Style
	ByNo        map[string]*Style
	ImgMap      map[string]string // 编号(3位) -> 单图路径
	SheetMap    map[string]string // 文件名 -> 拼图总览路径
	SheetNames  []string          // 排好序的拼图文件名
	Colors      map[string]*Color
	Layouts     map[string]*Layout
	Groups      []string
	stylesJSON  string
	ColorCount  int
	LayoutCount int
}

func padNo(s string) string {
	s = strings.TrimSpace(s)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

func containsAny(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func splitAny(s string, seps string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return strings.ContainsRune(seps, r)
	})
}

// positiveTraits 取 traits 里的正向子句。
// 两级粒度：先按 ；。切子句；子句里若含否定词，再按 ，切小段，
// 只丢被否定的那一小段，保住同一子句里其余正向描述。
func positiveTraits(traits string) string {
	if traits == "" {
		return ""
	}
	var clauses []string
	for _, seg := range splitAny(traits, "；;。\n") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if !containsAny(seg, NEG_MARKERS) {
			clauses = append(clauses, seg)
			continue
		}
		if utf8.RuneCountInString(seg) <= 15 { // 整条就是个否定句 → 丢
			continue
		}
		var subs []string
		for _, s := range splitAny(seg, "，,") {
			s = strings.TrimSpace(s)
			if s != "" && !containsAny(s, NEG_MARKERS) {
				subs = append(subs, s)
			}
		}
		if len(subs) > 0 {
			clauses = append(clauses, strings.Join(subs, "，"))
		}
	}
	return strings.Join(clauses, "；")
}

var imgExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// imgStemKey 单图文件名 → 编号 key（上游 1.8 起编号体系换了，两种格式并存）：
//   旧版：3 位纯数字（"18"/"018" → "018"，styles.json 里是 001-278）
//   新版：前缀组格式（"FA-001" → "FA-001"，FA-FH 八组 327 款，单图在 images/individual/FA/FA-001.webp）
var imgStemRe = regexp.MustCompile(`^[A-Za-z]{1,2}-\d{3}$`)

func imgStemKey(stem string) (string, bool) {
	if isDigits(stem) {
		if len(stem) == 0 || len(stem) > 3 {
			return "", false
		}
		return padNo(stem), true
	}
	if imgStemRe.MatchString(stem) {
		return strings.ToUpper(stem), true
	}
	return "", false
}

// normalizeStyleNo styles.json 的 number 字段规范化（"fa-001" 这类大小写差异兜底）
func normalizeStyleNo(no string) string {
	no = strings.TrimSpace(no)
	if no == "" {
		return ""
	}
	if imgStemRe.MatchString(no) {
		return strings.ToUpper(no)
	}
	return padNo(no)
}

var sheetRe = regexp.MustCompile(`^[A-Za-z]+_(\d{3})-(\d{3})`)

// loadStyleLib 加载风格库。styles.json 用 WalkDir 全树找（取字典序第一个）。
func loadStyleLib(dir string) *StyleLib {
	lib := &StyleLib{
		Dir:      dir,
		ByNo:     map[string]*Style{},
		ImgMap:   map[string]string{},
		SheetMap: map[string]string{},
		Colors:   map[string]*Color{},
		Layouts:  map[string]*Layout{},
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		logf("✗ 风格库目录不存在：%s", dir)
		return lib
	}

	// ---- styles.json ----
	var jsonCands []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == "styles.json" {
			jsonCands = append(jsonCands, p)
		}
		return nil
	})
	if len(jsonCands) == 0 {
		logf("✗ 在 %s 下找不到 styles.json", dir)
		return lib
	}
	sort.Strings(jsonCands)
	lib.stylesJSON = jsonCands[0]
	raw, err := os.ReadFile(lib.stylesJSON)
	if err != nil {
		logf("✗ styles.json 读取失败：%v", err)
		return lib
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		// 可能是 dict 形式
		var m map[string]map[string]any
		if err2 := json.Unmarshal(raw, &m); err2 != nil {
			logf("✗ styles.json 解析失败：%v", err)
			return lib
		}
		for _, v := range m {
			list = append(list, v)
		}
	}
	for _, s := range list {
		no := normalizeStyleNo(str(s, "number"))
		traits := str(s, "traits")
		st := &Style{
			Number:         no,
			Group:          str(s, "group"),
			Reference:      str(s, "reference"),
			GenerationName: str(s, "generation_name"),
			Traits:         traits,
			PosTraits:      positiveTraits(traits),
			Trap:           strings.TrimSpace(traits) == "",
		}
		lib.Styles = append(lib.Styles, st)
		lib.ByNo[no] = st
	}
	sort.Slice(lib.Styles, func(i, j int) bool { return lib.Styles[i].Number < lib.Styles[j].Number })

	// ---- 分组（保持 styles 顺序去重）----
	gseen := map[string]bool{}
	for _, s := range lib.Styles {
		g := s.Group
		if g == "" {
			g = "未分组"
		}
		if !gseen[g] {
			gseen[g] = true
			lib.Groups = append(lib.Groups, g)
		}
	}

	// ---- 单图（individual 目录：旧版文件名纯数字，新版 FA-001 前缀格式，可嵌前缀子目录）----
	var individualDirs []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == "individual" {
			individualDirs = append(individualDirs, p)
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(individualDirs)
	for _, d := range individualDirs {
		_ = filepath.WalkDir(d, func(p string, de os.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			stem := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			if imgExts[ext] {
				if no, ok := imgStemKey(stem); ok {
					if _, exists := lib.ImgMap[no]; !exists {
						lib.ImgMap[no] = p
					}
				}
			}
			return nil
		})
	}

	// ---- 拼图总览（images 目录顶层，文件名非纯数字）----
	var imageDirs []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == "images" && p != dir {
			imageDirs = append(imageDirs, p)
		}
		return nil
	})
	sort.Strings(imageDirs)
	for _, d := range imageDirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			if imgExts[ext] && !isDigits(stem) {
				if _, ok := lib.SheetMap[e.Name()]; !ok {
					lib.SheetMap[e.Name()] = filepath.Join(d, e.Name())
				}
			}
		}
	}
	for name := range lib.SheetMap {
		lib.SheetNames = append(lib.SheetNames, name)
	}
	sort.Strings(lib.SheetNames)

	// ---- 配色库 + 版式库 ----
	lib.loadExtras()

	for _, s := range lib.Styles {
		s.HasImg = true
		if _, ok := lib.ImgMap[s.Number]; !ok {
			s.HasImg = false
		}
		s.Sheet = lib.sheetFor(s.Number)
	}

	logf("风格库加载完成：%d 风格 / %d 单图 / %d 拼图 / %d 配色 / %d 版式 / 陷阱 %d",
		len(lib.Styles), len(lib.ImgMap), len(lib.SheetMap), len(lib.Colors), len(lib.Layouts), lib.TrapCount())
	return lib
}

func (l *StyleLib) TrapCount() int {
	n := 0
	for _, s := range l.Styles {
		if s.Trap {
			n++
		}
	}
	return n
}

func (l *StyleLib) sheetFor(no string) string {
	n, ok := atoi3(no)
	if !ok {
		return ""
	}
	for _, name := range l.SheetNames {
		if m := sheetRe.FindStringSubmatch(name); m != nil {
			lo := atoi(m[1])
			hi := atoi(m[2])
			if n >= lo && n <= hi {
				return name
			}
		}
	}
	return ""
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func atoi3(s string) (int, bool) {
	if len(s) != 3 {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	return atoi(s), true
}

// loadExtras 配色（C-01…）与版式（SC/IG/SB）。
// 图片路径不照抄 json 里的相对路径，改从 image 字段截 images/ 之后的部分，直接在技能根下找。
func (l *StyleLib) loadExtras() {
	if l.stylesJSON == "" {
		return
	}
	refDir := filepath.Dir(l.stylesJSON)

	if raw, err := os.ReadFile(filepath.Join(refDir, "colors.json")); err == nil {
		var cs []map[string]any
		if json.Unmarshal(raw, &cs) == nil {
			for _, c := range cs {
				cid := strings.TrimSpace(str(c, "id"))
				if cid == "" {
					continue
				}
				rel := afterImages(str(c, "image"))
				img := ""
				if rel != "" {
					fp := filepath.Join(l.Dir, "images", filepath.FromSlash(rel))
					if fileExists(fp) {
						img = fp
					}
				}
				pzh := strings.TrimSpace(str(c, "prompt_zh"))
				if pzh == "" {
					nm := str(c, "name_zh")
					if nm == "" {
						nm = cid
					}
					pzh = "主题色：" + nm + "。"
				}
				l.Colors[cid] = &Color{
					ID:       cid,
					Cat:      str(c, "category"),
					CatZh:    str(c, "category_zh"),
					Name:     str(c, "name_zh"),
					NameEn:   str(c, "name_en"),
					Quote:    str(c, "quote_zh"),
					PromptZh: pzh,
					PromptEn: strings.TrimSpace(str(c, "prompt_en")),
					Img:      img,
				}
			}
		}
	} else {
		logf("✗ colors.json 读取失败（不影响风格功能）：%v", err)
	}

	if raw, err := os.ReadFile(filepath.Join(refDir, "layouts.json")); err == nil {
		var ls []map[string]any
		if json.Unmarshal(raw, &ls) == nil {
			reZh := regexp.MustCompile(`(?s)<!--\s*zh\s*-->\s*(.+?)(?:<!--|$)`)
			reEn := regexp.MustCompile(`(?s)<!--\s*en\s*-->\s*(.+?)(?:<!--|$)`)
			for _, li := range ls {
				lid := strings.TrimSpace(str(li, "id"))
				if lid == "" {
					continue
				}
				zh, en := "", ""
				if pf := strings.TrimSpace(str(li, "prompt_file")); pf != "" {
					if praw, err := os.ReadFile(filepath.Join(refDir, filepath.FromSlash(pf))); err == nil {
						if m := reZh.FindStringSubmatch(string(praw)); m != nil {
							zh = strings.TrimSpace(m[1])
						}
						if m := reEn.FindStringSubmatch(string(praw)); m != nil {
							en = strings.TrimSpace(m[1])
						}
					}
				}
				rel := afterImages(str(li, "image"))
				img := ""
				if rel != "" {
					fp := filepath.Join(l.Dir, "images", filepath.FromSlash(rel))
					if fileExists(fp) {
						img = fp
					}
				}
				var kws []string
				if kw, ok := li["keywords"].([]any); ok {
					for _, k := range kw {
						if s, ok := k.(string); ok && s != "" {
							kws = append(kws, s)
						}
					}
				}
				l.Layouts[lid] = &Layout{
					ID:       lid,
					Cat:      str(li, "category"),
					Name:     str(li, "name"),
					NameEn:   str(li, "name_en"),
					Keywords: kws,
					Zh:       zh,
					En:       en,
					Img:      img,
				}
			}
		}
	} else {
		logf("✗ layouts.json 读取失败（不影响风格功能）：%v", err)
	}
	l.ColorCount = len(l.Colors)
	l.LayoutCount = len(l.Layouts)
}

// afterImages 取 "images/" 之后的部分（截最后一段）
func afterImages(p string) string {
	if i := strings.LastIndex(p, "images/"); i >= 0 {
		return p[i+len("images/"):]
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// str 从 map[string]any 安全取字符串
func str(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	case float64: // JSON 数字（前端偶尔传裸数字编号）
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case int:
		return fmt.Sprintf("%d", v)
	}
	return ""
}
