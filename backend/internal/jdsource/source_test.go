package jdsource

import (
	"strings"
	"testing"
)

func TestSourceCoordinatesSurviveChineseWhitespaceAndWindowing(t *testing.T) {
	raw := "网页导航\r\n后端开发工程师\n负责订单服务；熟悉 Go 和 PostgreSQL。\n" + strings.Repeat("无关说明ABCDEFGHIJKLMN0123456789", 900) + "\n最后岗位：负责 React 页面开发。"
	doc := New(raw)
	seen := map[string]bool{}
	for _, window := range doc.Windows() {
		view := doc.View(window)
		for _, block := range window {
			quote, ok := view.Quote([]string{block.ID})
			if !ok || !strings.Contains(raw, quote) {
				t.Fatalf("invalid immutable quote: %s", block.ID)
			}
			seen[block.ID] = true
		}
	}
	if !seen[doc.Blocks[len(doc.Blocks)-1].ID] {
		t.Fatal("last source block was silently truncated")
	}
	if _, ok := doc.Quote([]string{"invented"}); ok {
		t.Fatal("unknown source accepted")
	}
}
func TestSelectedDoesNotImportInterveningOtherJob(t *testing.T) {
	doc := New("后端岗位\n前端岗位\nGo 要求\n")
	text, ok := doc.Selected([]string{doc.Blocks[2].ID, doc.Blocks[0].ID})
	if !ok || strings.Contains(text, "前端") || !strings.HasPrefix(text, "后端") {
		t.Fatalf("mixed selected source: %q", text)
	}
}
