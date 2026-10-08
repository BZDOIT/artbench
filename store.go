package main

// SQLite 历史 + 角色档案库（json）
// 迁移自 Python app.py 的 db_conn / save_history / list_history / delete_history / load_characters / save_character / delete_character
// 用 modernc.org/sqlite（纯 Go，保单文件 exe）

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	dbMu      sync.Mutex
	dbPath    string
	dbInited  bool
	charsMu   sync.Mutex
	charsPath string
)

func openDB() (*sql.DB, error) {
	_ = os.MkdirAll(filepath.Dir(dbPath), 0o755)
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1) // sqlite 单写
	if !dbInited {
		for _, ddl := range []string{
			`CREATE TABLE IF NOT EXISTS history(
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				created TEXT, style_no TEXT, style_name TEXT, char_name TEXT,
				status TEXT, prompt TEXT, files TEXT, urls TEXT, elapsed REAL, error TEXT)`,
			`CREATE TABLE IF NOT EXISTS films(
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				title TEXT, mode TEXT, seconds INTEGER, aspect TEXT, movie TEXT,
				created TEXT, updated TEXT)`,
			`CREATE TABLE IF NOT EXISTS shots(
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				film_id INTEGER, seq INTEGER, img TEXT, img2 TEXT,
				action TEXT, camera TEXT, seconds INTEGER, prompt TEXT,
				status TEXT, video_id TEXT, result TEXT, error TEXT,
				elapsed REAL, aspect TEXT, updated TEXT)`,
			`CREATE TABLE IF NOT EXISTS clips(
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				title TEXT, prompt TEXT, mode TEXT, seconds INTEGER, aspect TEXT, refs TEXT,
				status TEXT, video_id TEXT, result TEXT, error TEXT, elapsed REAL,
				created TEXT, updated TEXT)`,
		} {
			if _, err := conn.Exec(ddl); err != nil {
				conn.Close()
				return nil, err
			}
		}
		// 迁移：history 加 mode 列（区分 sheet/card 等来源；旧行为空串）
		_, _ = conn.Exec("ALTER TABLE history ADD COLUMN mode TEXT DEFAULT ''")
		dbInited = true
	}
	return conn, nil
}

func saveHistory(job *Job) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	conn, err := openDB()
	if err != nil {
		return err
	}
	defer conn.Close()

	job.mu.Lock()
	filesJSON, _ := json.Marshal(job.Files)
	urlsJSON, _ := json.Marshal(job.URLs)
	_, err = conn.Exec(
		"INSERT INTO history(created,mode,style_no,style_name,char_name,status,prompt,files,urls,elapsed,error) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
		job.CreatedStr,
		str(job.Meta, "mode"),
		str(job.Meta, "style_no"),
		str(job.Meta, "style_name"),
		str(job.Meta, "char_name"),
		job.Status,
		str(job.Meta, "prompt"),
		string(filesJSON),
		string(urlsJSON),
		job.Elapsed,
		job.Error,
	)
	job.mu.Unlock()
	return err
}

func listHistory(limit int) []map[string]any {
	dbMu.Lock()
	defer dbMu.Unlock()
	if limit < 1 {
		limit = 60
	}
	conn, err := openDB()
	if err != nil {
		return []map[string]any{}
	}
	defer conn.Close()
	rows, err := conn.Query("SELECT id,created,mode,style_no,style_name,char_name,status,prompt,files,urls,elapsed,error FROM history ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int
		var created, mode, styleNo, styleName, charName, status, prompt, filesS, urlsS, errorS string
		var elapsed sql.NullFloat64
		if err := rows.Scan(&id, &created, &mode, &styleNo, &styleName, &charName, &status, &prompt, &filesS, &urlsS, &elapsed, &errorS); err != nil {
			continue
		}
		var files, urls []any
		_ = json.Unmarshal([]byte(filesS), &files)
		_ = json.Unmarshal([]byte(urlsS), &urls)
		if files == nil {
			files = []any{}
		}
		if urls == nil {
			urls = []any{}
		}
		out = append(out, map[string]any{
			"id": id, "created": created, "mode": mode, "style_no": styleNo, "style_name": styleName,
			"char_name": charName, "status": status, "prompt": prompt,
			"files": files, "urls": urls, "elapsed": elapsed.Float64, "error": errorS,
		})
	}
	return out
}

func deleteHistory(id int) {
	dbMu.Lock()
	defer dbMu.Unlock()
	conn, err := openDB()
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.Exec("DELETE FROM history WHERE id=?", id)
}

// ---- 角色档案库 ----

func loadCharacters() []map[string]any {
	charsMu.Lock()
	defer charsMu.Unlock()
	out := []map[string]any{}
	raw, err := os.ReadFile(charsPath)
	if err != nil {
		return out
	}
	var items []map[string]any
	if json.Unmarshal(raw, &items) != nil {
		return out
	}
	return items
}

func saveCharacter(data map[string]any) []map[string]any {
	charsMu.Lock()
	defer charsMu.Unlock()

	name := str(data, "char_name")
	items := []map[string]any{}
	if raw, err := os.ReadFile(charsPath); err == nil {
		_ = json.Unmarshal(raw, &items)
	}

	data["char_name"] = name
	data["updated"] = time.Now().Format("2006-01-02 15:04:05")
	replaced := false
	for i, it := range items {
		if str(it, "char_name") == name {
			items[i] = data
			replaced = true
			break
		}
	}
	if !replaced {
		items = append([]map[string]any{data}, items...)
	}
	if b, err := json.MarshalIndent(items, "", "  "); err == nil {
		_ = os.MkdirAll(filepath.Dir(charsPath), 0o755)
		_ = os.WriteFile(charsPath, b, 0o644)
	}
	return items
}

func deleteCharacter(name string) []map[string]any {
	charsMu.Lock()
	defer charsMu.Unlock()
	items := []map[string]any{}
	if raw, err := os.ReadFile(charsPath); err == nil {
		_ = json.Unmarshal(raw, &items)
	}
	var out []map[string]any
	for _, it := range items {
		if str(it, "char_name") != name {
			out = append(out, it)
		}
	}
	if b, err := json.MarshalIndent(out, "", "  "); err == nil {
		_ = os.MkdirAll(filepath.Dir(charsPath), 0o755)
		_ = os.WriteFile(charsPath, b, 0o644)
	}
	return out
}
