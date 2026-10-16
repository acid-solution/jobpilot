// Package jdsource owns the immutable source coordinates used by JD models.
// Models select block IDs; only this package constructs evidence quotations.
package jdsource

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

type Block struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Start int    `json:"-"`
	End   int    `json:"-"`
}

type Document struct {
	Raw    string
	Blocks []Block
	byID   map[string]Block
}

func (d Document) View(blocks []Block) Document {
	view := Document{Raw: d.Raw, Blocks: blocks, byID: make(map[string]Block)}
	for _, b := range blocks {
		if original, ok := d.byID[b.ID]; ok {
			view.byID[b.ID] = original
		}
	}
	return view
}

func New(raw string) Document {
	d := Document{Raw: raw, byID: make(map[string]Block)}
	start, count := 0, 0
	flush := func(end int) {
		if strings.TrimSpace(raw[start:end]) != "" {
			b := Block{ID: fmt.Sprintf("b%d", start), Text: raw[start:end], Start: start, End: end}
			d.Blocks = append(d.Blocks, b)
			d.byID[b.ID] = b
		}
		start = end
		count = 0
	}
	for offset, r := range raw {
		count++
		if r == '\n' || r == '；' || r == ';' || r == '。' || count >= 800 {
			flush(offset + utf8.RuneLen(r))
		}
	}
	if start < len(raw) {
		flush(len(raw))
	}
	return d
}

func (d Document) Quote(ids []string) (string, bool) {
	if len(ids) == 0 {
		return "", false
	}
	start, end := len(d.Raw), 0
	for _, id := range ids {
		b, ok := d.byID[id]
		if !ok {
			return "", false
		}
		if b.Start < start {
			start = b.Start
		}
		if b.End > end {
			end = b.End
		}
	}
	if end <= start {
		return "", false
	}
	return strings.TrimSpace(d.Raw[start:end]), true
}

// Selected returns only the selected blocks in source order, without importing
// intervening content belonging to another job.
func (d Document) Selected(ids []string) (string, bool) {
	seen := map[string]bool{}
	blocks := []Block{}
	for _, id := range ids {
		b, ok := d.byID[id]
		if !ok {
			return "", false
		}
		if !seen[id] {
			blocks = append(blocks, b)
			seen[id] = true
		}
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].Start < blocks[j].Start })
	var text strings.Builder
	for i, b := range blocks {
		if i > 0 {
			previous := blocks[i-1]
			gap := d.Raw[previous.End:b.Start]
			if strings.TrimSpace(gap) == "" {
				text.WriteString(gap)
			} else {
				text.WriteString("\n")
			}
		}
		text.WriteString(b.Text)
	}
	return strings.TrimSpace(text.String()), len(blocks) > 0
}

func (d Document) Windows() [][]Block {
	// Repeated qualifications can belong to distinct jobs. Keep their separate
	// coordinates rather than deduplicating away another job's requirements.
	blocks := d.Blocks
	result := [][]Block{}
	for start := 0; start < len(blocks); {
		end, size := start, 0
		for end < len(blocks) {
			n := utf8.RuneCountInString(blocks[end].Text)
			if end > start && size+n > 8000 {
				break
			}
			size += n
			end++
		}
		result = append(result, blocks[start:end])
		if end == len(blocks) {
			break
		}
		next, overlap := end, 0
		for next > start+1 && overlap < 1000 {
			next--
			overlap += utf8.RuneCountInString(blocks[next].Text)
		}
		start = next
	}
	return result
}
