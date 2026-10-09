package server

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// staticAssets 为内嵌前端资源提供两级缓存与按需 gzip：
//
//   - /assets/* 是 Vite 的内容哈希产物（内容变即文件名变），可不可变长缓存；
//   - 其余（入口 HTML / favicon / manifest）不带哈希，必须每次协商——否则
//     新构建的 HTML 会继续指向已删除的旧哈希资源（经典白屏成因）。
//
// gzip 只在客户端声明支持、资源为文本（非已压缩格式）、且非 Range 请求时
// 启用。按 MDN《Compression in HTTP》：内容协商后的响应必须携带
// Vary: Accept-Encoding，否则缓存可能把压缩体喂给不支持的客户端；
// 对已压缩格式二次压缩是负收益（不压）。
func staticAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if !staticCompressible(r) {
			next.ServeHTTP(w, r)
			return
		}
		// 协商维度必须在两种响应上都声明，缓存才能为压缩/未压缩各存一份。
		w.Header().Add("Vary", "Accept-Encoding")
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, writer: gz}, r)
	})
}

// compressedStaticExts 是已压缩或不可再压的格式：二次压缩无收益甚至更大。
var compressedStaticExts = []string{
	".png", ".jpg", ".jpeg", ".webp", ".gif", ".ico", ".avif",
	".woff", ".woff2", ".ttf", ".otf",
	".gz", ".br", ".zip", ".mp4", ".webm",
}

func staticCompressible(r *http.Request) bool {
	// Range 请求会得到 206 分段体，与整体 Content-Encoding 语义冲突。
	if r.Header.Get("Range") != "" {
		return false
	}
	path := strings.ToLower(r.URL.Path)
	for _, ext := range compressedStaticExts {
		if strings.HasSuffix(path, ext) {
			return false
		}
	}
	return true
}

// gzipResponseWriter 把响应体写进 gzip 流。WriteHeader 时删除
// Content-Length（编码后长度未知，且与压缩体不符）。
type gzipResponseWriter struct {
	http.ResponseWriter
	writer *gzip.Writer
	wrote  bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wrote {
		return
	}
	g.wrote = true
	g.Header().Del("Content-Length")
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	if !g.wrote {
		g.WriteHeader(http.StatusOK)
	}
	return g.writer.Write(p)
}
