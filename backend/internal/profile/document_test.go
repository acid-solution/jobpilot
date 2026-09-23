package profile

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestExtractMaterialDocumentFromDOCX(t *testing.T) {
	var document bytes.Buffer
	archive := zip.NewWriter(&document)
	entry, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="urn:test"><w:body><w:p><w:r><w:t>负责 Go 后端服务开发</w:t></w:r></w:p><w:p><w:r><w:t>使用 PostgreSQL 与 Redis</w:t></w:r></w:p></w:body></w:document>`))
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}

	title, text, err := ExtractMaterialDocument("后端简历.docx", document.Bytes())
	if err != nil {
		t.Fatalf("extract docx: %v", err)
	}
	if title != "后端简历" || text != "负责 Go 后端服务开发\n使用 PostgreSQL 与 Redis" {
		t.Fatalf("unexpected extraction: title=%q text=%q", title, text)
	}
}

func TestExtractMaterialDocumentRejectsUnsupportedFile(t *testing.T) {
	if _, _, err := ExtractMaterialDocument("resume.exe", []byte("not a resume document")); err == nil {
		t.Fatal("expected unsupported document error")
	}
}
