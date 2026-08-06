package model

import (
	"strings"
	"time"
)

// Tag 表示单个子域的打标结果。
type Tag string

const (
	TagHit        Tag = "hit"        // 命中：解析到稳定 IP / CNAME
	TagWildcard   Tag = "wildcard"   // 通配符：泛解析命中，需人工确认
	TagUnreviewed Tag = "unreviewed" // 待复核：解析不稳定 / 仅 CNAME / 解析错误
)

func (t Tag) Label() string {
	switch t {
	case TagHit:
		return "命中"
	case TagWildcard:
		return "通配符"
	case TagUnreviewed:
		return "待复核"
	}
	return string(t)
}

// Result 一条爆破结果。
type Result struct {
	Name   string   `json:"name"`
	IPs    []string `json:"ips"`
	CNAMEs []string `json:"cnames"`
	Tag    Tag      `json:"tag"`
	Base   string   `json:"base"`
	Depth  int      `json:"depth"`
}

// Progress 扫描过程中的实时统计。
type Progress struct {
	Queried    int `json:"queried"`
	Hits       int `json:"hits"`
	Wildcards  int `json:"wildcards"`
	Unreviewed int `json:"unreviewed"`
	Depth      int `json:"depth"`
	Active     bool `json:"active"`
}

// Stats 扫描结束汇总。
type Stats struct {
	Queried    int      `json:"queried"`
	Hits       int      `json:"hits"`
	Wildcards  int      `json:"wildcards"`
	Unreviewed int      `json:"unreviewed"`
	DurationMS int64    `json:"duration_ms"`
	Error      string   `json:"error"`
}

// Config 一次爆破的完整参数。
type Config struct {
	Root        string
	DictWords   []string
	MaxDepth    int
	Concurrency int
	RPS         int // 0 = 不限速
	Timeout     time.Duration
	Resolvers   []string
}

// ReviewVerdict 复核结论。
type ReviewVerdict string

const (
	VerdictNone      ReviewVerdict = ""          // 未复核
	VerdictConfirmed ReviewVerdict = "confirmed" // 确认真实存在
	VerdictFalse     ReviewVerdict = "false"     // 确认误报/虚假
)

// ScanSummary 一条历史扫描的列表概要。
// StartedAt 用 Unix 毫秒时间戳（int64），避免 time.Time 导致 Wails 绑定生成失败。
type ScanSummary struct {
	ID         int64  `json:"id"`
	Target     string `json:"target"`
	Params     string `json:"params"`
	StartedAt  int64  `json:"started_at"`
	DurationMS int64  `json:"duration_ms"`
	Queried    int    `json:"queried"`
	Hits       int    `json:"hits"`
	Wildcards  int    `json:"wildcards"`
	Unreviewed int    `json:"unreviewed"`
	Status     string `json:"status"` // done / stopped / error
	Error      string `json:"error"`
}

// ResultRow 结果表一行（含复核与 HTTP 探测字段）。
type ResultRow struct {
	ID         int64         `json:"id"`
	Name       string        `json:"name"`
	IPs        []string      `json:"ips"`
	CNAMEs     []string      `json:"cnames"`
	Tag        Tag           `json:"tag"`
	Base       string        `json:"base"`
	Depth      int           `json:"depth"`
	Verdict    ReviewVerdict `json:"verdict"`
	Note       string        `json:"note"`
	HTTPStatus int           `json:"http_status"` // 0=未探/不可达
	HTTPScheme string        `json:"http_scheme"` // https/http/fail/""(未探)
	HTTPOK     bool          `json:"http_ok"`
}

// DiffItem 单条对比差异。
type DiffItem struct {
	Name     string        `json:"name"`
	State    string        `json:"state"` // added / removed
	Tag      Tag           `json:"tag"`
	IPs      []string      `json:"ips"`
	Verdict  ReviewVerdict `json:"verdict"`
	ScanID   int64         `json:"scan_id"` // 该状态所属的扫描
}

// RecheckItem 单条批量复核的产物。
type RecheckItem struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Status int    `json:"status"` // HTTP 状态码，0=不可达
	Scheme string `json:"scheme"` // https / http
	Note   string `json:"note"`
}

// DiffResult 两次扫描的资产差异。
type DiffResult struct {
	Added   []DiffItem `json:"added"`   // B 中出现（新增）
	Removed []DiffItem `json:"removed"` // A 存在、B 消失
}

// NormalizeName 统一小写、去掉尾点、去空白。
func NormalizeName(s string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimSuffix(s, ".")))
}

// DedupeStrings 保序去重。
func DedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
