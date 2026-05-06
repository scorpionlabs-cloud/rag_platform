package ingestion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type OCR struct{}

type ImageListManifest struct {
	SourcePath string `json:"source_path"`
	Mode       string `json:"mode"`
	Pages      []struct {
		Page      int    `json:"page"`
		Path      string `json:"path"`
		ObjectKey string `json:"object_key,omitempty"`
		ObjectURL string `json:"object_url,omitempty"`
	} `json:"pages"`
}

func (o OCR) Extract(path string) (string, error) {
	pages, err := o.ExtractPages(path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i, page := range pages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(page.Text)
	}
	return b.String(), nil
}

func (o OCR) ExtractPages(path string) ([]PageText, error) {
	if strings.HasSuffix(strings.ToLower(filepath.Base(path)), ".imagelist.json") {
		return o.extractImageListPages(path)
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt", ".md", ".csv", ".json", ".log":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return []PageText{{Page: 1, Text: string(data)}}, nil
	case ".pdf":
		if pages, err := extractPDFTextPages(path); err == nil && hasTextPages(pages) {
			return pages, nil
		}
		return o.extractPDFWithTesseractPages(path)
	default:
		text, err := o.extractWithTesseract(path)
		if err != nil {
			return nil, err
		}
		return []PageText{{Page: 1, Text: text}}, nil
	}
}

func (o OCR) extractImageListPages(path string) ([]PageText, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest ImageListManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("image list manifest: %w", err)
	}
	if len(manifest.Pages) == 0 {
		return nil, fmt.Errorf("image list manifest contains no pages")
	}
	outPages := make([]PageText, 0, len(manifest.Pages))
	var lastErr error
	for i, item := range manifest.Pages {
		pageNum := item.Page
		if pageNum <= 0 {
			pageNum = i + 1
		}
		text, err := o.extractWithTesseract(item.Path)
		if err != nil {
			lastErr = err
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		outPages = append(outPages, PageText{Page: pageNum, Text: text, ImagePath: item.Path, ObjectKey: item.ObjectKey, ObjectURL: item.ObjectURL})
	}
	if len(outPages) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no OCR text extracted from image list")
	}
	return outPages, nil
}

func hasTextPages(pages []PageText) bool {
	for _, page := range pages {
		if strings.TrimSpace(page.Text) != "" {
			return true
		}
	}
	return false
}

func (o OCR) extractWithTesseract(path string) (string, error) {
	out, err := exec.Command("tesseract", path, "stdout").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tesseract: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (o OCR) extractPDFWithTesseract(path string) (string, error) {
	pages, err := o.extractPDFWithTesseractPages(path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i, page := range pages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(page.Text)
	}
	return b.String(), nil
}

func (o OCR) extractPDFWithTesseractPages(path string) ([]PageText, error) {
	dir, err := os.MkdirTemp("", "rag-pdf-ocr-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	prefix := filepath.Join(dir, "page")
	out, err := exec.Command("pdftoppm", "-r", "200", "-png", path, prefix).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pdftoppm: %w: %s", err, strings.TrimSpace(string(out)))
	}

	pages, err := filepath.Glob(filepath.Join(dir, "page-*.png"))
	if err != nil {
		return nil, err
	}
	sort.Strings(pages)
	if len(pages) == 0 {
		return nil, fmt.Errorf("pdftoppm produced no pages")
	}

	outPages := make([]PageText, 0, len(pages))
	var lastErr error
	for i, page := range pages {
		pageText, err := o.extractWithTesseract(page)
		if err != nil {
			lastErr = err
			continue
		}
		if strings.TrimSpace(pageText) == "" {
			continue
		}
		outPages = append(outPages, PageText{Page: i + 1, Text: pageText})
	}
	if len(outPages) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no OCR text extracted")
	}
	return outPages, nil
}

func extractPDFText(path string) (string, error) {
	pages, err := extractPDFTextPages(path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i, page := range pages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(page.Text)
	}
	return b.String(), nil
}

func extractPDFTextPages(path string) ([]PageText, error) {
	out, err := exec.Command("pdftotext", "-layout", path, "-").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pdftotext: %w: %s", err, strings.TrimSpace(string(out)))
	}
	text := string(bytes.TrimSpace(out))
	parts := strings.Split(text, "\f")
	pages := make([]PageText, 0, len(parts))
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pages = append(pages, PageText{Page: i + 1, Text: part})
	}
	if len(pages) == 0 && strings.TrimSpace(text) != "" {
		pages = append(pages, PageText{Page: 1, Text: text})
	}
	return pages, nil
}
