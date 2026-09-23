package profile

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"rsc.io/pdf"
)

const maxExtractedDocumentBytes = 4 << 20

var ErrUnsupportedDocument = errors.New("unsupported profile document")

// ExtractMaterialDocument extracts text in memory. Callers decide whether the
// returned text should later be stored as a material; the original file is not persisted.
func ExtractMaterialDocument(filename string, data []byte) (title, text string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			title, text = "", ""
			err = fmt.Errorf("parse document: %v", recovered)
		}
	}()

	extension := strings.ToLower(filepath.Ext(filename))
	title = strings.TrimSpace(strings.TrimSuffix(filepath.Base(filename), extension))
	switch extension {
	case ".txt", ".md", ".markdown":
		if !utf8.Valid(data) {
			return "", "", fmt.Errorf("text document is not UTF-8")
		}
		text = string(data)
	case ".docx":
		text, err = extractDOCX(data)
	case ".pdf":
		text, err = extractPDF(data)
	default:
		return "", "", ErrUnsupportedDocument
	}
	if err != nil {
		return "", "", err
	}
	text = normalizeExtractedText(text)
	if utf8.RuneCountInString(text) < 20 {
		return "", "", fmt.Errorf("document contains too little readable text")
	}
	if utf8.RuneCountInString(text) > 100000 {
		return "", "", fmt.Errorf("document text exceeds 100000 characters")
	}
	if title == "" {
		title = "导入的简历或经历"
	}
	if runes := []rune(title); len(runes) > 120 {
		title = string(runes[:120])
	}
	return title, text, nil
}

func extractDOCX(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	for _, entry := range reader.File {
		if entry.Name != "word/document.xml" {
			continue
		}
		if entry.UncompressedSize64 > maxExtractedDocumentBytes {
			return "", fmt.Errorf("docx document.xml is too large")
		}
		stream, openErr := entry.Open()
		if openErr != nil {
			return "", fmt.Errorf("open docx content: %w", openErr)
		}
		defer stream.Close()
		return extractWordXML(io.LimitReader(stream, maxExtractedDocumentBytes+1))
	}
	return "", fmt.Errorf("docx does not contain word/document.xml")
}

func extractWordXML(reader io.Reader) (string, error) {
	decoder := xml.NewDecoder(reader)
	var builder strings.Builder
	inText := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("decode docx XML: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "t" {
				inText = true
			}
			if value.Name.Local == "tab" {
				builder.WriteByte('\t')
			}
			if value.Name.Local == "br" {
				builder.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				builder.Write([]byte(value))
			}
		case xml.EndElement:
			if value.Name.Local == "t" {
				inText = false
			}
			if value.Name.Local == "p" {
				builder.WriteByte('\n')
			}
		}
	}
	return builder.String(), nil
}

func extractPDF(data []byte) (string, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}
	var builder strings.Builder
	for pageNumber := 1; pageNumber <= reader.NumPage(); pageNumber++ {
		page := reader.Page(pageNumber)
		if page.V.IsNull() {
			continue
		}
		fragments := append([]pdf.Text(nil), page.Content().Text...)
		sort.SliceStable(fragments, func(i, j int) bool {
			if math.Abs(fragments[i].Y-fragments[j].Y) > 2 {
				return fragments[i].Y > fragments[j].Y
			}
			return fragments[i].X < fragments[j].X
		})
		var previous *pdf.Text
		for index := range fragments {
			fragment := &fragments[index]
			if strings.TrimSpace(fragment.S) == "" {
				continue
			}
			if previous != nil {
				lineThreshold := math.Max(2, math.Max(previous.FontSize, fragment.FontSize)*0.35)
				if math.Abs(previous.Y-fragment.Y) > lineThreshold {
					builder.WriteByte('\n')
				} else if fragment.X-(previous.X+previous.W) > math.Max(1, fragment.FontSize*0.15) {
					builder.WriteByte(' ')
				}
			}
			builder.WriteString(fragment.S)
			previous = fragment
		}
		builder.WriteString("\n\n")
		if builder.Len() > maxExtractedDocumentBytes {
			return "", fmt.Errorf("PDF extracted text is too large")
		}
	}
	return builder.String(), nil
}

func normalizeExtractedText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ReplaceAll(value, "\u00a0", " ")
	lines := strings.Split(value, "\n")
	result := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(result) > 0 && !blank {
				result = append(result, "")
				blank = true
			}
			continue
		}
		result = append(result, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}
