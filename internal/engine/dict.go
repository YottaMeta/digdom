package engine

import (
	"embed"
	"fmt"
	"os"
	"strings"
)

//go:embed dict/default.txt
var dictFS embed.FS

// DefaultDictWords 返回内置字典（去除空行与 # 注释）。
func DefaultDictWords() []string {
	b, err := dictFS.ReadFile("dict/default.txt")
	if err != nil {
		return nil
	}
	return parseDict(b)
}

// LoadDictFile 从磁盘加载字典文件（跳过空行与 # 注释）。
func LoadDictFile(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	words := parseDict(b)
	if len(words) == 0 {
		return nil, fmt.Errorf("字典文件为空: %s", path)
	}
	return words, nil
}

func parseDict(b []byte) []string {
	var words []string
	for _, line := range strings.Split(string(b), "\n") {
		w := strings.TrimSpace(line)
		if w == "" || strings.HasPrefix(w, "#") {
			continue
		}
		words = append(words, w)
	}
	return words
}

// ParseDictText 解析用户自定义字典（空白 / 换行 / 逗号分隔）。
func ParseDictText(text string) []string {
	var words []string
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ' ' || r == '\t' || r == ',' || r == '，'
	}) {
		w := strings.TrimSpace(part)
		if w == "" {
			continue
		}
		words = append(words, w)
	}
	return words
}
