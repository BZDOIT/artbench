package main

// Go↔Python 拼词一致性对照测试：读 testdata.json（Python 版生成的基准），逐条比对。

import (
	"encoding/json"
	"os"
	"testing"
)

func loadTestdata(t *testing.T) (expected map[string]any, p1, p2, freeP map[string]any) {
	t.Helper()
	raw, err := os.ReadFile("testdata.json")
	if err != nil {
		t.Skip("testdata.json 不存在，先跑 _gen_expected.py")
	}
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("testdata.json 解析失败：%v", err)
	}
	return all["expected"].(map[string]any),
		all["p1"].(map[string]any),
		all["p2"].(map[string]any),
		all["free_p"].(map[string]any)
}

func initLibForTest(t *testing.T) *StyleLib {
	t.Helper()
	lib := loadStyleLib(`D:\skills\handdraw-style-prompter`)
	if len(lib.Styles) == 0 {
		t.Skip("风格库不可用")
	}
	return lib
}

func eqStr(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		// 找出第一个差异点，方便定位
		gr := []rune(got)
		wr := []rune(want)
		i := 0
		for i < len(gr) && i < len(wr) && gr[i] == wr[i] {
			i++
		}
		lo := i - 20
		if lo < 0 {
			lo = 0
		}
		hi := i + 20
		t.Errorf("%s 不一致（第 %d 个字符处开始分歧）\n  got : %q\n  want: %q",
			name, i,
			string(gr[lo:min(hi, len(gr))]),
			string(wr[lo:min(hi, len(wr))]))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestPromptParity(t *testing.T) {
	expected, p1, p2, freeP := loadTestdata(t)
	lib := initLibForTest(t)

	// 风格库 1.8+ 换成 FA-001 前缀编号体系（327 款），旧纯数字编号（#192/#042…）和
	// testdata.json 基准都基于 1.2.x 体系 —— 新体系下跳过逐字比对，拼词语义由其它用例覆盖。
	if lib.ByNo["192"] == nil || lib.ByNo["042"] == nil {
		t.Skip("风格库已切到新前缀编号体系（1.8+），旧编号拼词基准不适用")
	}

	// 1. 设定表完整版 #192
	st := lib.ByNo["192"]
	if st == nil {
		t.Fatal("风格 #192 不存在")
	}
	eqStr(t, "sheet_192", buildPrompt(p1, st, lib), expected["sheet_192"].(string))

	// 2. 设定表精简版 #042
	st42 := lib.ByNo["042"]
	if st42 == nil {
		t.Fatal("风格 #042 不存在")
	}
	lite := map[string]any{}
	for k, v := range p1 {
		lite[k] = v
	}
	lite["mode"] = "lite"
	lite["height"] = ""
	eqStr(t, "sheet_lite", buildPrompt(lite, st42, lib), expected["sheet_lite"].(string))

	// 3. 图文卡 #192 + SC-01 + C-03
	eqStr(t, "card", buildCardPrompt(p2, st, lib), expected["card"].(string))

	// 4. 自由出图 #042
	eqStr(t, "free", buildFreePrompt(freeP, st42), expected["free"].(string))

	// 5. 陷阱检测：263 必须是陷阱（1.2.23 校准后 6 个之一）
	st263 := lib.ByNo["263"]
	if st263 == nil || !st263.Trap {
		t.Errorf("#263 应为陷阱编号（traits 为空），got %+v", st263)
	}
	// 6. 非陷阱：#201（1.2.23 已解禁）
	st201 := lib.ByNo["201"]
	if st201 == nil || st201.Trap {
		t.Errorf("#201 校准后应非陷阱")
	}

	// 7. 结构性断言：库会热更新（278 → 279 → 327 …），规模只设下限，不写死快照
	if len(lib.Styles) < 200 {
		t.Errorf("风格数异常偏少（%d），疑似加载坏了", len(lib.Styles))
	}
	if lib.ColorCount < 30 || lib.LayoutCount < 100 {
		t.Errorf("配色/版式异常偏少：%d / %d", lib.ColorCount, lib.LayoutCount)
	}
	if got := lib.TrapCount(); got < 1 {
		t.Errorf("陷阱数应为正（traits 为空的编号），got %d", got)
	}
}

func TestPositiveTraits(t *testing.T) {
	// 期望值全部来自 Python 版实测（语义：短否定子句整条丢；长子句按 ， 切小段只丢被否定的小段）
	cases := []struct{ in, want string }{
		{"线条流畅，不要阴影；色彩明亮", "色彩明亮"},                      // 含否定的子句 ≤15 字 → 整条丢
		{"线条流畅，背景不要阴影不要杂物，色彩明亮；构图饱满", "线条流畅，色彩明亮；构图饱满"}, // 长子句只丢否定小段
		{"不要高光", ""}, // 整条否定且短 → 丢
		{"", ""},
	}
	for _, c := range cases {
		if got := positiveTraits(c.in); got != c.want {
			t.Errorf("positiveTraits(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCnNum(t *testing.T) {
	cases := map[int]string{
		1: "一", 5: "五", 9: "九", 10: "十", 11: "十一", 15: "十五",
		20: "二十", 21: "二十一", 32: "三十二", 0: "0",
	}
	for n, want := range cases {
		if got := cnNum(n); got != want {
			t.Errorf("cnNum(%d) = %q, want %q", n, got, want)
		}
	}
}
