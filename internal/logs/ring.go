package logs

import (
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// maxMessageBytes 限定单条 entry 的大小。ring 已经限制了条目数量，
// 但一个过大的上游错误页否则会占满它的全部额度。
const maxMessageBytes = 8 * 1024

// clampMessage 截断过长的行，且不切断 UTF-8 rune。
func clampMessage(s string) string {
	if len(s) <= maxMessageBytes {
		return s
	}
	cut := maxMessageBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…[truncated]"
}

type Entry struct {
	ID        uint64    `json:"id"`
	Time      time.Time `json:"time"`
	Level     string    `json:"level"`
	AccountID string    `json:"account_id,omitempty"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

type Ring struct {
	mu      sync.RWMutex
	entries []Entry
	size    int
	nextID  atomic.Uint64
}

var (
	accountPrefixRe = regexp.MustCompile(`(?i)\[account=([^\]]+)\]`)
	bearerRe        = regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9\-._~+/]+=*`)
	apiKeyAssignRe  = regexp.MustCompile(`(?i)((?:PROXY_API_KEY|api[_-]?key|token)\s*[=:]\s*)(\S+)`)
	// 泛化匹配「initialized … key …: <secret>」形态：覆盖调用密钥与控制台
	// 口令两条初始化文案（此前只匹配前者），防止未来接入 ring 时泄漏。
	initializedKeyRe = regexp.MustCompile(`(?i)(initialized[^:]*key[^:]*:\s*)(\S+)`)
	// 裸 JWT 没有 "Bearer " 前缀，因此上面的规则会漏掉它。
	bareJWTRe = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
)

func NewRing(size int) *Ring {
	if size <= 0 {
		size = 2000
	}
	return &Ring{
		entries: make([]Entry, 0, size),
		size:    size,
	}
}

func (r *Ring) Write(p []byte) (int, error) {
	if r == nil {
		return len(p), nil
	}
	text := string(p)
	if text == "" {
		return 0, nil
	}
	for _, line := range splitLines(text) {
		r.appendLine(line)
	}
	return len(p), nil
}

func (r *Ring) Append(message string) Entry {
	return r.appendLine(message)
}

func (r *Ring) Snapshot(afterID uint64, limit, offset int, level, query, accountID string) ([]Entry, int) {
	if r == nil {
		return nil, 0
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	level = strings.ToLower(strings.TrimSpace(level))
	query = strings.TrimSpace(query)
	accountID = strings.TrimSpace(accountID)

	r.mu.RLock()
	defer r.mu.RUnlock()
	matched := make([]Entry, 0, len(r.entries))
	for _, entry := range r.entries {
		if entry.ID <= afterID {
			continue
		}
		if level != "" && level != "all" && entry.Level != level {
			continue
		}
		if accountID != "" && entry.AccountID != accountID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(entry.Message), strings.ToLower(query)) &&
			!strings.Contains(strings.ToLower(entry.AccountID), strings.ToLower(query)) {
			continue
		}
		matched = append(matched, entry)
	}
	total := len(matched)
	for i, j := 0, total-1; i < j; i, j = i+1, j-1 {
		matched[i], matched[j] = matched[j], matched[i]
	}
	if offset >= total {
		return []Entry{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	out := make([]Entry, end-offset)
	copy(out, matched[offset:end])
	return out, total
}

func (r *Ring) Latest(limit int) []Entry {
	items, _ := r.Snapshot(0, limit, 0, "", "", "")
	return items
}

func (r *Ring) appendLine(line string) Entry {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return Entry{}
	}
	accountID := ""
	if match := accountPrefixRe.FindStringSubmatch(line); len(match) == 2 {
		accountID = strings.TrimSpace(match[1])
	}
	entry := Entry{
		ID:        r.nextID.Add(1),
		Time:      time.Now().UTC(),
		Level:     detectLevel(line),
		AccountID: accountID,
		Source:    detectSource(line),
		Message:   clampMessage(redactSecrets(line)),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) >= r.size {
		copy(r.entries, r.entries[1:])
		r.entries[len(r.entries)-1] = entry
		r.entries = r.entries[:r.size]
	} else {
		r.entries = append(r.entries, entry)
	}
	return entry
}

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	parts := strings.Split(text, "\n")
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		if i == len(parts)-1 && part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func detectLevel(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, " error") || strings.Contains(lower, "[error]") || strings.Contains(lower, "failed") || strings.Contains(lower, "panic"):
		return "error"
	case strings.Contains(lower, " warn") || strings.Contains(lower, "[warn]") || strings.Contains(lower, "warning"):
		return "warn"
	default:
		return "info"
	}
}

func detectSource(line string) string {
	switch {
	case strings.Contains(line, "[daemon]"):
		return "daemon"
	case strings.Contains(line, "[sse]"):
		return "sse"
	case strings.Contains(line, "[security]"):
		return "security"
	default:
		return "proxy"
	}
}

func redactSecrets(line string) string {
	line = bareJWTRe.ReplaceAllString(line, "***")
	line = bearerRe.ReplaceAllString(line, "${1}***")
	line = apiKeyAssignRe.ReplaceAllString(line, "${1}***")
	line = initializedKeyRe.ReplaceAllString(line, "${1}***")
	return line
}
