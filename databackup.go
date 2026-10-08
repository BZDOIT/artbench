package main

// data 目录启动自动备份：history.db / settings.json / sc021_state.json / characters.json
// 滚动保留最近 5 份（数据丢失可救，重装系统前也方便整目录拷走）

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const autoBackupKeep = 5

func autoBackupData() {
	srcs := []string{"history.db", "settings.json", "sc021_state.json", "characters.json"}
	var present []string
	for _, f := range srcs {
		if fileExists(filepath.Join(dataDir(), f)) {
			present = append(present, f)
		}
	}
	if len(present) == 0 {
		return
	}
	dstDir := filepath.Join(dataDir(), "auto_backup", time.Now().Format("20060102_150405"))
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return
	}
	n := 0
	for _, f := range present {
		if err := copyFileSync(filepath.Join(dataDir(), f), filepath.Join(dstDir, f)); err == nil {
			n++
		}
	}
	if n > 0 {
		logf("数据自动备份：%d 个文件 → %s", n, dstDir)
		pruneAutoBackups()
	}
}

func copyFileSync(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// pruneAutoBackups 只留最近 N 份（目录名时间戳，字典序即时间序）
func pruneAutoBackups() {
	root := filepath.Join(dataDir(), "auto_backup")
	ents, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var dirs []string
	for _, e := range ents {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(root, e.Name()))
		}
	}
	sort.Strings(dirs)
	for i := 0; i < len(dirs)-autoBackupKeep; i++ {
		_ = os.RemoveAll(dirs[i])
	}
}
