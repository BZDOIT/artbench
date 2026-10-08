package main

// 素材库：扫描 / 打开 / 定位 / 回收站删除（SHFileOperationW，可还原）
// 迁移自 Python app.py 的 scan_library / material_file / _sh_delete / recycle_files / apply_output_dir

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var imgExtSet = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

// scanLibrary 扫素材：出图（gOut 递归）+ 视频（mp4）+ 分镜关键帧
func scanLibrary(kind string, limit int) []map[string]any {
	if limit < 1 {
		limit = 300
	}
	limit = clamp(limit, 1, 1000)
	var items []map[string]any

	filepath.WalkDir(gOut, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		k := ""
		if imgExtSet[ext] {
			k = "img"
		} else if ext == ".mp4" {
			k = "video"
		}
		if k == "" || (kind == "img" || kind == "video") && k != kind {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(gOut, p)
		items = append(items, map[string]any{
			"kind": k, "name": d.Name(),
			"url":  "/out/" + urlEscape(filepath.ToSlash(rel)),
			"file": p, "size": st.Size(), "mtime": st.ModTime().Unix(),
		})
		return nil
	})

	shotDir := filepath.Join(dataDir(), "shot_imgs")
	if ents, err := os.ReadDir(shotDir); err == nil && kind != "video" {
		for _, e := range ents {
			if e.IsDir() || !imgExtSet[strings.ToLower(filepath.Ext(e.Name()))] {
				continue
			}
			p := filepath.Join(shotDir, e.Name())
			if st, err := e.Info(); err == nil {
				items = append(items, map[string]any{
					"kind": "img", "name": e.Name(),
					"url":  "/shots-img/" + urlEscape(e.Name()),
					"file": p, "size": st.Size(), "mtime": st.ModTime().Unix(),
				})
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return asInt(items[i]["mtime"], 0) > asInt(items[j]["mtime"], 0)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items
}

// materialFile open/reveal 安全校验：路径必须落在素材目录里且真实存在
func materialFile(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	for _, root := range []string{gOut, filepath.Join(dataDir(), "shot_imgs")} {
		rootAbs, _ := filepath.Abs(root)
		if strings.HasPrefix(strings.ToLower(abs), strings.ToLower(rootAbs)) && fileExists(abs) {
			return abs
		}
	}
	return ""
}

// ---- SHFileOperationW 回收站删除 ----

const (
	foDelete     = 3
	fofAllowUndo = 0x40
	fofNoConfirm = 0x10
	fofSilent    = 0x4
	fofNoErrorUI = 0x400
)

type shFileOpStructW struct {
	hwnd              uintptr
	wFunc             uint32
	pFrom             *uint16
	pTo               *uint16
	fFlags            uint16
	fAnyOpsAborted    int32
	hNameMappings     uintptr
	lpszProgressTitle *uint16
}

var (
	shell32       = syscall.NewLazyDLL("shell32.dll")
	procSHFileOpW = shell32.NewProc("SHFileOperationW")
)

// shDelete 删单个文件到回收站。返回码在部分环境不可靠，以「文件确实没了」为准。
// 注意：SHFileOperationW 的 pFrom 要求「路径 + 双 null 结尾」；
// syscall.UTF16PtrFromString 遇到字符串内部 \x00 会返回 EINVAL，
// 所以必须用 UTF16FromString（它自己补一个结尾 null）再手动 append 一个 0。
func shDelete(path string) bool {
	if path == "" {
		return false
	}
	u16, err := syscall.UTF16FromString(path)
	if err != nil {
		return false
	}
	u16 = append(u16, 0) // 双 null 结尾，SHFileOperationW 要求
	op := shFileOpStructW{
		wFunc:  foDelete,
		pFrom:  &u16[0],
		fFlags: fofAllowUndo | fofNoConfirm | fofSilent | fofNoErrorUI,
	}
	procSHFileOpW.Call(uintptr(unsafe.Pointer(&op)))
	return !fileExists(path)
}

var recycleMu sync.Mutex

// recycleFiles 批量删到回收站（白名单校验 + 上限 200）
func recycleFiles(paths []string) (int, []string) {
	recycleMu.Lock()
	defer recycleMu.Unlock()
	ok := 0
	var failed []string
	for i, p := range paths {
		if i >= 200 {
			failed = append(failed, fmt.Sprintf("第 %d 个之后没处理（单次上限 200）", i+1))
			break
		}
		fp := materialFile(p)
		if fp == "" {
			failed = append(failed, filepath.Base(p))
			continue
		}
		if shDelete(fp) {
			ok++
		} else {
			failed = append(failed, filepath.Base(fp))
		}
	}
	return ok, failed
}

// ---- 素材根 ----

var outMu sync.RWMutex

func setOutputDir(dir string) (map[string]any, error) {
	dir = strings.TrimSpace(dir)
	var od string
	if dir == "" {
		od = filepath.Join(APP_DIR, "out")
	} else {
		od = dir
		if st, err := os.Stat(od); err == nil && !st.IsDir() {
			return nil, fmt.Errorf("这个路径是个文件不是文件夹：%s", od)
		}
	}
	abs, err := filepath.Abs(od)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	outMu.Lock()
	gOut = abs
	outMu.Unlock()
	logf("素材根目录：%s", abs)
	go saveOutputDirIni(dir)
	return map[string]any{"out_dir": abs}, nil
}

// saveOutputDirIni 文本级替换 config.ini 的 output_dir 行，保住中文注释
func saveOutputDirIni(dir string) {
	cfgPath := filepath.Join(APP_DIR, "config.ini")
	line := "output_dir = " + dir
	var txt []byte
	var err error
	if txt, err = os.ReadFile(cfgPath); err != nil {
		_ = os.WriteFile(cfgPath, []byte("[paths]\n"+line+"\n"), 0o644)
		return
	}
	content := string(txt)
	lines := strings.Split(content, "\n")
	found := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "output_dir") && strings.Contains(l, "=") {
			lines[i] = line
			found = true
			break
		}
	}
	var out string
	if found {
		out = strings.Join(lines, "\n")
	} else {
		// 插到 [paths] 节末尾（下一个 [ 节或文件尾之前）
		out = content
		if i := strings.Index(content, "[paths]"); i >= 0 {
			rest := content[i:]
			if j := strings.Index(rest, "\n["); j >= 0 {
				out = content[:i+j+1] + line + "\n\n" + content[i+j+1:]
			} else {
				out = content + line + "\n"
			}
		} else {
			out = "[paths]\n" + line + "\n\n" + content
		}
	}
	_ = os.WriteFile(cfgPath, []byte(out), 0o644)
}

func getOut() string {
	outMu.RLock()
	defer outMu.RUnlock()
	return gOut
}

var _ = time.Now // 保留 time 引用
