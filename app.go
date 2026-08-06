package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"digdom/internal/brute"
	"digdom/internal/engine"
	"digdom/internal/httpcheck"
	"digdom/internal/model"
	"digdom/internal/store"
)

// defaultDictName 默认字典文件名（放在 exe 同目录，用户可直接编辑）。
const defaultDictName = "dic.txt"

// debugLogPath 后端调试日志（诊断"点了没反应"用，定位后移除）。
const debugLogPath = "digdom-debug.log"

// debugLog 追加一行后端日志到系统临时目录下的 digdom-debug.log。
func debugLog(format string, args ...any) {
	line := fmt.Sprintf("%s %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
	path := filepath.Join(os.TempDir(), debugLogPath)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// App 是 Wails 绑定层，负责把引擎暴露给前端并转发事件。
type App struct {
	ctx     context.Context
	mu      sync.Mutex
	scanner *engine.Scanner
	store   *store.Store
	stopped atomic.Bool
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// 存储初始化失败不致命：爆破照常，仅历史/复核不可用。
	if st, err := store.Open(""); err == nil {
		a.store = st
	} else {
		debugLog("历史库打开失败（不影响爆破）: %v", err)
	}
}

// defaultDictFile 返回 exe 同目录下的默认字典文件路径。
func defaultDictFile() string {
	exe, err := os.Executable()
	if err != nil {
		return defaultDictName
	}
	return filepath.Join(filepath.Dir(exe), defaultDictName)
}

// resolveDictWords 确定本次使用的字典与路径：
// dictPath 非空则加载该文件；否则用 exe 旁的 dic.txt，不存在时以内置字典生成一份落盘。
func (a *App) resolveDictWords(dictPath string) ([]string, string, error) {
	if dictPath != "" {
		words, err := engine.LoadDictFile(dictPath)
		if err != nil {
			return nil, dictPath, err
		}
		return words, dictPath, nil
	}
	f := defaultDictFile()
	if words, err := engine.LoadDictFile(f); err == nil {
		return words, f, nil
	}
	words := engine.DefaultDictWords()
	if len(words) == 0 {
		return nil, f, errors.New("内置字典为空")
	}
	if err := os.WriteFile(f, []byte(strings.Join(words, "\n")+"\n"), 0o644); err == nil {
		return words, f, nil
	}
	return words, f, nil
}

// parseDNSServers 解析用户输入的 DNS（逗号/空白分隔），空则用默认池。
func parseDNSServers(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		part = model.NormalizeName(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	out = model.DedupeStrings(out)
	if len(out) == 0 {
		return brute.DefaultServers
	}
	return out
}

// Version 返回构建版本号（前端展示用，便于识别当前构建）。
func (a *App) Version() string { return buildVersion }

// GetDictWords 返回指定字典的词数（空 path 表示默认字典）。
func (a *App) GetDictWords(dictPath string) []string {
	words, _, _ := a.resolveDictWords(dictPath)
	return words
}

// PickDict 弹出文件选择框，返回用户选中的字典路径；取消返回空串。
func (a *App) PickDict() (string, error) {
	f, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择字典文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "字典 (*.txt)", Pattern: "*.txt"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return "", err
	}
	return f, nil
}

// StartScan 启动一次爆破。target 为根域；customDict 为自定义追加词（可空）；
// maxDepth 递归深度；concurrency 并发；rps 限速（0 = 不限）；dns 服务器列表（可空）；
// dictPath 字典文件路径（可空 = 默认字典 dic.txt）。
func (a *App) StartScan(target string, customDict string, maxDepth, concurrency, rps int, dns string, dictPath string) error {
	debugLog("StartScan 进入 target=%q maxDepth=%d conc=%d rps=%d dns=%q dictPath=%q", target, maxDepth, concurrency, rps, dns, dictPath)
	target = model.NormalizeName(target)
	if target == "" {
		debugLog("StartScan 返回: 目标为空")
		return errors.New("目标子域不能为空")
	}
	if maxDepth < 0 {
		maxDepth = 0
	}
	if concurrency <= 0 {
		concurrency = 300
	}

	a.mu.Lock()
	if a.scanner != nil {
		a.mu.Unlock()
		return errors.New("已有扫描正在进行，请先停止")
	}
	a.mu.Unlock()

	words, _, err := a.resolveDictWords(dictPath)
	if err != nil {
		return err
	}
	words = append(words, engine.ParseDictText(customDict)...)
	words = model.DedupeStrings(words)

	cfg := model.Config{
		Root:        target,
		DictWords:   words,
		MaxDepth:    maxDepth,
		Concurrency: concurrency,
		RPS:         rps,
		Timeout:     3 * time.Second,
		Resolvers:   parseDNSServers(dns),
	}
	sc := engine.NewScanner(cfg)

	a.mu.Lock()
	a.scanner = sc
	a.stopped.Store(false)
	a.mu.Unlock()

	runtime.EventsEmit(a.ctx, "scan:started", map[string]any{
		"target": target,
		"words":  len(words),
		"depth":  maxDepth,
	})
	paramsJSON, _ := json.Marshal(map[string]any{
		"target": target, "depth": maxDepth, "concurrency": concurrency,
		"rps": rps, "dns": dns, "dict": dictPath, "words": len(words),
	})
	debugLog("StartScan 成功，启动扫描（词数=%d，DNS=%v）", len(words), words)
	go a.consume(sc, string(paramsJSON))
	return nil
}

// StopScan 停止当前扫描。
func (a *App) StopScan() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.scanner == nil {
		return errors.New("当前没有进行中的扫描")
	}
	a.stopped.Store(true)
	a.scanner.Stop()
	return nil
}

// consume 消费引擎通道：结果批量转发、进度直转、结束后保存历史并发 done。
func (a *App) consume(sc *engine.Scanner, paramsJSON string) {
	defer func() {
		a.mu.Lock()
		if a.scanner == sc {
			a.scanner = nil
		}
		a.mu.Unlock()
	}()

	all := make([]model.Result, 0, 1024)
	batch := make([]model.Result, 0, 300)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		out := make([]model.Result, len(batch))
		copy(out, batch)
		batch = batch[:0]
		runtime.EventsEmit(a.ctx, "scan:results", out)
		debugLog("consume 批量 flush %d 条", len(out))
	}

	stopTick := make(chan struct{})
	go func() {
		t := time.NewTicker(150 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stopTick:
				return
			case <-t.C:
				flush()
			}
		}
	}()

	statsCh := make(chan model.Stats, 1)
	go func() { statsCh <- sc.Run() }()

	resCh := sc.Results()
	progCh := sc.Progress()
	for resCh != nil || progCh != nil {
		select {
		case r, ok := <-resCh:
			if !ok {
				resCh = nil
				continue
			}
			all = append(all, r)
			batch = append(batch, r)
			if len(batch) >= 300 {
				flush()
			}
		case p, ok := <-progCh:
			if !ok {
				progCh = nil
				continue
			}
			runtime.EventsEmit(a.ctx, "scan:progress", p)
			debugLog("consume 进度 q=%d 命中=%d 通配=%d 待复核=%d", p.Queried, p.Hits, p.Wildcards, p.Unreviewed)
		}
	}
	close(stopTick)
	flush()

	stats := <-statsCh
	if a.stopped.Load() {
		stats.Error = "已手动停止"
	}
	debugLog("consume 完成 stats=%+v", stats)
	runtime.EventsEmit(a.ctx, "scan:done", stats)
	a.saveScan(sc, paramsJSON, stats, all)
}

// saveScan 把本次扫描与全部结果落库，成功后通知前端刷新历史。
func (a *App) saveScan(sc *engine.Scanner, paramsJSON string, stats model.Stats, all []model.Result) {
	if a.store == nil {
		return
	}
	status := "done"
	if a.stopped.Load() {
		status = "stopped"
	}
	_, err := a.store.SaveScan(model.ScanSummary{
		Target:     model.NormalizeName(sc.Config().Root),
		Params:     paramsJSON,
		StartedAt:  time.Now().Add(-time.Duration(stats.DurationMS) * time.Millisecond).UnixMilli(),
		DurationMS: stats.DurationMS,
		Queried:    stats.Queried,
		Hits:       stats.Hits,
		Wildcards:  stats.Wildcards,
		Unreviewed: stats.Unreviewed,
		Status:     status,
		Error:      stats.Error,
	}, all)
	if err != nil {
		debugLog("历史落库失败: %v", err)
		return
	}
	debugLog("历史落库成功（结果 %d 条）", len(all))
	runtime.EventsEmit(a.ctx, "history:changed", nil)
}

// ListScans 返回全部历史扫描（新→旧）。
func (a *App) ListScans() ([]model.ScanSummary, error) {
	if a.store == nil {
		return nil, errors.New("历史存储未初始化")
	}
	return a.store.ListScans()
}

// LoadScanResults 返回某次历史扫描的全部结果。
func (a *App) LoadScanResults(scanID int64) ([]model.ResultRow, error) {
	if a.store == nil {
		return nil, errors.New("历史存储未初始化")
	}
	return a.store.LoadResults(scanID)
}

// UpdateReview 更新一条历史结果的复核结论（verdict: ""/confirmed/false）。
func (a *App) UpdateReview(scanID, resultID int64, verdict, note string) error {
	if a.store == nil {
		return errors.New("历史存储未初始化")
	}
	return a.store.UpdateReview(scanID, resultID, model.ReviewVerdict(verdict), note)
}

// DiffScans 对比两次历史扫描的资产差异（a=基准旧，b=当前新）。
func (a *App) DiffScans(aID, bID int64) (*model.DiffResult, error) {
	if a.store == nil {
		return nil, errors.New("历史存储未初始化")
	}
	return a.store.DiffScans(aID, bID)
}

// RecheckBatch 对某次历史扫描的域名做批量 HTTP 探活复核。
// names 为空表示处理该次扫描的全部记录；否则只处理指定名字。
// 可达判为 confirmed，不可达保留原 verdict 仅更新备注；返回每条的探活结果。
func (a *App) RecheckBatch(scanID int64, names []string) ([]model.RecheckItem, error) {
	if a.store == nil {
		return nil, errors.New("历史存储未初始化")
	}
	rows, err := a.store.LoadResults(scanID)
	if err != nil {
		return nil, err
	}
	sel := make(map[string]bool, len(names))
	for _, n := range names {
		sel[n] = true
	}
	hasSel := len(names) > 0

	type target struct {
		id   int64
		name string
		verdict model.ReviewVerdict
	}
	var targets []target
	for _, r := range rows {
		if hasSel && !sel[r.Name] {
			continue
		}
		targets = append(targets, target{id: r.ID, name: r.Name, verdict: r.Verdict})
	}
	if len(targets) == 0 {
		return nil, errors.New("没有可复核的记录")
	}

	hosts := make([]string, len(targets))
	for i, t := range targets {
		hosts[i] = t.name
	}
	results := httpcheck.CheckAll(a.ctx, hosts, 4*time.Second, 50)

	items := make([]model.RecheckItem, len(targets))
	for i, t := range targets {
		it := model.RecheckItem{
			Name: t.name, OK: results[i].OK,
			Status: results[i].Status, Scheme: results[i].Scheme, Note: results[i].Note,
		}
		items[i] = it

		verdict := t.verdict
		note := "HTTP 不可达"
		if it.OK {
			verdict = model.VerdictConfirmed
			note = it.Scheme + " 可达 " + it.Note
		}
		if err := a.store.UpdateReview(scanID, t.id, verdict, note); err != nil {
			debugLog("批量复核落库失败 %s: %v", t.name, err)
		}
		scheme := ""
		if it.OK {
			scheme = it.Scheme
		} else {
			scheme = "fail"
		}
		if err := a.store.UpdateHTTP(scanID, t.id, it.Status, scheme, it.OK); err != nil {
			debugLog("HTTP 探测落库失败 %s: %v", t.name, err)
		}
	}
	debugLog("RecheckBatch 完成: %d 条（可达 %d）", len(targets), okCount(results))
	runtime.EventsEmit(a.ctx, "history:changed", nil)
	return items, nil
}

// okCount 统计可达条数。
func okCount(rs []httpcheck.Result) int {
	n := 0
	for _, r := range rs {
		if r.OK {
			n++
		}
	}
	return n
}

// OpenURL 用系统默认浏览器打开链接（仅允许 http/https）。
func (a *App) OpenURL(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return errors.New("仅支持 http/https 链接")
	}
	runtime.BrowserOpenURL(a.ctx, url)
	return nil
}

// DeleteScanRecord 删除一条历史扫描（级联删除其全部结果）。
func (a *App) DeleteScanRecord(scanID int64) error {
	if a.store == nil {
		return errors.New("历史存储未初始化")
	}
	if err := a.store.DeleteScan(scanID); err != nil {
		return err
	}
	runtime.EventsEmit(a.ctx, "history:changed", nil)
	return nil
}

// DeleteResultRecord 删除一条结果记录。
func (a *App) DeleteResultRecord(scanID, resultID int64) error {
	if a.store == nil {
		return errors.New("历史存储未初始化")
	}
	return a.store.DeleteResult(scanID, resultID)
}

// DeleteResults 批量删除同一扫描下的多条结果。
func (a *App) DeleteResults(scanID int64, resultIDs []int64) error {
	if a.store == nil {
		return errors.New("历史存储未初始化")
	}
	return a.store.DeleteResults(scanID, resultIDs)
}
