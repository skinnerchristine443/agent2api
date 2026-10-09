package workbuddy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// 国际（global）每日活跃签到。
//
// global 区域没有 /v2/billing/meter/daily-checkin 发放。每日 30/50 额度走的是
// Web 控制台自身的「daily activity」流程，它分两步执行：
//
//	step 1  desktop-identity light chat/completions  （主线；ok 在这里置位）
//	step 2  web-console agent session 驱动至完成 (issue #75/#59/#90)
//
// 第 2 步是 Web 控制台的 ACP-over-SSE 会话：
//
//	POST /console/as/conversations/           排队一个会话
//	GET  /console/as/conversations/{id}/session  沙箱链接 + token
//	GET  <link>  Accept: text/event-stream       SSE 流，Acp-Connection-Id 头
//	POST <link>  Acp-Connection-Id + JSON-RPC     initialize -> session/load -> session/prompt
//	GET  /console/as/conversations/{id}          轮询状态直到 completed
//
// 这里只实现每日一轮所需的最小协议：无工具、无终端、无文件系统回调
// （与上游客户端的 capability 集合一致）。这是一个仅用标准库的实现。
//
// 一次运行完成并不等于发放成功：奖励由服务端发放，因此成功与否取决于是否观察到
// 一个新的额度套餐出现（见 activityCreditSnapshot），而绝不是流程本身跑完。

const (
	webAgentOrigin           = "https://www.workbuddy.ai"
	webAgentConversationsURL = webAgentOrigin + "/console/as/conversations/"
	// webAgentUserAgent 是 Web 控制台期望的浏览器 UA。它不是
	// desktop 通道所用的 CLI UserAgent。
	webAgentUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0"
	dailyActivityModel  = "deepseek-v4.1-flash"
	dailyActivityPrompt = "Hi"
	acpProtocolVersion  = 1

	webAgentSessionPathSuffix = "/session"

	// activityTurnCap 是我们等待一轮 ACP 达到 "completed" 的最长时间。
	// 它必须严格低于 providers.CheckinRequestBudget：一次存续时间超过其调用方
	// context 的签到会在中途被取消，永远无法领取奖励。
	// 由 TestActivityTimeoutWithinCheckinBudget 锁定。
	activityTurnCap = 60 * time.Second
	// activityDesktopChatTimeout 限定第 1 步（desktop-identity chat）。
	activityDesktopChatTimeout = 20 * time.Second
)

// activityPackagesTimeout 限定单次额度套餐读取。
const activityPackagesTimeout = 20 * time.Second

var (
	// 可在测试中覆盖，以避免长时间等待。
	activityTurnTimeout  = activityTurnCap
	activityPollInterval = 3 * time.Second
	// activityHandshakeReserve 从调用方预算中预留出来，用于
	// create/session 握手、三次 ACP POST 以及额度读取。
	activityHandshakeReserve = 20 * time.Second
	// activityWebSessionEnabled 是「只跑第 1 步」的开关。第 2 步是更脆弱的一半
	// （控制台 API 经常变动）；第 1 步是主线。禁用它即只运行 desktop chat。
	activityWebSessionEnabled = true
)

// ActivityErrorKind 对每日活跃失败进行分类，使调用方（以及控制台）能区分
// 「连不上机器」「机器答错了」与「机器说了不」。
type ActivityErrorKind string

const (
	ActivityNetwork  ActivityErrorKind = "network"
	ActivityProtocol ActivityErrorKind = "protocol"
	ActivityUpstream ActivityErrorKind = "upstream"
)

// ActivityError 是 DailyActivity 返回的可读、已分类错误。
type ActivityError struct {
	Kind    ActivityErrorKind
	Op      string
	Message string
	Err     error
}

func (e *ActivityError) Error() string {
	op := strings.TrimSpace(e.Op)
	switch {
	case op != "" && e.Message != "":
		return fmt.Sprintf("%s (%s): %s", op, e.Kind, e.Message)
	case op != "":
		return fmt.Sprintf("%s (%s)", op, e.Kind)
	default:
		return fmt.Sprintf("daily activity (%s): %s", e.Kind, e.Message)
	}
}

func (e *ActivityError) Unwrap() error { return e.Err }

func activityError(kind ActivityErrorKind, op, message string, err error) *ActivityError {
	return &ActivityError{Kind: kind, Op: op, Message: message, Err: err}
}

// ActivityUnsupportedError 标记上游账号未暴露的每日活跃端点。
// 调用方把它映射为「skipped」签到状态。
type ActivityUnsupportedError struct {
	Message string
}

func (e ActivityUnsupportedError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return "web activity check-in is not available for this account"
	}
	return e.Message
}

// ActivityUnconfirmedError 表示流程跑完了，但观察不到额度发放。调用方把它映射为
// 「skipped」：在这里报告「success」会是最糟的失败模式——一次什么都没发、
// 而所有监控都说它成功的签到。
type ActivityUnconfirmedError struct {
	Message string
}

func (e ActivityUnconfirmedError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return "daily activity completed but the credit grant could not be confirmed"
	}
	return e.Message
}

// ActivityPartialError 表示第 1 步（desktop chat）成功但第 2 步（web 控制台
// 会话）失败。它既区别于成功也区别于普通失败：调用方把它映射为「partial」状态。
type ActivityPartialError struct {
	Reward  float64
	Message string
}

func (e ActivityPartialError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return "desktop activity chat succeeded but the web console session failed"
	}
	return e.Message
}

// dailyActivitySandbox 是 session API 返回的沙箱描述符。
type dailyActivitySandbox struct {
	link      string
	token     string
	sessionID string
	cwd       string
}

// DailyActivity 运行国际每日活跃流程（第 1、2 步），返回人类可读的消息以及
// 已发放的额度。
//
// 成功语义：
//   - 第 1 步失败                          -> error
//   - 第 1 步 ok、第 2 步失败              -> ActivityPartialError（partial）
//   - 各步 ok、看到新额度套餐              -> nil（success），reward > 0
//   - 各步 ok、未观察到发放                -> ActivityUnconfirmedError（skipped）
//
// 「already」在流程开始前从既有的 *今天* 额度套餐检测（也来自一条明确的上游
// 业务消息）。
func (c *Client) DailyActivity(ctx context.Context, accountID string) (string, float64, error) {
	credential, err := c.resolvedCredential(ctx, accountID)
	if err != nil {
		return "", 0, err
	}
	if !credential.IsGlobal() {
		return "", 0, activityError(ActivityProtocol, "daily-activity",
			"web activity check-in is only for international accounts", nil)
	}
	client, err := c.httpClient(ctx, accountID)
	if err != nil {
		return "", 0, err
	}

	before, beforeOK := c.activityCreditSnapshot(ctx, accountID, credential)
	if beforeOK {
		if reward, ok := todayGrantPackages(before, time.Now()); ok {
			return "", 0, AlreadyCheckedInError{Msg: fmt.Sprintf(
				"today's activity reward already granted (+%.0f credits)", reward)}
		}
	}

	// 第 1 步：desktop-identity 轻量 chat（主线）。
	if err := c.activityDesktopChat(ctx, accountID, credential, client); err != nil {
		return "", 0, err
	}

	// 第 2 步：web 控制台 agent 会话（可选；可开关）。
	webMessage := ""
	webOK := true
	if activityWebSessionEnabled {
		message, sessionErr := c.activityWebSession(ctx, accountID, credential, client)
		var already AlreadyCheckedInError
		if errors.As(sessionErr, &already) {
			return "", 0, already
		}
		if sessionErr != nil {
			webOK = false
		} else {
			webMessage = message
		}
	}

	after, afterOK := c.activityCreditSnapshot(ctx, accountID, credential)
	reward, confirmed := 0.0, false
	if beforeOK && afterOK {
		reward, confirmed = grantFromNewPackages(before, after, time.Now())
	}
	// 第 1 步 ok 且第 2 步失败是可区分的「partial」，
	// 无论是否观察到发放。
	if !webOK {
		return "", reward, ActivityPartialError{
			Reward:  reward,
			Message: "desktop activity chat succeeded but the web console session failed",
		}
	}
	if !confirmed {
		return "", 0, ActivityUnconfirmedError{Message: fmt.Sprintf(
			"activity flow completed but no new credit package was observed (before=%s after=%s)",
			activitySnapshotNote(before, beforeOK), activitySnapshotNote(after, afterOK))}
	}
	message := fmt.Sprintf("daily activity completed (+%.0f credits)", reward)
	if webMessage != "" {
		message = webMessage + "; desktop chat completed"
	}
	return message, reward, nil
}

// activityDesktopChat 是第 1 步：desktop-identity 的轻量 chat/completions 调用。
// 请求体字段是固定的；载荷走共享的 PrepareBody 路径，因此 global 的开头 system
// 要求（code 11-128）与 chat 的处理方式一致。
func (c *Client) activityDesktopChat(ctx context.Context, accountID string, credential Credential, client *http.Client) error {
	body, err := json.Marshal(map[string]any{
		"model":            dailyActivityModel,
		"messages":         []map[string]any{{"role": "user", "content": dailyActivityPrompt}},
		"stream":           true,
		"max_tokens":       10,
		"reasoning_effort": "none",
	})
	if err != nil {
		return activityError(ActivityProtocol, "desktop chat", err.Error(), err)
	}
	stepCtx, cancel := context.WithTimeout(ctx, activityDesktopChatTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(stepCtx, http.MethodPost,
		credential.ChatBase()+pathChat, bytes.NewReader(PrepareBody(body)))
	if err != nil {
		return activityError(ActivityProtocol, "desktop chat", err.Error(), err)
	}
	SetChatHeaders(req.Header, credential)
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		return activityError(ActivityNetwork, "desktop chat", err.Error(), err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return activityError(ActivityUpstream, "desktop chat",
			fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncateBody(payload)), nil)
	}
	return nil
}

// activityWebSession 是第 2 步：创建会话、接入沙箱，并驱动一轮 ACP 直到完成。
func (c *Client) activityWebSession(ctx context.Context, accountID string, credential Credential, client *http.Client) (string, error) {
	conversation, already, err := c.activityCreateConversation(ctx, client, credential)
	if already != "" {
		return already, AlreadyCheckedInError{Msg: already}
	}
	if err != nil {
		return "", err
	}
	sandbox, err := c.activityConversationSession(ctx, client, credential, conversation)
	if err != nil {
		return "", err
	}
	result, err := c.activityRunTurn(ctx, client, credential, conversation, sandbox)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("web console session completed (%d chunks, %d ms)",
		result.chunks, result.elapsed.Milliseconds()), nil
}

// activityCreateConversation 排队一个 Web 控制台会话。业务的「已领取」响应以
// (message, message, nil) 形式暴露，使调用方能把它映射为「already」状态。
func (c *Client) activityCreateConversation(ctx context.Context, client *http.Client, credential Credential) (string, string, error) {
	body, err := json.Marshal(map[string]any{
		"prompt":             dailyActivityPrompt,
		"model":              dailyActivityModel,
		"conversationOrigin": "workbuddy-app",
		"plugins":            []map[string]string{{"name": "weixinpay", "marketplace": "codebuddy-builtin"}},
	})
	if err != nil {
		return "", "", activityError(ActivityProtocol, "create conversation", err.Error(), err)
	}
	status, payload, err := c.activityJSON(ctx, client, credential, http.MethodPost, webAgentConversationsURL, body)
	if err != nil {
		return "", "", err
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return "", "", ActivityUnsupportedError{Message: fmt.Sprintf(
			"web console conversations API unavailable (HTTP %d)", status)}
	}
	if status >= 300 {
		return "", "", activityError(ActivityUpstream, "create conversation",
			fmt.Sprintf("HTTP %d: %s", status, truncateBody(payload)), nil)
	}
	if msg, ok := dailyActivityAlreadyMessage(payload); ok {
		return "", msg, nil
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", "", activityError(ActivityProtocol, "create conversation",
			"unparseable response: "+truncateBody(payload), err)
	}
	if env.Code != 0 {
		return "", "", activityError(ActivityUpstream, "create conversation",
			fmt.Sprintf("code=%d msg=%s", env.Code, strings.TrimSpace(env.Msg)), nil)
	}
	if strings.TrimSpace(env.Data.ID) == "" {
		return "", "", activityError(ActivityProtocol, "create conversation",
			"response missing conversation id: "+truncateBody(payload), nil)
	}
	return env.Data.ID, "", nil
}

// activityConversationSession 读取某个会话的沙箱 link/token。
func (c *Client) activityConversationSession(ctx context.Context, client *http.Client, credential Credential, conversation string) (dailyActivitySandbox, error) {
	rawURL := webAgentConversationsURL + url.PathEscape(conversation) + webAgentSessionPathSuffix
	status, payload, err := c.activityJSON(ctx, client, credential, http.MethodGet, rawURL, nil)
	if err != nil {
		return dailyActivitySandbox{}, err
	}
	if status >= 300 {
		return dailyActivitySandbox{}, activityError(ActivityUpstream, "conversation session",
			fmt.Sprintf("HTTP %d: %s", status, truncateBody(payload)), nil)
	}
	var env struct {
		Data struct {
			Link       string `json:"link"`
			Endpoint   string `json:"endpoint"`
			Token      string `json:"token"`
			SessionID  string `json:"sessionId"`
			SessionID2 string `json:"session_id"`
			Cwd        string `json:"cwd"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return dailyActivitySandbox{}, activityError(ActivityProtocol, "conversation session",
			"unparseable response: "+truncateBody(payload), err)
	}
	data := env.Data
	if msg, ok := dailyActivityAlreadyMessage(payload); ok {
		return dailyActivitySandbox{}, AlreadyCheckedInError{Msg: msg}
	}
	link := strings.TrimSpace(data.Link)
	if link == "" {
		link = strings.TrimSpace(data.Endpoint)
	}
	token := strings.TrimSpace(data.Token)
	if link == "" || token == "" {
		return dailyActivitySandbox{}, activityError(ActivityProtocol, "conversation session",
			"sandbox not ready (missing link/token)", nil)
	}
	sessionID := strings.TrimSpace(data.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(data.SessionID2)
	}
	if sessionID == "" {
		sessionID = conversation
	}
	cwd := strings.TrimSpace(data.Cwd)
	if cwd == "" {
		cwd = "/workspace"
	}
	return dailyActivitySandbox{link: link, token: token, sessionID: sessionID, cwd: cwd}, nil
}

// activityConversationStatus 轮询某个会话的控制台状态。
func (c *Client) activityConversationStatus(ctx context.Context, client *http.Client, credential Credential, conversation string) (string, error) {
	rawURL := webAgentConversationsURL + url.PathEscape(conversation)
	status, payload, err := c.activityJSON(ctx, client, credential, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	if status >= 300 {
		return "", activityError(ActivityUpstream, "conversation status",
			fmt.Sprintf("HTTP %d: %s", status, truncateBody(payload)), nil)
	}
	var env struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", activityError(ActivityProtocol, "conversation status",
			"unparseable response: "+truncateBody(payload), err)
	}
	return strings.TrimSpace(env.Data.Status), nil
}

// activityPackage 是来自计费资源端点的一个额度套餐，派生自共享的 resourcePackage
// 解析器（client.go）。cycleStart 是权威的「本周期何时开始」信号（CycleStartTime）；
// keyNames 是为未确认兜底说明保留的原始字段名清单。
type activityPackage struct {
	id            string
	hasID         bool
	cycleStart    time.Time
	hasCycleStart bool
	cycleEnd      string
	createMS      int64
	remain        int64
	size          int64
	keyNames      []string
}

func (p activityPackage) key() string {
	if p.hasID {
		return "id:" + p.id
	}
	start := ""
	if p.hasCycleStart {
		start = p.cycleStart.Format("2006-01-02T15:04:05")
	}
	return fmt.Sprintf("anon:%s:%d:%s", start, p.remain, p.cycleEnd)
}

// activityCreditSnapshot 是账号额度套餐的某个时点快照。
type activityCreditSnapshot struct {
	remain   int64
	packages []activityPackage
}

// activityCreditSnapshot 通过共享的 UserResource 请求路径（client.go 中的
// fetchResourceAccounts）读取额度套餐列表，但保留了 UserResource 丢弃的每套餐
// 身份/时间字段，正是这让我们能区分新发放的套餐与无关的余额变化。
func (c *Client) activityCreditSnapshot(ctx context.Context, accountID string, credential Credential) (activityCreditSnapshot, bool) {
	stepCtx, cancel := context.WithTimeout(ctx, activityPackagesTimeout)
	defer cancel()
	accounts, _, err := c.fetchResourceAccounts(stepCtx, accountID, credential)
	if err != nil {
		return activityCreditSnapshot{}, false
	}
	snapshot := activityCreditSnapshot{}
	for _, raw := range accounts {
		pkg := activityPackageFromRaw(raw)
		snapshot.packages = append(snapshot.packages, pkg)
		snapshot.remain += pkg.remain
	}
	return snapshot, true
}

func activityPackageFromRaw(raw json.RawMessage) activityPackage {
	var pkg resourcePackage
	_ = json.Unmarshal(raw, &pkg)
	remain, _, size := packageRemainUsed(pkg)
	entry := activityPackage{
		remain:   remain,
		size:     size,
		cycleEnd: strings.TrimSpace(pkg.CycleEndTime),
		createMS: pkg.CreateTime,
		keyNames: rawFieldNames(raw),
	}
	entry.id, entry.hasID = resourceIdentity(pkg)
	if parsed, ok := parseBillingTime(pkg.CycleStartTime); ok {
		entry.cycleStart = parsed
		entry.hasCycleStart = true
	}
	return entry
}

// resourceIdentity 返回真实的套餐身份。字段名来自一次实时上游抓包：ResourceId
// （string）与 ResourceCycleId（int）是套餐/周期 id；PackageCode 与 PackageName
// 是兜底。上游没有 "PackageId" 字段。
func resourceIdentity(pkg resourcePackage) (string, bool) {
	if id := strings.TrimSpace(pkg.ResourceID); id != "" {
		return id, true
	}
	if pkg.ResourceCycleID > 0 {
		return fmt.Sprintf("cycle:%d", pkg.ResourceCycleID), true
	}
	if id := strings.TrimSpace(pkg.PackageCode); id != "" {
		return id, true
	}
	if id := strings.TrimSpace(pkg.PackageName); id != "" {
		return id, true
	}
	return "", false
}

// rawFieldNames 列出一个原始 Accounts 条目中出现的 JSON 键，并排序。它驱动未确认
// 兜底说明，使首次真实运行能显示我们的字段名是否与实时响应匹配。
func rawFieldNames(raw json.RawMessage) []string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// grantFromNewPackages 报告两次快照之间新出现的套餐所发放的额度。信号优先级：
//  1. 一个新套餐的 CycleStartTime 是今天（UTC+8）-> 发放；
//  2. 一个新套餐没有 CycleStartTime 但身份确为新（ResourceId）-> 发放（次要）；
//  3. 否则不发放（调用方报告中性状态，绝不猜测）。
func grantFromNewPackages(before, after activityCreditSnapshot, now time.Time) (float64, bool) {
	if len(after.packages) == 0 {
		return 0, false
	}
	seen := make(map[string]struct{}, len(before.packages))
	for _, pkg := range before.packages {
		seen[pkg.key()] = struct{}{}
	}
	today := now.In(cycleEndTimeZone)
	reward := int64(0)
	found := false
	for _, pkg := range after.packages {
		if _, exists := seen[pkg.key()]; exists {
			continue
		}
		if pkg.hasCycleStart {
			if sameBillingDay(pkg.cycleStart, today) {
				reward += pkg.remain
				found = true
			}
			continue
		}
		if pkg.hasID {
			reward += pkg.remain
			found = true
		}
	}
	return float64(reward), found
}

// todayGrantPackages 报告今天已发放的额度：一个 CycleStartTime 是今天的既有套餐。
// 没有 CycleStartTime 的套餐无法证明当日发放，因此被忽略——我们不猜「already」。
func todayGrantPackages(snapshot activityCreditSnapshot, now time.Time) (float64, bool) {
	today := now.In(cycleEndTimeZone)
	reward := int64(0)
	found := false
	for _, pkg := range snapshot.packages {
		if pkg.hasCycleStart && sameBillingDay(pkg.cycleStart, today) {
			reward += pkg.remain
			found = true
		}
	}
	return float64(reward), found
}

func sameBillingDay(candidate, reference time.Time) bool {
	c := candidate.In(cycleEndTimeZone)
	r := reference.In(cycleEndTimeZone)
	return c.Year() == r.Year() && c.YearDay() == r.YearDay()
}

func parseBillingTime(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", trimmed, cycleEndTimeZone); err == nil {
		return parsed, true
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func activitySnapshotNote(snapshot activityCreditSnapshot, ok bool) string {
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("remain=%d %s", snapshot.remain, packageInventory(snapshot.packages))
}

// packageInventory 渲染套餐的经过净化处理的字段名 + 值清单。它只记录键名、时间戳
// 和数字——绝不记录 token 或完整响应体——以便首次实时运行能确认我们的字段名是否与
// 实时响应匹配。
func packageInventory(packages []activityPackage) string {
	parts := make([]string, 0, len(packages))
	for _, pkg := range packages {
		id := "id=absent"
		if pkg.hasID {
			id = fmt.Sprintf("id(len=%d)", len(pkg.id))
		}
		start := ""
		if pkg.hasCycleStart {
			start = pkg.cycleStart.Format("2006-01-02 15:04:05")
		}
		parts = append(parts, fmt.Sprintf("{fields=%s %s cycleStart=%q cycleEnd=%q createMS=%d remain=%d size=%d}",
			strings.Join(pkg.keyNames, "|"), id, start, pkg.cycleEnd, pkg.createMS, pkg.remain, pkg.size))
	}
	return "packages=[" + strings.Join(parts, " ") + "]"
}

type activityTurnResult struct {
	status  string
	chunks  int64
	events  int64
	elapsed time.Duration
}

// activityRunTurn 接入沙箱并驱动一轮 ACP 直到完成。完成与否通过控制台状态观察
// （SSE 流在后台被排空，其 chunk 数计入报告）。
func (c *Client) activityRunTurn(ctx context.Context, client *http.Client, credential Credential, conversation string, sandbox dailyActivitySandbox) (activityTurnResult, error) {
	started := time.Now()
	channel := &activityChannel{
		client:    client,
		link:      sandbox.link,
		token:     sandbox.token,
		userAgent: webAgentUserAgent,
	}
	if err := channel.open(ctx); err != nil {
		return activityTurnResult{}, err
	}
	defer channel.close()

	params := []struct {
		method string
		params map[string]any
	}{
		{"initialize", map[string]any{
			"protocolVersion": acpProtocolVersion,
			"clientCapabilities": map[string]any{
				"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
				"terminal": false,
			},
		}},
		{"session/load", map[string]any{
			"sessionId":  sandbox.sessionID,
			"cwd":        sandbox.cwd,
			"mcpServers": []any{},
		}},
		{"session/prompt", map[string]any{
			"sessionId": sandbox.sessionID,
			"prompt":    []map[string]string{{"type": "text", "text": dailyActivityPrompt}},
		}},
	}
	for index, call := range params {
		if err := channel.request(ctx, call.method, call.params, index+1); err != nil {
			return activityTurnResult{}, err
		}
	}

	// 推导出本轮截止时间，使其严格落在调用方预算之内，为握手与额度读取预留空间。
	// 这样即便调用方缩小了自己的预算，该不变量依然成立。
	wait := activityTurnTimeout
	if wait > activityTurnCap {
		wait = activityTurnCap
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline) - activityHandshakeReserve
		if remaining < wait {
			wait = remaining
		}
	}
	if wait <= 0 {
		return activityTurnResult{}, activityError(ActivityProtocol, "run turn",
			"insufficient check-in budget for the ACP turn", nil)
	}
	turnDeadline := time.Now().Add(wait)
	interval := activityPollInterval
	if interval <= 0 {
		interval = time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()

	lastStatus := ""
	for {
		if status, err := c.activityConversationStatus(ctx, client, credential, conversation); err == nil {
			lastStatus = status
		}
		switch lastStatus {
		case "completed":
			return activityTurnResult{
				status:  lastStatus,
				chunks:  channel.chunks.Load(),
				events:  channel.updates.Load(),
				elapsed: time.Since(started),
			}, nil
		case "failed", "error":
			return activityTurnResult{}, activityError(ActivityUpstream, "run turn",
				"conversation status="+lastStatus, nil)
		}
		if !time.Now().Before(turnDeadline) {
			return activityTurnResult{}, activityError(ActivityUpstream, "run turn",
				fmt.Sprintf("conversation did not complete within %s (status=%s)", wait, orUnknown(lastStatus)), nil)
		}
		select {
		case <-ctx.Done():
			return activityTurnResult{}, activityError(ActivityNetwork, "run turn",
				"context canceled: "+ctx.Err().Error(), ctx.Err())
		case <-timer.C:
			timer.Reset(interval)
		}
	}
}

// activityJSON 执行一次控制台 JSON 请求并返回 status + body。
func (c *Client) activityJSON(ctx context.Context, client *http.Client, credential Credential, method, rawURL string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return 0, nil, activityError(ActivityProtocol, strings.ToLower(method)+" "+rawURL, err.Error(), err)
	}
	setWebAgentHeaders(req.Header, credential)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, activityError(ActivityNetwork, strings.ToLower(method)+" "+rawURL, err.Error(), err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, activityError(ActivityNetwork, strings.ToLower(method)+" "+rawURL, err.Error(), err)
	}
	return resp.StatusCode, payload, nil
}

// setWebAgentHeaders 镜像 Web 控制台应用的外发请求头：只有 bearer token 与
// X-User-Id（没有桌面端的 X-IDE-* 指纹）。
func setWebAgentHeaders(header http.Header, credential Credential) {
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json, text/plain, */*")
	header.Set("Origin", webAgentOrigin)
	header.Set("Referer", webAgentOrigin+"/app")
	header.Set("User-Agent", webAgentUserAgent)
	if credential.AccessToken != "" {
		header.Set("Authorization", "Bearer "+credential.AccessToken)
	}
	if credential.UID != "" {
		header.Set("X-User-Id", credential.UID)
	}
}

// dailyActivityAlreadyMessage 识别明确表示每日奖励已领取的上游业务消息。它只是一个
// 文本信号；主要的「already」路径是当日发放套餐检查。
func dailyActivityAlreadyMessage(body []byte) (string, bool) {
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &env) == nil {
		if strings.TrimSpace(env.Msg) != "" {
			msg = strings.TrimSpace(env.Msg)
		} else if env.Code == 0 {
			return "", false
		}
	}
	lower := strings.ToLower(msg)
	for _, needle := range []string{"已领取", "已经领取", "已领过", "今日已领", "已打卡"} {
		if strings.Contains(msg, needle) {
			return msg, true
		}
	}
	if strings.Contains(lower, "already") &&
		(strings.Contains(lower, "claim") || strings.Contains(lower, "reward") ||
			strings.Contains(lower, "activity") || strings.Contains(lower, "today")) {
		return msg, true
	}
	if strings.Contains(lower, "daily limit") || strings.Contains(lower, "limit reached") {
		return msg, true
	}
	return "", false
}

func truncateBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 200 {
		return text[:200] + "..."
	}
	return text
}

func orUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

// activityChannel 是 ACP 的 streamable-HTTP 传输：一条 SSE 流承载通知，
// POST 承载 JSON-RPC 请求。
type activityChannel struct {
	client       *http.Client
	link         string
	token        string
	userAgent    string
	connectionID string

	body    io.ReadCloser
	done    chan struct{}
	updates atomic.Int64
	chunks  atomic.Int64
}

// open 建立 SSE 流并捕获 Acp-Connection-Id 头。
func (ch *activityChannel) open(ctx context.Context) error {
	if _, err := url.Parse(ch.link); err != nil {
		return activityError(ActivityProtocol, "sse open", "invalid sandbox link: "+ch.link, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ch.link, nil)
	if err != nil {
		return activityError(ActivityProtocol, "sse open", err.Error(), err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+ch.token)
	req.Header.Set("User-Agent", ch.userAgent)
	resp, err := ch.client.Do(req)
	if err != nil {
		return activityError(ActivityNetwork, "sse open", err.Error(), err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return activityError(ActivityProtocol, "sse open", fmt.Sprintf("SSE channel returned HTTP %d", resp.StatusCode), nil)
	}
	connectionID := strings.TrimSpace(resp.Header.Get("Acp-Connection-Id"))
	if connectionID == "" {
		resp.Body.Close()
		return activityError(ActivityProtocol, "sse open", "SSE channel did not return Acp-Connection-Id", nil)
	}
	ch.connectionID = connectionID
	ch.body = resp.Body
	ch.done = make(chan struct{})
	go ch.readLoop(resp.Body)
	return nil
}

// request 通过 ACP 通道发送一个 JSON-RPC 请求。
func (ch *activityChannel) request(ctx context.Context, method string, params map[string]any, id int) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return activityError(ActivityProtocol, method, err.Error(), err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ch.link, bytes.NewReader(body))
	if err != nil {
		return activityError(ActivityProtocol, method, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Acp-Connection-Id", ch.connectionID)
	req.Header.Set("Authorization", "Bearer "+ch.token)
	req.Header.Set("User-Agent", ch.userAgent)
	resp, err := ch.client.Do(req)
	if err != nil {
		return activityError(ActivityNetwork, method, err.Error(), err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return activityError(ActivityUpstream, method, fmt.Sprintf("returned HTTP %d", resp.StatusCode), nil)
	}
	return nil
}

func (ch *activityChannel) readLoop(body io.Reader) {
	defer close(ch.done)
	reader := bufio.NewReaderSize(body, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			ch.consumeLine(line)
		}
		if err != nil {
			return
		}
	}
}

func (ch *activityChannel) consumeLine(line string) {
	text := strings.TrimSpace(line)
	if !strings.HasPrefix(text, "data:") {
		return
	}
	payload := strings.TrimSpace(text[len("data:"):])
	if payload == "" || payload == "[DONE]" {
		return
	}
	var message struct {
		Method string `json:"method"`
		Params struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
			} `json:"update"`
		} `json:"params"`
	}
	if json.Unmarshal([]byte(payload), &message) != nil {
		return
	}
	if message.Method != "session/update" {
		return
	}
	ch.updates.Add(1)
	if message.Params.Update.SessionUpdate == "agent_message_chunk" {
		ch.chunks.Add(1)
	}
}

func (ch *activityChannel) close() {
	if ch.body != nil {
		_ = ch.body.Close()
	}
	if ch.done != nil {
		select {
		case <-ch.done:
		case <-time.After(time.Second):
		}
	}
}
