package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestExtractProfileMaterialDocument(t *testing.T) {
	var docx bytes.Buffer
	archive := zip.NewWriter(&docx)
	entry, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="urn:test"><w:body><w:p><w:r><w:t>负责 Go 后端开发与接口设计</w:t></w:r></w:p><w:p><w:r><w:t>熟悉 PostgreSQL、Redis 和 Docker</w:t></w:r></w:p></w:body></w:document>`))
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "后端开发简历.docx")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(docx.Bytes())
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/extract", extractProfileMaterialDocument())
	request := httptest.NewRequest(http.MethodPost, "/extract", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Data struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"data"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.Title != "后端开发简历" || !bytes.Contains([]byte(result.Data.Text), []byte("PostgreSQL")) {
		t.Fatalf("unexpected extraction response: %#v", result.Data)
	}
}
