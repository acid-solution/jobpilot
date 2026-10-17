package abilityidentity

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// NormalizeName is shared by catalog matching, vector normalization and review
// deduplication. Keep meaningful language symbols such as '+' and '#'.
func NormalizeName(value string) string {
	value = strings.ToLower(norm.NFKC.String(strings.TrimSpace(value)))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("-_.:/\\·", r) {
			return -1
		}
		return r
	}, value)
}

// IsAtomicLabel rejects obvious lists and sentences, not every conjunction:
// names such as CI/CD and 数据结构与算法 are legitimate catalog units.
func IsAtomicLabel(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 60 || strings.ContainsAny(value, "、，,；;。\n\r：:") {
		return false
	}
	if i := strings.Index(value, "等"); i > 0 {
		tail := value[i+len("等"):]
		for _, marker := range []string{"常用", "相关", "核心", "技术", "框架", "工具", "语言", "数据库", "协议", "计算机", "各类", "多种"} {
			if strings.Contains(tail, marker) {
				return false
			}
		}
	}
	return !strings.Contains(value, "者优先") && !strings.Contains(value, "相关项目") && !strings.Contains(value, "实践经历")
}
