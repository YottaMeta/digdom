// digdom-cli 是 DigDom 引擎的命令行复用：不依赖 GUI，同一引擎/存储/字典逻辑。
//
// 用法：
//
//	digdom-cli -target example.com [-depth 0] [-concurrency 300] [-rps 0]
//	          [-dns 8.8.8.8,1.1.1.1] [-dict path] [-words "www,api"] [-all] [-json]
//	digdom-cli history [id]     列出历史扫描 / 查看某次结果
//	digdom-cli diff <a> <b>     对比两次历史（a=基准旧，b=当前新）
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"digdom/internal/brute"
	"digdom/internal/engine"
	"digdom/internal/model"
	"digdom/internal/store"
)

func main() {
	fs := flag.NewFlagSet("digdom-cli", flag.ExitOnError)
	target := fs.String("target", "", "根域目标，如 example.com")
	depth := fs.Int("depth", 0, "递归深度 0/1/2")
	conc := fs.Int("concurrency", 300, "并发数")
	rps := fs.Int("rps", 0, "限速/秒（0=不限）")
	dns := fs.String("dns", "", "DNS 服务器，逗号分隔（空=内置池）")
	dict := fs.String("dict", "", "字典文件路径（空=内置字典）")
	words := fs.String("words", "", "追加词，逗号/空白分隔")
	all := fs.Bool("all", false, "打印全部标签结果（默认仅命中）")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	db := fs.String("db", "", "SQLite 数据库路径（空=默认用户目录）")
	fs.Parse(os.Args[1:])

	st, err := store.Open(*db)
	if err != nil {
		fatal("打开历史库失败: %v", err)
	}
	defer st.Close()

	args := fs.Args()
	if len(args) > 0 {
		switch args[0] {
		case "history":
			if len(args) > 1 {
				id, e := strconv.ParseInt(args[1], 10, 64)
				if e != nil {
					fatal("history 参数必须是扫描 ID: %v", e)
				}
				viewScan(st, id, *asJSON)
			} else {
				listScans(st, *asJSON)
			}
			return
		case "diff":
			if len(args) < 3 {
				fatal("diff 需要两个扫描 ID: digdom-cli diff <a> <b>")
			}
			a, e1 := strconv.ParseInt(args[1], 10, 64)
			b, e2 := strconv.ParseInt(args[2], 10, 64)
			if e1 != nil || e2 != nil {
				fatal("diff 参数必须是扫描 ID")
			}
			diffScans(st, a, b)
			return
		}
		fatal("未知子命令 %q（支持 history / diff）", args[0])
	}

	if *target == "" {
		fmt.Fprintln(os.Stderr, usageText)
		os.Exit(2)
	}
	runScan(st, *target, *depth, *conc, *rps, *dns, *dict, *words, *all, *asJSON)
}

const usageText = `DigDom 命令行爆破工具

用法：
  digdom-cli -target example.com [-depth 0] [-concurrency 300] [-rps 0]
             [-dns 8.8.8.8,1.1.1.1] [-dict path] [-words "www,api"] [-all] [-json]
  digdom-cli history [id]
  digdom-cli diff <a> <b>
  digdom-cli -h`

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "digdom-cli: "+format+"\n", args...)
	os.Exit(1)
}

func runScan(st *store.Store, target string, depth, conc, rps int, dns, dict, words string, all, asJSON bool) {
	target = model.NormalizeName(target)
	if target == "" {
		fatal("目标不能为空")
	}

	var dictWords []string
	if dict != "" {
		w, err := engine.LoadDictFile(dict)
		if err != nil {
			fatal("加载字典失败: %v", err)
		}
		dictWords = w
	} else {
		dictWords = engine.DefaultDictWords()
	}
	dictWords = append(dictWords, engine.ParseDictText(words)...)
	dictWords = model.DedupeStrings(dictWords)
	if len(dictWords) == 0 {
		fatal("字典为空")
	}

	resolvers := parseServers(dns)
	if len(resolvers) == 0 {
		resolvers = brute.DefaultServers
	}

	cfg := model.Config{
		Root:        target,
		DictWords:   dictWords,
		MaxDepth:    depth,
		Concurrency: conc,
		RPS:         rps,
		Timeout:     3 * time.Second,
		Resolvers:   resolvers,
	}
	sc := engine.NewScanner(cfg)

	results := make([]model.Result, 0, 1024)
	printed := 0
	done := make(chan model.Stats, 1)
	go func() { done <- sc.Run() }()

	fmt.Fprintf(os.Stderr, "[digdom] 开始爆破 %s（词数=%d 深度=%d 并发=%d rps=%d）\n",
		target, len(dictWords), depth, conc, rps)

	stop := time.Now()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	progressCh := sc.Progress()
	quit := false
	for !quit {
		select {
		case r, ok := <-sc.Results():
			if !ok {
				quit = true
				break
			}
			results = append(results, r)
			if asJSON {
				continue
			}
			if all || r.Tag == model.TagHit {
				printed++
				fmt.Printf("%s\t%s\t%s\t%s\n", r.Name, r.Tag, strings.Join(r.IPs, " "), strings.Join(r.CNAMEs, " "))
			}
		case p, ok := <-progressCh:
			if ok {
				fmt.Fprintf(os.Stderr, "\r[digdom] 查询=%d 命中=%d 通配=%d 待复核=%d", p.Queried, p.Hits, p.Wildcards, p.Unreviewed)
			}
		case <-tick.C:
		}
	}
	stats := <-done
	fmt.Fprintf(os.Stderr, "\r[digdom] 完成：查询=%d 命中=%d 通配=%d 待复核=%d 耗时=%s（打印 %d 条）\n",
		stats.Queried, stats.Hits, stats.Wildcards, stats.Unreviewed, time.Since(stop).Round(time.Millisecond), printed)

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		out := struct {
			Stats   model.Stats      `json:"stats"`
			Results []model.Result   `json:"results"`
		}{stats, results}
		_ = enc.Encode(out)
	}

	scanID, err := st.SaveScan(model.ScanSummary{
		Target:     target,
		Params:     fmt.Sprintf(`{"depth":%d,"concurrency":%d,"rps":%d,"dns":%q,"dict":%q,"words":%d}`, depth, conc, rps, dns, dict, len(dictWords)),
		StartedAt:  time.Now().Add(-time.Duration(stats.DurationMS) * time.Millisecond).UnixMilli(),
		DurationMS: stats.DurationMS,
		Queried:    stats.Queried,
		Hits:       stats.Hits,
		Wildcards:  stats.Wildcards,
		Unreviewed: stats.Unreviewed,
		Status:     "done",
		Error:      stats.Error,
	}, results)
	if err != nil {
		fatal("落库失败: %v", err)
	}
	fmt.Fprintf(os.Stderr, "[digdom] 已保存到历史，scan id=%d\n", scanID)
}

// parseServers 解析用户 DNS 列表。
func parseServers(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\t'
	}) {
		p := model.NormalizeName(part)
		if p != "" {
			out = append(out, p)
		}
	}
	return model.DedupeStrings(out)
}

func listScans(st *store.Store, asJSON bool) {
	scans, err := st.ListScans()
	if err != nil {
		fatal("读取历史失败: %v", err)
	}
	if len(scans) == 0 {
		fmt.Println("（暂无历史扫描）")
		return
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(scans)
		return
	}
	fmt.Printf("%-4s %-24s %-8s %-6s %-6s %-6s %-6s\n", "ID", "开始时间", "目标", "查询", "命中", "通配", "待复核")
	for _, sc := range scans {
		t := "?"
		if sc.StartedAt > 0 {
			t = time.UnixMilli(sc.StartedAt).Format("2006-01-02 15:04")
		}
		fmt.Printf("%-4d %-24s %-8s %-6d %-6d %-6d %-6d\n",
			sc.ID, t, sc.Target, sc.Queried, sc.Hits, sc.Wildcards, sc.Unreviewed)
	}
}

func viewScan(st *store.Store, id int64, asJSON bool) {
	rows, err := st.LoadResults(id)
	if err != nil {
		fatal("读取结果失败: %v", err)
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(rows)
		return
	}
	if len(rows) == 0 {
		fmt.Println("（该扫描无结果）")
		return
	}
	for _, r := range rows {
		v := string(r.Verdict)
		if v == "" {
			v = "-"
		}
		fmt.Printf("%-8s %-6s %-6s %-40s %s\n", r.Tag, v, strconv.Itoa(r.Depth), r.Name, strings.Join(r.IPs, " "))
	}
}

func diffScans(st *store.Store, a, b int64) {
	d, err := st.DiffScans(a, b)
	if err != nil {
		fatal("diff 失败: %v", err)
	}
	fmt.Printf("对比 #%d → #%d：新增 %d 条，消失 %d 条\n", a, b, len(d.Added), len(d.Removed))
	for _, item := range d.Added {
		fmt.Printf("+ %-40s %s %s\n", item.Name, item.Tag, strings.Join(item.IPs, " "))
	}
	for _, item := range d.Removed {
		fmt.Printf("- %-40s %s %s\n", item.Name, item.Tag, strings.Join(item.IPs, " "))
	}
	if len(d.Added)+len(d.Removed) == 0 {
		fmt.Println("无差异")
	}
}
