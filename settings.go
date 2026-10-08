package main

// 设置面板后端：key 管理 + 模型/接口地址自由切换（OpenAI 兼容接口通用）
// 存 data\settings.json；首次运行从 .env 导入 key，之后以 settings.json 为准。
// base_url + model + key 三件套 = 任意 OpenAI 兼容生图服务（Agnes / 硅基流动 / OpenAI / 各类中转）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Settings struct {
	Keys         []string `json:"keys"`
	BaseURL      string   `json:"base_url"`
	Model        string   `json:"model"`
	Size         string   `json:"size"`  // 档位 1K/2K/3K/4K 或精确 1024x768
	Ratio        string   `json:"ratio"` // 档位尺寸配合的比例（1:1/3:4/4:3/16:9/9:16/2:3/3:2/21:9）
	Timeout      int      `json:"timeout"`
	Retries      int      `json:"retries"`
	DefaultCount int      `json:"default_count"`
	UpdateURL    string   `json:"update_url"` // 自更新源（托管 latest.json 的地址）
	// 视频通道（M3 启用）
	VideoBaseURL string `json:"video_base_url"`
	VideoModel   string `json:"video_model"`
	// 文本/推理通道（AI 推荐 + SC-021 动态解析；留空 = 不启用）
	TextModel string `json:"text_model"`
}

var (
	settingsMu   sync.Mutex
	gSettings    *Settings
	settingsFile string
)

func settingsPath() string { return filepath.Join(dataDir(), "settings.json") }

// loadSettings 读 settings.json；没有就从 .env + config.ini 播种一份
func loadSettings() *Settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	settingsFile = settingsPath()

	if raw, err := os.ReadFile(settingsFile); err == nil {
		var s Settings
		if json.Unmarshal(raw, &s) == nil && len(s.Keys) > 0 {
			gSettings = &s
			return gSettings
		}
	}

	// 播种：从 .env 导入
	env := loadEnv(gCfg.EnvFile)
	s := &Settings{
		Keys:         collectKeys(env),
		BaseURL:      env["AGNES_BASE_URL"],
		Model:        env["AGNES_IMAGE_MODEL"],
		Size:         "1K",
		Ratio:        "4:3",
		Timeout:      gCfg.Timeout,
		Retries:      gCfg.Retries,
		DefaultCount: gCfg.DefaultCount,
		UpdateURL:    "",
		VideoBaseURL: env["AGNES_BASE_URL"],
		VideoModel:   env["AGNES_VIDEO_MODEL"],
		TextModel:    env["AGNES_TEXT_MODEL"],
	}
	if s.BaseURL == "" {
		s.BaseURL = "https://apihub.agnes-ai.com/v1"
	}
	if s.Model == "" {
		s.Model = gCfg.Model
	}
	gSettings = s
	_ = s.saveLocked()
	return gSettings
}

func (s *Settings) saveLocked() error {
	_ = os.MkdirAll(filepath.Dir(settingsFile), 0o755)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsFile, b, 0o644)
}

// ktailSec 打码：只露尾 4 位
func ktailSec(k string) string {
	if len(k) <= 8 {
		return "****"
	}
	return "…" + k[len(k)-4:]
}

// settingsAddKey / settingsRemoveKey / settingsUpdate 带锁的三个变更操作
func settingsAddKey(k string) error {
	k = strings.TrimSpace(k)
	if k == "" {
		return fmt.Errorf("key 不能为空")
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()
	for _, old := range gSettings.Keys {
		if old == k {
			return fmt.Errorf("这把 key 已经在池子里了")
		}
	}
	gSettings.Keys = append(gSettings.Keys, k)
	return gSettings.saveLocked()
}

func settingsRemoveKey(idx int) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if idx < 0 || idx >= len(gSettings.Keys) {
		return fmt.Errorf("序号不存在")
	}
	gSettings.Keys = append(gSettings.Keys[:idx], gSettings.Keys[idx+1:]...)
	return gSettings.saveLocked()
}

// settingsUpdate 更新模型 / 地址 / 参数（keys 不在这里动，走 Add/Remove）
func settingsUpdate(body map[string]any) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	s := gSettings
	if v := strings.TrimSpace(str(body, "base_url")); v != "" {
		s.BaseURL = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(str(body, "model")); v != "" {
		s.Model = v
	}
	if v := strings.TrimSpace(str(body, "size")); v != "" {
		s.Size = v
	}
	if v := strings.TrimSpace(str(body, "ratio")); v != "" {
		s.Ratio = v
	}
	if v := strings.TrimSpace(str(body, "update_url")); v != "" || bodyHas(body, "update_url") {
		s.UpdateURL = strings.TrimRight(v, "/")
	}
	if _, ok := body["timeout"]; ok {
		if n := asInt(body["timeout"], s.Timeout); n >= 30 && n <= 3600 {
			s.Timeout = n
		}
	}
	if _, ok := body["retries"]; ok {
		if n := asInt(body["retries"], s.Retries); n >= 0 && n <= 10 {
			s.Retries = n
		}
	}
	if _, ok := body["default_count"]; ok {
		if n := asInt(body["default_count"], s.DefaultCount); n >= 1 && n <= 6 {
			s.DefaultCount = n
		}
	}
	if v := strings.TrimSpace(str(body, "video_base_url")); v != "" {
		s.VideoBaseURL = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(str(body, "video_model")); v != "" {
		s.VideoModel = v
	}
	// text_model 允许显式清空（bodyHas 而不是非空判断）
	if bodyHas(body, "text_model") {
		s.TextModel = strings.TrimSpace(str(body, "text_model"))
	}
	return gSettings.saveLocked()
}

// snapshotSettings 取一份快照（读安全）
func snapshotSettings() Settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return *gSettings
}

func bodyHas(body map[string]any, key string) bool {
	_, ok := body[key]
	return ok
}

// buildAgnes 按当前设置构建客户端（设置变更即重建，不用重启）
func buildAgnes(s *Settings) *AgnesClient {
	return &AgnesClient{
		Keys:    s.Keys,
		Base:    s.BaseURL,
		Model:   s.Model,
		Timeout: s.Timeout,
		Retries: s.Retries,
		DefSize: s.Size,
		OK:      len(s.Keys) > 0,
		sems:    map[string]chan struct{}{},
		BuiltAt: time.Now(),
	}
}

// swapAgnes 原子换客户端
var agnesMu sync.RWMutex

func getAgnes() *AgnesClient {
	agnesMu.RLock()
	defer agnesMu.RUnlock()
	return gAgnes
}

func setAgnes(c *AgnesClient) {
	agnesMu.Lock()
	gAgnes = c
	agnesMu.Unlock()
}
