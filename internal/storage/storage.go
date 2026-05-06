package storage

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type StoredObject struct {
	Provider string `json:"provider"`
	Key      string `json:"key"`
	URL      string `json:"url,omitempty"`
}

type Store interface {
	Enabled() bool
	PutFile(ctx context.Context, key string, filePath string) (StoredObject, error)
}

type DisabledStore struct{}

func (DisabledStore) Enabled() bool { return false }
func (DisabledStore) PutFile(ctx context.Context, key string, filePath string) (StoredObject, error) {
	return StoredObject{}, nil
}

type PARStore struct {
	parURL     string
	prefix     string
	publicBase string
	httpClient *http.Client
}

func NewPARStore(parURL, prefix, publicBase string, timeout time.Duration) *PARStore {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &PARStore{
		parURL:     strings.TrimRight(strings.TrimSpace(parURL), "/"),
		prefix:     cleanPrefix(prefix),
		publicBase: strings.TrimRight(strings.TrimSpace(publicBase), "/"),
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (s *PARStore) Enabled() bool { return strings.TrimSpace(s.parURL) != "" }

func (s *PARStore) PutFile(ctx context.Context, key string, filePath string) (StoredObject, error) {
	key = s.fullKey(key)
	if !s.Enabled() {
		return StoredObject{}, nil
	}
	f, err := os.Open(filePath)
	if err != nil {
		return StoredObject{}, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return StoredObject{}, err
	}

	putURL := s.objectURL(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, f)
	if err != nil {
		return StoredObject{}, err
	}
	req.ContentLength = stat.Size()
	if ct := contentTypeForPath(filePath); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return StoredObject{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return StoredObject{}, fmt.Errorf("object storage put status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return StoredObject{Provider: "oci-par", Key: key, URL: s.safeURL(key)}, nil
}

func (s *PARStore) fullKey(key string) string {
	key = strings.Trim(strings.ReplaceAll(key, "\\", "/"), "/")
	if s.prefix == "" {
		return key
	}
	if key == "" {
		return s.prefix
	}
	return s.prefix + "/" + key
}

func (s *PARStore) objectURL(key string) string {
	parts := strings.Split(strings.Trim(key, "/"), "/")
	escaped := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		escaped = append(escaped, url.PathEscape(p))
	}
	if len(escaped) == 0 {
		return s.parURL
	}
	return s.parURL + "/" + strings.Join(escaped, "/")
}

func (s *PARStore) safeURL(key string) string {
	if s.publicBase != "" {
		return s.publicBase + "/" + strings.TrimLeft(key, "/")
	}
	return "object://oci-par/" + strings.TrimLeft(key, "/")
}

func ObjectKeyForPath(dataDir, filePath string) string {
	absData, err1 := filepath.Abs(dataDir)
	absFile, err2 := filepath.Abs(filePath)
	if err1 == nil && err2 == nil {
		if rel, err := filepath.Rel(absData, absFile); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return strings.Trim(strings.ReplaceAll(rel, "\\", "/"), "/")
		}
	}
	return path.Base(strings.ReplaceAll(filePath, "\\", "/"))
}

func cleanPrefix(prefix string) string {
	prefix = strings.ReplaceAll(strings.TrimSpace(prefix), "\\", "/")
	prefix = path.Clean("/" + prefix)
	prefix = strings.Trim(prefix, "/")
	if prefix == "." {
		return ""
	}
	return prefix
}

func contentTypeForPath(filePath string) string {
	if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(filePath))); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
