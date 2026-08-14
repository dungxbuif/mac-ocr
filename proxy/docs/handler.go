package docs

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed static/*
var staticFS embed.FS

type dynamicFS struct {
	replacements map[string]string
	cache        sync.Map
}

func Handler(apiBaseURL, docsBaseURL string) http.Handler {
	dfs := &dynamicFS{
		replacements: map[string]string{
			"https://ocr.dungxbuif.com": strings.TrimRight(apiBaseURL, "/"),
			"https://ocr.example.com":   strings.TrimRight(apiBaseURL, "/"),
			"https://docs.example.com":  strings.TrimRight(docsBaseURL, "/"),
			"{{PUBLIC_API_URL}}":        strings.TrimRight(apiBaseURL, "/"),
			"{{PUBLIC_DOCS_URL}}":       strings.TrimRight(docsBaseURL, "/"),
		},
	}
	return dfs
}

func SwaggerHandler(apiBaseURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile("static/swagger.html")
		if err != nil {
			http.Error(w, "swagger template not found", http.StatusInternalServerError)
			return
		}
		replaced := bytes.ReplaceAll(data, []byte("https://ocr.dungxbuif.com"), []byte(strings.TrimRight(apiBaseURL, "/")))
		replaced = bytes.ReplaceAll(replaced, []byte("https://ocr.example.com"), []byte(strings.TrimRight(apiBaseURL, "/")))
		replaced = bytes.ReplaceAll(replaced, []byte("{{PUBLIC_API_URL}}"), []byte(strings.TrimRight(apiBaseURL, "/")))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(replaced)
	})
}

func (dfs *dynamicFS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	fullPath := "static/" + filepath.Clean(path)

	content, cType, err := dfs.readFile(fullPath)
	if err != nil && !strings.Contains(path, ".") {
		content, cType, err = dfs.readFile(strings.TrimSuffix(fullPath, "/") + "/index.html")
	}
	if err != nil {
		if strings.HasSuffix(path, ".md") || !strings.Contains(path, ".") {
			fullPath = "static/index.html"
			content, cType, err = dfs.readFile(fullPath)
		}
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", cType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(content)
}

func (dfs *dynamicFS) readFile(name string) ([]byte, string, error) {
	if val, ok := dfs.cache.Load(name); ok {
		cached := val.(cachedFile)
		return cached.data, cached.contentType, nil
	}

	raw, err := staticFS.ReadFile(name)
	if err != nil {
		return nil, "", err
	}

	ext := filepath.Ext(name)
	cType := mime.TypeByExtension(ext)
	if cType == "" {
		switch ext {
		case ".md":
			cType = "text/markdown; charset=utf-8"
		case ".html":
			cType = "text/html; charset=utf-8"
		case ".js":
			cType = "application/javascript; charset=utf-8"
		case ".css":
			cType = "text/css; charset=utf-8"
		case ".json":
			cType = "application/json; charset=utf-8"
		default:
			cType = "application/octet-stream"
		}
	}

	isText := strings.HasPrefix(cType, "text/") || strings.Contains(cType, "json") || strings.Contains(cType, "javascript") || ext == ".md"
	if isText {
		text := string(raw)
		for target, repl := range dfs.replacements {
			if repl != "" {
				text = strings.ReplaceAll(text, target, repl)
			}
		}
		raw = []byte(text)
	}

	dfs.cache.Store(name, cachedFile{data: raw, contentType: cType})
	return raw, cType, nil
}

type cachedFile struct {
	data        []byte
	contentType string
}

func (dfs *dynamicFS) Open(name string) (fs.File, error) {
	data, _, err := dfs.readFile("static/" + filepath.Clean(name))
	if err != nil {
		return nil, err
	}
	return &inMemoryFile{Reader: bytes.NewReader(data), name: name}, nil
}

type inMemoryFile struct {
	*bytes.Reader
	name string
}

func (f *inMemoryFile) Stat() (fs.FileInfo, error) {
	return inMemoryFileInfo{name: f.name, size: f.Reader.Size()}, nil
}

func (f *inMemoryFile) Close() error { return nil }

type inMemoryFileInfo struct {
	name string
	size int64
}

func (i inMemoryFileInfo) Name() string       { return i.name }
func (i inMemoryFileInfo) Size() int64        { return i.size }
func (i inMemoryFileInfo) Mode() fs.FileMode  { return 0444 }
func (i inMemoryFileInfo) ModTime() time.Time { return time.Time{} }
func (i inMemoryFileInfo) IsDir() bool        { return false }
func (i inMemoryFileInfo) Sys() any           { return nil }

var _ fs.FS = (*dynamicFS)(nil)
var _ io.Closer = (*inMemoryFile)(nil)
