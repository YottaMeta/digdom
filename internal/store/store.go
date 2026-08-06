// Package store 用 modernc.org/sqlite（纯 Go 零 cgo）持久化扫描历史、复核结论与资产 diff。
// 引擎层与 GUI 解耦：本包只依赖 database/sql 与 model，供 Wails 绑定层与 CLI 复用同一套存储。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"digdom/internal/model"
)

// Store 封装 SQLite 连接与表结构。
type Store struct {
	db *sql.DB
}

// DefaultDBPath 返回数据库路径：优先程序（exe）所在目录，便携使用；
// 该目录不可写时回退到用户配置目录。
func DefaultDBPath() string {
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if dirWritable(exeDir) {
			return filepath.Join(exeDir, "digdom.db")
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "digdom.db"
	}
	return filepath.Join(dir, "digdom", "digdom.db")
}

// dirWritable 探测目录是否可写（创建并删除探针文件）。
func dirWritable(dir string) bool {
	probe := filepath.Join(dir, ".digdom-wprobe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
}

// legacyDefaultPath 旧版（v0.1.x）数据库位置 %AppData%\digdom\digdom.db。
func legacyDefaultPath() string {
	dir, _ := os.UserConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "digdom", "digdom.db")
}

// Open 打开（必要时创建）数据库并迁移表结构。
func Open(path string) (*Store, error) {
	if path == "" {
		path = DefaultDBPath()
		adoptLegacy(path)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("store: 创建目录失败: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: 打开失败: %w", err)
	}
	// 单写多读 + 短忙等待，避免 SQLITE_BUSY。
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭连接。
func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS scans (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	target      TEXT    NOT NULL,
	params      TEXT    NOT NULL DEFAULT '{}',
	started_at  INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	queried     INTEGER NOT NULL DEFAULT 0,
	hits        INTEGER NOT NULL DEFAULT 0,
	wildcards   INTEGER NOT NULL DEFAULT 0,
	unreviewed  INTEGER NOT NULL DEFAULT 0,
	status      TEXT    NOT NULL DEFAULT 'done',
	error       TEXT    NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS results (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	scan_id      INTEGER NOT NULL REFERENCES scans(id) ON DELETE CASCADE,
	name         TEXT    NOT NULL,
	base         TEXT    NOT NULL DEFAULT '',
	depth        INTEGER NOT NULL DEFAULT 0,
	tag          TEXT    NOT NULL DEFAULT 'unreviewed',
	ips          TEXT    NOT NULL DEFAULT '[]',
	cnames       TEXT    NOT NULL DEFAULT '[]',
	verdict      TEXT    NOT NULL DEFAULT '',
	note         TEXT    NOT NULL DEFAULT '',
	http_status  INTEGER NOT NULL DEFAULT 0,
	http_scheme  TEXT    NOT NULL DEFAULT '',
	http_ok      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_results_scan ON results(scan_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_results_scan_name ON results(scan_id, name);
CREATE INDEX IF NOT EXISTS idx_scans_target ON scans(target);
`

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("store: 迁移失败: %w", err)
	}
	// 幂等补列：老库用 ALTER TABLE 补齐 HTTP 探活字段。
	cols := s.tableCols("results")
	add := func(name, ddl string) error {
		for _, c := range cols {
			if c == name {
				return nil
			}
		}
		if _, err := s.db.Exec("ALTER TABLE results ADD COLUMN " + ddl); err != nil {
			return err
		}
		cols = s.tableCols("results")
		return nil
	}
	for _, def := range []string{
		"http_status INTEGER NOT NULL DEFAULT 0",
		"http_scheme TEXT NOT NULL DEFAULT ''",
		"http_ok INTEGER NOT NULL DEFAULT 0",
	} {
		name := strings.SplitN(def, " ", 2)[0]
		if err := add(name, def); err != nil {
			return fmt.Errorf("store: 补列失败(%s): %w", name, err)
		}
	}
	return nil
}

// tableCols 返回表的所有列名。
func (s *Store) tableCols(table string) []string {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err == nil {
			out = append(out, name)
		}
	}
	return out
}

// listJSON 把 Base 设为 web 渲染基准（命中后递归的父层级 + 名字）。这里只做工具函数。
func listJSON(in []string) string {
	if in == nil {
		return "[]"
	}
	b, _ := json.Marshal(in)
	return string(b)
}

// adoptLegacy 迁移旧版库：若新默认路径无库而 %AppData%\digdom\digdom.db 存在，
// 复制过去并删除旧文件，避免便携化后历史"消失"。
func adoptLegacy(newPath string) {
	old := legacyDefaultPath()
	if old == "" || old == newPath {
		return
	}
	if fileExists(newPath) || !fileExists(old) {
		return
	}
	if err := copyFile(old, newPath); err == nil {
		_ = os.Remove(old)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

// parseList 解析 JSON 数组列，容错为空返回空切片（永不为 nil）。
func parseList(s string) []string {
	var out []string
	if s == "" {
		return out
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return []string{}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// SaveScan 事务性保存一次扫描及其全部结果，返回 scan id。
func (s *Store) SaveScan(sc model.ScanSummary, results []model.Result) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: 开启事务失败: %w", err)
	}
	defer tx.Rollback()

	startedMS := sc.StartedAt
	res, err := tx.Exec(
		`INSERT INTO scans(target, params, started_at, duration_ms, queried, hits, wildcards, unreviewed, status, error)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		sc.Target, sc.Params, startedMS, sc.DurationMS, sc.Queried, sc.Hits, sc.Wildcards, sc.Unreviewed, sc.Status, sc.Error,
	)
	if err != nil {
		return 0, fmt.Errorf("store: 写入扫描记录失败: %w", err)
	}
	scanID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	stmt, err := tx.Prepare(
		`INSERT OR IGNORE INTO results(scan_id, name, base, depth, tag, ips, cnames, verdict, note)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
	)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, r := range results {
		if _, err := stmt.Exec(scanID, r.Name, r.Base, r.Depth, string(r.Tag), listJSON(r.IPs), listJSON(r.CNAMEs), "", ""); err != nil {
			return 0, fmt.Errorf("store: 写入结果失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return scanID, nil
}

// ListScans 返回历史扫描列表（新→旧）。
func (s *Store) ListScans() ([]model.ScanSummary, error) {
	rows, err := s.db.Query(
		`SELECT id, target, params, started_at, duration_ms, queried, hits, wildcards, unreviewed, status, error
		 FROM scans ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.ScanSummary{}
	for rows.Next() {
		var sc model.ScanSummary
		var startedMS int64
		if err := rows.Scan(&sc.ID, &sc.Target, &sc.Params, &startedMS, &sc.DurationMS,
			&sc.Queried, &sc.Hits, &sc.Wildcards, &sc.Unreviewed, &sc.Status, &sc.Error); err != nil {
			return nil, err
		}
		sc.StartedAt = startedMS
		out = append(out, sc)
	}
	return out, rows.Err()
}

// LoadResults 返回某次扫描的全部结果（含复核与 HTTP 探测字段）。
func (s *Store) LoadResults(scanID int64) ([]model.ResultRow, error) {
	rows, err := s.db.Query(
		`SELECT id, name, base, depth, tag, ips, cnames, verdict, note, http_status, http_scheme, http_ok
		 FROM results WHERE scan_id=? ORDER BY depth ASC, name ASC`, scanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.ResultRow{}
	for rows.Next() {
		var r model.ResultRow
		var tag, verdict, ipsRaw, cnamesRaw, scheme, note string
		var httpOK int
		if err := rows.Scan(&r.ID, &r.Name, &r.Base, &r.Depth, &tag, &ipsRaw, &cnamesRaw, &verdict, &note,
			&r.HTTPStatus, &scheme, &httpOK); err != nil {
			return nil, err
		}
		r.Tag = model.Tag(tag)
		r.Verdict = model.ReviewVerdict(verdict)
		r.IPs = parseList(ipsRaw)
		r.CNAMEs = parseList(cnamesRaw)
		r.Note = note
		r.HTTPScheme = scheme
		r.HTTPOK = httpOK != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateReview 修改一条结果的复核结论与备注。
func (s *Store) UpdateReview(scanID, resultID int64, verdict model.ReviewVerdict, note string) error {
	_, err := s.db.Exec(
		`UPDATE results SET verdict=?, note=? WHERE id=? AND scan_id=?`,
		string(verdict), note, resultID, scanID)
	return err
}

// UpdateHTTP 写入一条结果的 HTTP 探活结果。scheme 传 "fail" 表示已探但不可达；"" 表示未探。
func (s *Store) UpdateHTTP(scanID, resultID int64, status int, scheme string, ok bool) error {
	okI := 0
	if ok {
		okI = 1
	}
	_, err := s.db.Exec(
		`UPDATE results SET http_status=?, http_scheme=?, http_ok=? WHERE id=? AND scan_id=?`,
		status, scheme, okI, resultID, scanID)
	return err
}

// DeleteScan 删除一次历史扫描及其全部结果（级联）。
func (s *Store) DeleteScan(scanID int64) error {
	_, err := s.db.Exec(`DELETE FROM scans WHERE id=?`, scanID)
	return err
}

// DeleteResult 删除一条结果记录。
func (s *Store) DeleteResult(scanID, resultID int64) error {
	_, err := s.db.Exec(`DELETE FROM results WHERE id=? AND scan_id=?`, resultID, scanID)
	return err
}

// DeleteResults 按 resultID 批量删除同一扫描下的多条记录。
func (s *Store) DeleteResults(scanID int64, resultIDs []int64) error {
	if len(resultIDs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`DELETE FROM results WHERE id=? AND scan_id=?`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, id := range resultIDs {
		if _, err := stmt.Exec(id, scanID); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// DiffScans 对比两次扫描的资产差异。a 为基准（旧），b 为当前（新）。
func (s *Store) DiffScans(aID, bID int64) (*model.DiffResult, error) {
	aRows, err := s.LoadResults(aID)
	if err != nil {
		return nil, err
	}
	bRows, err := s.LoadResults(bID)
	if err != nil {
		return nil, err
	}
	byName := func(rows []model.ResultRow) map[string]model.ResultRow {
		m := make(map[string]model.ResultRow, len(rows))
		for _, r := range rows {
			m[r.Name] = r
		}
		return m
	}
	a := byName(aRows)
	b := byName(bRows)

	out := &model.DiffResult{Added: []model.DiffItem{}, Removed: []model.DiffItem{}}
	for name, rb := range b {
		if _, ok := a[name]; !ok {
			out.Added = append(out.Added, model.DiffItem{Name: name, State: "added", Tag: rb.Tag, IPs: rb.IPs, Verdict: rb.Verdict, ScanID: bID})
		}
	}
	for name, ra := range a {
		if _, ok := b[name]; !ok {
			out.Removed = append(out.Removed, model.DiffItem{Name: name, State: "removed", Tag: ra.Tag, IPs: ra.IPs, Verdict: ra.Verdict, ScanID: aID})
		}
	}
	return out, nil
}