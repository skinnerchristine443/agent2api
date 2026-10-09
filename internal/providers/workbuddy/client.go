package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/providers/credfresh"
	proxyutil "agent2api/internal/proxy"
	"agent2api/internal/translate"
)

// Store 是适配器所需的持久化接口。它与 SQLite store 匹配，
// 但无需导入具体类型。
type Store interface {
	Get(ctx context.Context, id string) (accounts.Account, error)
	LoadCredentialPayload(ctx context.Context, accountID string) (string, []byte, error)
	SaveCredentialPayload(ctx context.Context, accountID, format string, payload []byte) error
	Observe(ctx context.Context, id, remoteUID, status, lastError, lastKind string) error
}

// SecretReader 是可选的。缺失它意味着没有全局代理，而非错误。
type SecretReader interface {
	GetSecret(context.Context, string) (string, bool, error)
}

// ModelSettingReader 是可选的。缺失它意味着跳过控制台保存的推理
// 默认值，与之前的匿名类型断言行为一致。
type ModelSettingReader interface {
	GetProviderModelSetting(context.Context, string, string) (accounts.ProviderModelSetting, error)
}

type Client struct {
	store Store
	http  *http.Client

	transports proxyutil.TransportCache

	mu          sync.Mutex
	loginStates map[string]string
	catalog     map[string]providers.ModelInfo

	// refreshes 按账号合并 token 刷新；见 credential()。
	refreshes credfresh.Group[Credential]
}

const catalogTimeout = 15 * time.Second

// 每日签到的重试阶梯，按失败类别分别维护，这样耗尽某一类不会挤占另一类。
// 定时签到最常碰到的是网络尚未就绪（刚开机或唤醒时 Wi-Fi、DHCP 和 VPN
// 还在稳定过程中），因此网络阶梯以分钟为粒度，取代旧的一秒级两次重试——
// 那只会直接撞上同一堵墙。5xx 属于服务端抖动，只配一次短重试。整条阶梯
// 仍须落在 providers.CheckinRequestBudget 之内（它给调用设上限），所以它
// 刻意停在了长任务才负担得起的分钟级阶梯之前。
var (
	checkinNetworkRetryDelays    = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}
	checkinServerRetryDelays     = []time.Duration{3 * time.Second, 10 * time.Second}
	checkinProcessingRetryDelays = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}
)

func NewClient(store Store) *Client {
	return &Client{
		store: store,
		http: &http.Client{
			Timeout: 120 * time.Second,
			// 当路径是控制台页面时，Catalog 与插件 API 会 302 到 OIDC HTML。
			// 跟随该重定向会把 302 变成一份登录文档，从而掩盖真实状态。
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		loginStates: map[string]string{},
	}
}

type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (c *Client) globalProxy(ctx context.Context) (string, error) {
	store, ok := c.store.(SecretReader)
	if !ok {
		return "", nil
	}

	value, found, err := store.GetSecret(ctx, "proxy_url")
	if err != nil {
		return "", fmt.Errorf("load global proxy setting: %w", err)
	}
	if !found {
		return "", nil
	}
	return strings.TrimSpace(value), nil
}

func (c *Client) effectiveProxy(ctx context.Context, accountID string) (string, error) {
	account, err := c.store.Get(ctx, accountID)
	if err != nil {
		return "", err
	}

	if value := strings.TrimSpace(account.ProxyURL); value != "" {
		return value, nil
	}

	return c.globalProxy(ctx)
}

func (c *Client) httpClient(ctx context.Context, accountID string) (*http.Client, error) {
	rawProxy, err := c.effectiveProxy(ctx, accountID)
	if err != nil {
		return nil, err
	}

	client := *c.http

	transport, err := c.transports.Get(rawProxy)
	if err != nil {
		return nil, err
	}
	if transport != nil {
		client.Transport = transport
	}

	return &client, nil
}

func (c *Client) do(ctx context.Context, accountID, method, rawURL string, body []byte, setHeaders func(http.Header)) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, 0, err
	}
	if setHeaders != nil {
		setHeaders(req.Header)
	}
	client, err := c.httpClient(ctx, accountID)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return payload, resp.StatusCode, nil
}

// loginRealm 依据账号区域解析登录所用的 realm，
// 当区域未设置或未知时回退到 CN 默认值。
func (c *Client) loginRealm(ctx context.Context, accountID string) Realm {
	if account, err := c.store.Get(ctx, accountID); err == nil {
		if realm, ok := workbuddyRealmForRegion(account.ProviderRegion); ok {
			return realm
		}
	}
	return RealmCN
}

// StartLogin 请求服务端下发的 state 并返回浏览器 URL。
func (c *Client) StartLogin(ctx context.Context, accountID string) (providers.LoginSession, error) {
	realm := c.loginRealm(ctx, accountID)
	base := chatBaseForRealm(realm)
	body, status, err := c.do(ctx, accountID, http.MethodPost, base+pathAuthState+"?platform=CLI", []byte("{}"),
		func(h http.Header) { setCommonHeaders(h, realm) })
	if err != nil {
		return providers.LoginSession{}, err
	}
	if status >= 300 {
		return providers.LoginSession{}, fmt.Errorf("auth state status=%d: %s", status, string(body))
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil || env.Code != 0 {
		return providers.LoginSession{}, fmt.Errorf("auth state failed: code=%d msg=%s", env.Code, env.Msg)
	}
	var data struct {
		State   string `json:"state"`
		AuthURL string `json:"authUrl"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || data.State == "" {
		return providers.LoginSession{}, fmt.Errorf("auth state response missing state")
	}
	c.mu.Lock()
	c.loginStates[accountID] = data.State
	c.mu.Unlock()
	return providers.LoginSession{AuthURL: data.AuthURL, State: data.State}, nil
}

// PollLogin 用 state 换取 token 与账号身份，然后保存
// 规范化的凭据载荷。
func (c *Client) PollLogin(ctx context.Context, accountID string) (bool, string, error) {
	c.mu.Lock()
	state := c.loginStates[accountID]
	c.mu.Unlock()
	if state == "" {
		return false, "", fmt.Errorf("login not started for account %s", accountID)
	}
	realm := c.loginRealm(ctx, accountID)
	base := chatBaseForRealm(realm)
	tokenBody, status, err := c.do(ctx, accountID, http.MethodGet, base+pathAuthToken+"?state="+url.QueryEscape(state), nil,
		func(h http.Header) { setCommonHeaders(h, realm) })
	if err != nil {
		return false, "", err
	}
	var tokenEnv envelope
	_ = json.Unmarshal(tokenBody, &tokenEnv)
	if status >= 500 {
		return false, "", fmt.Errorf("token endpoint status=%d", status)
	}
	if status >= 300 || tokenEnv.Code != 0 {
		return false, "waiting for authorization", nil
	}
	var token struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if err := json.Unmarshal(tokenEnv.Data, &token); err != nil || token.AccessToken == "" {
		return false, "waiting for authorization", nil
	}
	accountBody, _, err := c.do(ctx, accountID, http.MethodGet, base+pathAuthAccount+"?state="+url.QueryEscape(state), nil,
		func(h http.Header) {
			setCommonHeaders(h, realm)
			h.Set("Authorization", "Bearer "+token.AccessToken)
		})
	if err != nil {
		return false, "", err
	}
	var accountEnv envelope
	var identity struct {
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterpriseId"`
		Nickname     string `json:"nickname"`
	}
	if json.Unmarshal(accountBody, &accountEnv) == nil && accountEnv.Code == 0 {
		_ = json.Unmarshal(accountEnv.Data, &identity)
	}
	domain := token.Domain
	if domain == "" || (base == ChatBaseGlobal && !strings.Contains(strings.ToLower(domain), DomainGlobal) && !strings.Contains(strings.ToLower(domain), "workbuddy")) {
		if base == ChatBaseGlobal {
			domain = DomainGlobal
		} else if domain == "" {
			domain = DomainCN
		}
	}
	credential := Credential{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix(),
		Domain:       domain,
		UID:          identity.UID,
		EnterpriseID: identity.EnterpriseID,
		Nickname:     identity.Nickname,
	}
	payload, err := credential.Encode()
	if err != nil {
		return false, "", err
	}
	if err := c.store.SaveCredentialPayload(ctx, accountID, CredentialFormat, payload); err != nil {
		return false, "", err
	}
	_ = c.store.Observe(ctx, accountID, identity.UID, "ready", "", "")
	c.mu.Lock()
	delete(c.loginStates, accountID)
	c.mu.Unlock()
	return true, "login complete", nil
}

// credentialRefreshTimeout 限定单次共享刷新的时长。刷新是脱离发起它的调用方
// 独立运行的，因此需要自己的截止时间。
const credentialRefreshTimeout = 60 * time.Second

// credential 加载存储的 token，并在临近过期时刷新它。
//
// 刷新按账号合并：该端点会轮换 refresh token，因此 N 个并发刷新会持久化 N 个
// 互不相同的 token，而后面的写入会使已经发出去的那些失效。回写本身是一次
// 比较并写入（见 persistRefreshed）。
func (c *Client) credential(ctx context.Context, accountID string) (Credential, error) {
	credential, err := c.loadCredential(ctx, accountID)
	if err != nil {
		return Credential{}, err
	}
	if !credentialExpiring(credential, time.Now()) {
		return credential, nil
	}
	refreshed, err := c.refreshes.Do(ctx, accountID, credentialRefreshTimeout, func(runCtx context.Context) (Credential, error) {
		base := credential
		if current, loadErr := c.loadCredential(runCtx, accountID); loadErr == nil {
			// 刚刚完成的刷新使这次刷新变得多余。
			if !credentialExpiring(current, time.Now()) {
				return current, nil
			}
			base = current
		}
		return c.Refresh(runCtx, accountID, base)
	})
	if err != nil {
		// 刷新失败不得让请求失败：由调用方暴露上游的拒绝，
		// 账号分类归账号管理器负责。
		return credential, nil
	}
	return refreshed, nil
}

func (c *Client) loadCredential(ctx context.Context, accountID string) (Credential, error) {
	_, payload, err := c.store.LoadCredentialPayload(ctx, accountID)
	if err != nil {
		return Credential{}, err
	}
	return DecodeCredential(payload)
}

// credentialExpiring 报告 token 是否已足够接近过期、值得刷新；
// ExpiresAt == 0 表示上游未给出过期时间。
func credentialExpiring(credential Credential, now time.Time) bool {
	if credential.ExpiresAt == 0 {
		return true
	}
	return now.Add(2*time.Minute).Unix() >= credential.ExpiresAt
}

// persistRefreshed 仅在存储的载荷仍是本次刷新所基于的那一份时，才写入刷新后的
// 凭据。该端点会轮换 refresh token，因此竞争失败的刷新不得覆盖胜出者的 token——
// 那样会让账号持有一个无效 token。
//
// 不具备可选比较并写入能力的 Store 回退为无条件保存。
func (c *Client) persistRefreshed(ctx context.Context, accountID string, base Credential, payload []byte) error {
	versioned, ok := c.store.(credfresh.VersionedStore)
	if !ok {
		return c.store.SaveCredentialPayload(ctx, accountID, CredentialFormat, payload)
	}
	_, storedPayload, version, err := versioned.LoadCredentialPayloadWithVersion(ctx, accountID)
	if err != nil {
		return err
	}
	if stored, decodeErr := DecodeCredential(storedPayload); decodeErr == nil {
		if stored.AccessToken != base.AccessToken || stored.RefreshToken != base.RefreshToken {
			// 本次刷新运行期间有更新的写入落地了；我们这份已过期。
			return nil
		}
	}
	_, err = versioned.SaveCredentialPayloadIfUnchanged(ctx, accountID, CredentialFormat, payload, version)
	return err
}

func (c *Client) overlayRegion(ctx context.Context, accountID string, credential Credential) Credential {
	account, err := c.store.Get(ctx, accountID)
	if err != nil {
		return credential
	}
	// 当控制面区域指明了一个已知 realm，且与凭据中存储的 domain 不一致时，
	// 以控制面区域为准。
	if realm, ok := workbuddyRealmForRegion(account.ProviderRegion); ok && realm != credential.Realm() {
		credential.Domain = domainForRealm(realm)
	}
	return credential
}

func (c *Client) resolvedCredential(ctx context.Context, accountID string) (Credential, error) {
	credential, err := c.credential(ctx, accountID)
	if err != nil {
		return Credential{}, err
	}
	return c.overlayRegion(ctx, accountID, credential), nil
}

// Refresh 用 refresh token 换取新 token。会话失效类错误通过向账号管理器暴露
// 认证分类来停用该账号。
//
// 单次「会话失效」拒绝本身并不可信：它可能描述的是一份已被更新凭据取代的
// token（另一个实例或官方客户端在本次刷新运行期间轮换了它），也可能只是上游的
// 瞬时抖动。该拒绝会被复核一次——检查存储的凭据是否有更新的写入，再给刷新一次
// 尝试——只有重复出现拒绝才把账号标记为需要重新登录。
func (c *Client) Refresh(ctx context.Context, accountID string, credential Credential) (Credential, error) {
	credential = c.overlayRegion(ctx, accountID, credential)
	base := credential
	refreshed, err := c.refreshAttempt(ctx, accountID, base)
	if err == nil {
		return refreshed, nil
	}
	if !errors.Is(err, errSessionDead) {
		return credential, err
	}
	if c.credentialSuperseded(ctx, accountID, base) {
		return credential, fmt.Errorf("workbuddy refresh raced with a newer stored credential")
	}
	refreshed, retryErr := c.refreshAttempt(ctx, accountID, base)
	if retryErr == nil {
		return refreshed, nil
	}
	if !errors.Is(retryErr, errSessionDead) {
		return credential, retryErr
	}
	if c.credentialSuperseded(ctx, accountID, base) {
		return credential, fmt.Errorf("workbuddy refresh raced with a newer stored credential")
	}
	_ = c.store.Observe(ctx, accountID, credential.UID, "login_required", "session dead; re-login required", accounts.KindAuth)
	return credential, fmt.Errorf("workbuddy session dead: re-login required")
}

// errSessionDead 标记一次被上游以会话失效契约拒绝的刷新，
// 以便调用方在宣布账号失效前先复核。
var errSessionDead = errors.New("workbuddy session dead")

// credentialSuperseded 报告存储的凭据是否在本次刷新运行期间发生了变化
// （这是比较并写入竞争，而非会话失效）。
func (c *Client) credentialSuperseded(ctx context.Context, accountID string, base Credential) bool {
	current, err := c.loadCredential(ctx, accountID)
	if err != nil {
		return false
	}
	return current.AccessToken != base.AccessToken || current.RefreshToken != base.RefreshToken
}

// refreshAttempt 针对 base 凭据执行一次刷新交换。
func (c *Client) refreshAttempt(ctx context.Context, accountID string, credential Credential) (Credential, error) {
	base := credential
	body, status, err := c.do(ctx, accountID, http.MethodPost, credential.ChatBase()+pathTokenRefresh, []byte("{}"),
		func(h http.Header) { SetRefreshHeaders(h, credential) })
	if err != nil {
		return credential, err
	}
	classified := Classify(status, string(body))
	if classified.Kind == accounts.KindAuth {
		return credential, errSessionDead
	}
	if status >= 300 || classified.Kind != "" {
		return credential, fmt.Errorf("refresh status=%d kind=%s", status, classified.Kind)
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil || env.Code != 0 {
		return credential, fmt.Errorf("refresh envelope code=%d msg=%s", env.Code, env.Msg)
	}
	var data struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || data.AccessToken == "" {
		return credential, fmt.Errorf("refresh response missing accessToken")
	}
	if data.RefreshToken != "" {
		credential.RefreshToken = data.RefreshToken
	}
	if data.Domain != "" {
		credential.Domain = data.Domain
	}
	if data.ExpiresIn > 0 {
		credential.ExpiresAt = time.Now().Add(time.Duration(data.ExpiresIn) * time.Second).Unix()
	}
	credential.AccessToken = data.AccessToken
	credential = c.overlayRegion(ctx, accountID, credential)
	payload, err := credential.Encode()
	if err != nil {
		return credential, err
	}
	if err := c.persistRefreshed(ctx, accountID, base, payload); err != nil {
		return credential, err
	}
	return credential, nil
}

// Models 获取与 IDE 对齐的产品配置目录（/v3/config）。失败即显式报错；
// 不存在静态兜底列表。这里有意对齐 WorkBuddy 桌面端下拉框的数据源，
// 而非 /v2/enterprises/personal/models，后者可能遗漏 IDE 可见的 id。
func (c *Client) Models(ctx context.Context, accountID string) ([]providers.ModelInfo, error) {
	credential, err := c.resolvedCredential(ctx, accountID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	body, status, err := c.do(ctx, accountID, http.MethodGet, credential.ChatBase()+credential.productConfigPath(), nil,
		func(h http.Header) { SetCatalogHeaders(h, credential) })
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, fmt.Errorf("models status=%d: %s", status, catalogErrorBody(body))
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Models []catalogModelEntry `json:"models"`
			Agents []struct {
				Name   string   `json:"name"`
				Models []string `json:"models"`
			} `json:"agents"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("models parse: %s", catalogErrorBody(body))
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("models envelope code=%d msg=%s", env.Code, env.Msg)
	}
	if env.Data.Models == nil {
		return nil, fmt.Errorf("workbuddy product config returned no models")
	}
	// IDE 下拉框把产品配置里的模型与同一份 /v3/config 载荷中的 CLI agent
	// 白名单求交集。若 agents 缺失，则保留所有启用的模型。不要臆造公开别名。
	cliModels := map[string]struct{}{}
	for _, agent := range env.Data.Agents {
		if !isCLIAgent(agent.Name) {
			continue
		}
		for _, id := range agent.Models {
			cliModels[id] = struct{}{}
		}
	}
	filterCLI := len(cliModels) > 0
	var out []providers.ModelInfo
	for _, model := range env.Data.Models {
		if model.Disabled {
			continue
		}
		if filterCLI {
			if _, ok := cliModels[model.ID]; !ok {
				continue
			}
		}
		out = append(out, catalogModel(model))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("workbuddy product config returned no models")
	}
	c.rememberCatalog(out)
	return out, nil
}

func (c *Client) chatRequest(ctx context.Context, accountID string, credential Credential, req translate.ChatRequest) (*http.Request, providers.ResolvedChat, error) {
	caps := c.capsFor(req.Model)
	storedLevel := ""
	if setter, ok := c.store.(ModelSettingReader); ok {
		// CanonicalModelID 必须与 control.ModelContextKey 一致，
		// 这样在对话时才能找到控制台保存的推理等级。
		if stored, err := setter.GetProviderModelSetting(ctx, "workbuddy", accounts.CanonicalModelID(req.Model)); err == nil {
			storedLevel = stored.ReasoningEffort
		}
	}
	// 在解析上游模型 id 与推理能力之前，先预热实时目录。目录条目是权威的；
	// 当请求的 id 缺失时，我们不臆造别名。
	if (!c.hasCatalogEntry(req.Model) || len(caps.ReasoningOptions) == 0) && accountID != "" {
		_, _ = c.Models(ctx, accountID)
		caps = c.capsFor(req.Model)
	}
	body := map[string]any{
		"model":       c.upstreamModelID(req.Model),
		"messages":    req.Messages,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
		"tools":       req.Tools,
		"tool_choice": req.ToolChoice,
	}
	// prompt_cache_key：客户端已带则保留，否则按「账号 + 会话」注入稳定键
	// （上游前缀缓存费用优化；跨账号绝不共用——见 cachekey.go）。
	applyPromptCacheKey(body, req.PromptCacheKey, credential.UID, providers.SessionKeyFromContext(ctx))
	resolved := providers.ResolvedChat{ReasoningLevel: applyChatReasoning(body, req, storedLevel, caps)}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		credential.ChatBase()+pathChat, bytes.NewReader(PrepareBody(payload)))
	if err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	SetChatHeaders(httpReq.Header, credential)
	return httpReq, resolved, nil
}

func (c *Client) ChatNonStream(ctx context.Context, accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
	credential, err := c.resolvedCredential(ctx, accountID)
	if err != nil {
		return providers.ChatOutcome{}, err
	}
	httpReq, resolved, err := c.chatRequest(ctx, accountID, credential, req)
	if err != nil {
		return providers.ChatOutcome{}, err
	}
	client, err := c.httpClient(ctx, accountID)
	if err != nil {
		return providers.ChatOutcome{}, err
	}
	client.Timeout = 0
	resp, err := client.Do(httpReq)
	if err != nil {
		return providers.ChatOutcome{}, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if readErr != nil {
		return providers.ChatOutcome{}, fmt.Errorf("read workbuddy stream: %w", readErr)
	}
	if resp.StatusCode >= 300 {
		return providers.ChatOutcome{}, classifiedErrorWithHeader(resp.StatusCode, body, resp.Header)
	}
	aggregate, err := Aggregate(bytes.NewReader(body))
	if err != nil {
		// 一个实为单个错误帧的 200 响应体无法满足流式语法；
		// 应对该帧进行分类，而不是报告一个匿名的截断。
		if frameErr := chatErrorFrame(body); frameErr != nil {
			return providers.ChatOutcome{}, frameErr
		}
		return providers.ChatOutcome{}, err
	}
	outcome, err := outcomeFromAggregate(aggregate)
	if err != nil {
		return providers.ChatOutcome{}, err
	}
	if outcome.Content == "" && outcome.Reasoning == "" && len(outcome.ToolCalls) == 0 {
		// 空的补全可能藏着被聚合器跳过的响应体中段错误帧；
		// 应把它暴露为失败，而不是静默的空成功。
		if frameErr := chatErrorFrame(body); frameErr != nil {
			return providers.ChatOutcome{}, frameErr
		}
	}
	outcome.ReasoningLevel = resolved.ReasoningLevel
	return outcome, nil
}

func (c *Client) ChatStream(ctx context.Context, accountID string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	credential, err := c.resolvedCredential(ctx, accountID)
	if err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	httpReq, resolved, err := c.chatRequest(ctx, accountID, credential, req)
	if err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	client, err := c.httpClient(ctx, accountID)
	if err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	client.Timeout = 0
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return nil, providers.ResolvedChat{}, classifiedErrorWithHeader(resp.StatusCode, body, resp.Header)
	}
	return rewriteChatStream(resp), resolved, nil
}

func outcomeFromAggregate(aggregate map[string]any) (providers.ChatOutcome, error) {
	raw, err := json.Marshal(aggregate)
	if err != nil {
		return providers.ChatOutcome{}, err
	}
	var parsed struct {
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int      `json:"prompt_tokens"`
			CompletionTokens int      `json:"completion_tokens"`
			CacheReadTokens  *int     `json:"cache_read_tokens"`
			CacheWriteTokens *int     `json:"cache_write_tokens"`
			Source           string   `json:"source"`
			Credit           *float64 `json:"credit"`
			PromptDetails    struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          string          `json:"content"`
				ReasoningContent string          `json:"reasoning_content"`
				ToolCalls        json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return providers.ChatOutcome{}, err
	}
	out := providers.ChatOutcome{UsageSource: "upstream", FinishReason: "stop"}
	if len(parsed.Choices) > 0 {
		out.Content = parsed.Choices[0].Message.Content
		out.Reasoning = parsed.Choices[0].Message.ReasoningContent
		out.ToolCalls = parsed.Choices[0].Message.ToolCalls
		if parsed.Choices[0].FinishReason != "" {
			out.FinishReason = parsed.Choices[0].FinishReason
		}
	}
	out.Model = parsed.Model
	out.PromptTokens = parsed.Usage.PromptTokens
	out.CompletionTokens = parsed.Usage.CompletionTokens
	out.CacheReadTokens = parsed.Usage.CacheReadTokens
	if out.CacheReadTokens == nil {
		out.CacheReadTokens = parsed.Usage.PromptDetails.CachedTokens
	}
	out.CacheWriteTokens = parsed.Usage.CacheWriteTokens
	out.Credits = parsed.Usage.Credit
	return out, nil
}

// classifiedErrorWithHeader 把非 2xx 的响应体转换为分类错误，同时也顾及
// 仅通过响应头（Retry-After 之类）告知何时重试的上游。响应体自身声明的重置
// 时刻优先——那是上游自己的答案，而非估算。header 对没有响应的调用方可以为 nil。
func classifiedErrorWithHeader(status int, body []byte, header http.Header) error {
	classified := Classify(status, string(body))
	out := &providers.Error{Kind: classified.Kind, Status: classified.Status, Message: classified.Message}
	if classified.Kind == accounts.KindRateLimit {
		if reset := parseQuotaReset(string(body), time.Now()); reset > 0 {
			out.RetryAfter = reset
		}
		if out.RetryAfter <= 0 {
			out.RetryAfter = providers.RetryAfterFromHeaders(header, time.Now())
		}
	}
	return out
}

// parseQuotaReset 提取 WorkBuddy 在其用量限制消息中内嵌的绝对重置时间，例如
//
//	{"code":6004,"msg":"您的使用量已超出频率限制，将在 2026-09-01 13:56:47 UTC+8 重置"}
//
// 信封判定保持为 provider 私有（6004 属于 WorkBuddy）；时间戳语法本身与分类
// 表共用（providers.ParseResetTimestampCN），因此两条路径不会漂移。没有重置
// 时间时返回 0，留给调用方的兜底逻辑。
func parseQuotaReset(body string, now time.Time) time.Duration {
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || env.Code != rateLimitCode {
		return 0
	}
	return providers.ParseResetTimestampCN(env.Msg, now)
}

// ClassifiedError 是 providers.Error 的历史 WorkBuddy 别名。
type ClassifiedError = providers.Error

// Classify 把 WorkBuddy 的 HTTP/错误响应体映射到内部分类体系。
//
// 信封中精确的业务码优先于状态码/文本启发式：数字码是精确的，而下面的兜底探测
// 是对原始响应体做子串匹配——数字片段可能藏在另一个数字里，而基于状态码的分支
// （比如一个裸 400）可能遮蔽掉自身带有码的配额拒绝。对于没有可解码信封的响应体，
// 启发式仍是兜底。
func Classify(status int, body string) providers.ClassifiedError {
	if env, ok := decodeEnvelope(body); ok {
		switch env.Code {
		case sessionDeadCode:
			return providers.ClassifiedError{Kind: accounts.KindAuth, Status: 401, Message: "session dead; re-login required"}
		case modelNotRegisteredCode:
			return providers.ClassifiedError{Kind: accounts.KindModelNotAvailable, Status: firstNonEmptyStatus(status, 404), Message: env.Msg}
		case missingSystemPromptCode, toolCallSequenceCode:
			return providers.ClassifiedError{Kind: accounts.KindInvalidRequest, Status: firstNonEmptyStatus(status, 400), Message: env.Msg}
		case quotaExhaustedCode:
			return providers.ClassifiedError{Kind: accounts.KindQuota, Status: 402, Message: env.Msg}
		case rateLimitCode:
			// 调用方会从响应体中解析出重置时刻；正是分类让该解析在 200 响应上仍然可达。
			return providers.ClassifiedError{Kind: accounts.KindRateLimit, Status: 429, Message: strings.TrimSpace(body)}
		}
	}
	text := strings.ToLower(body)
	if accounts.IsPromptLimitText(body) {
		return providers.ClassifiedError{Kind: accounts.KindInvalidRequest, Status: 400, Message: strings.TrimSpace(body)}
	}
	// 没有业务信封的 403 是边缘节点/WAF 拦截：它的作用域是来源而非账号，
	// 因此 HTTP 状态必须保留进分类（执行器据此做来源级快速失败）。
	bareForbidden := status == 403 && !hasEnvelopeCode(body)
	switch {
	case status == 402 || strings.Contains(text, "insufficient credit") ||
		strings.Contains(text, "quota exceeded") || strings.Contains(text, "积分不足") ||
		strings.Contains(text, "余额不足"):
		return providers.ClassifiedError{Kind: accounts.KindQuota, Status: 402, Message: strings.TrimSpace(body)}
	case status == 401 || sessionDeadLike(text) ||
		strings.Contains(text, fmt.Sprintf("%d", sessionDeadCode)):
		return providers.ClassifiedError{Kind: accounts.KindAuth, Status: 401, Message: "session dead; re-login required"}
	case status == 429 || strings.Contains(text, "soft_rate"):
		return providers.ClassifiedError{Kind: accounts.KindRateLimit, Status: 429, Message: strings.TrimSpace(body)}
	case status == 404:
		return providers.ClassifiedError{Kind: accounts.KindUnavailable, Status: 404, Message: strings.TrimSpace(body)}
	case strings.Contains(text, fmt.Sprintf("%d", modelNotRegisteredCode)):
		// 请求的模型未在该 realm/账号下注册：账号是健康的，
		// 因此这不得让它退出轮换。
		return providers.ClassifiedError{Kind: accounts.KindModelNotAvailable, Status: 404, Message: strings.TrimSpace(body)}
	case status == 400 || accounts.IsPromptLimitText(body) || accounts.IsInvalidRequestText(body) || isMissingSystemPrompt(body) || isBrokenToolSequence(body):
		// 请求级拒绝（内容审核、字段格式错误、缺失开头的 system 消息）：
		// 换另一个账号重试也无济于事，且账号是健康的。
		return providers.ClassifiedError{Kind: accounts.KindInvalidRequest, Status: firstNonEmptyStatus(status, 400), Message: strings.TrimSpace(body)}
	case status >= 500:
		return providers.ClassifiedError{Kind: accounts.KindUnavailable, Status: status, Message: strings.TrimSpace(body)}
	case bareForbidden:
		return providers.ClassifiedError{Kind: accounts.KindUnavailable, Status: 403, Message: strings.TrimSpace(body)}
	}
	var env envelope
	if json.Unmarshal([]byte(body), &env) == nil && env.Code != 0 {
		// code 不在已知表中的信封：回退到它的消息，否则保留历史上的 502。
		msg := strings.TrimSpace(env.Msg)
		if sessionDeadLike(msg) {
			return providers.ClassifiedError{Kind: accounts.KindAuth, Status: 401, Message: "session dead; re-login required"}
		}
		if accounts.IsPromptLimitText(msg) || accounts.IsInvalidRequestText(msg) || isMissingSystemPrompt(msg) || isBrokenToolSequence(msg) {
			return providers.ClassifiedError{Kind: accounts.KindInvalidRequest, Status: firstNonEmptyStatus(status, 400), Message: msg}
		}
		return providers.ClassifiedError{Kind: accounts.KindUnavailable, Status: 502, Message: env.Msg}
	}
	return providers.ClassifiedError{}
}

func firstNonEmptyStatus(status, fallback int) int {
	if status >= 400 {
		return status
	}
	return fallback
}

// decodeEnvelope 在响应体带有非零 code 时返回解码后的业务信封。
// 字符串类型的 code 会解码失败，从而有意回退到文本启发式。
func decodeEnvelope(body string) (envelope, bool) {
	var env envelope
	if json.Unmarshal([]byte(body), &env) != nil || env.Code == 0 {
		return envelope{}, false
	}
	return env, true
}

// hasEnvelopeCode 报告响应体是否可解析为带非零 code 的业务信封。
func hasEnvelopeCode(body string) bool {
	_, ok := decodeEnvelope(body)
	return ok
}

// sessionDeadLike 报告文本是否提及会话失效契约。有意区分大小写不敏感：
// 上游把它写作 "Offline user session not found"，而早前对一个小写化的
// 待查串做精确大小写比较是永远匹配不上的。
func sessionDeadLike(text string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(sessionDeadText))
}

func isMissingSystemPrompt(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, missingSystemPromptText) ||
		strings.Contains(lower, fmt.Sprintf("%d", missingSystemPromptCode))
}

func isBrokenToolSequence(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, toolCallSequenceText) ||
		strings.Contains(lower, "tool_call_sequence_broken") ||
		strings.Contains(lower, fmt.Sprintf("%d", toolCallSequenceCode))
}

func catalogErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(text), "<html") || strings.Contains(strings.ToLower(text), "<!doctype") {
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "502"), strings.Contains(lower, "bad gateway"):
			return "upstream html error page (bad gateway)"
		case strings.Contains(lower, "500"), strings.Contains(lower, "internal server error"):
			return "upstream html error page (internal server error)"
		case strings.Contains(lower, "openid-connect"), strings.Contains(lower, "auth/realms"):
			return "upstream html login redirect"
		default:
			return "upstream html error page"
		}
	}
	if len(text) > 240 {
		return text[:240]
	}
	return text
}

// Probe 报告存储的凭据是否可用。这里没有 WASM 热状态；
// Hot 与 Ready 一致，使账号控制台无需外部 /health 端点即可把已登录账号视为可用。
func (c *Client) Probe(ctx context.Context, accountID string) (providers.AccountHealth, error) {
	credential, err := c.resolvedCredential(ctx, accountID)
	if err != nil {
		return providers.AccountHealth{LastError: err.Error()}, nil
	}
	if !credential.Ready() {
		msg := "workbuddy credential incomplete; re-login required"
		return providers.AccountHealth{UID: credential.UID, LastError: msg}, nil
	}
	return providers.AccountHealth{
		Ready: true,
		Hot:   true,
		UID:   credential.UID,
	}, nil
}

// Quota 从计费计量 API 获取剩余额度。
// 失败返回错误，供调用方忽略而不翻转就绪状态。
func (c *Client) Quota(ctx context.Context, accountID string) (*providers.QuotaInfo, error) {
	credential, err := c.resolvedCredential(ctx, accountID)
	if err != nil {
		return nil, err
	}
	remain, used, total, packages, err := c.UserResource(ctx, accountID, credential)
	if err != nil {
		return nil, err
	}
	if total <= 0 && remain > 0 {
		total = remain
	}
	if used <= 0 && total > remain {
		used = total - remain
	}
	percentage := 0.0
	if total > 0 {
		percentage = (float64(used) / float64(total)) * 100
		if percentage < 0 {
			percentage = 0
		}
		if percentage > 100 {
			percentage = 100
		}
	}
	expiresAt, expiringRemain := soonestExpiry(packages)
	return &providers.QuotaInfo{
		Used:           float64(used),
		Total:          float64(total),
		Remaining:      float64(remain),
		Percentage:     percentage,
		Unit:           "credits",
		Exceeded:       total > 0 && remain <= 0,
		FetchedAt:      time.Now().UTC().Format(time.RFC3339),
		ProviderID:     "workbuddy",
		ExpiresAt:      expiresAt,
		ExpiringRemain: expiringRemain,
		Packages:       packages,
	}, nil
}

// AlreadyCheckedInError 是仅针对签到的未命中。调用方仍应刷新额度，
// 且不得写入对话冷却。
type AlreadyCheckedInError struct {
	Msg string
}

func (e AlreadyCheckedInError) Error() string {
	if strings.TrimSpace(e.Msg) == "" {
		return "workbuddy already checked in"
	}
	return e.Msg
}

func (AlreadyCheckedInError) AlreadyCheckedIn() bool { return true }

// DailyCheckin 领取上游的每日额度发放。请求体是字面量 {}。
// 业务的「已签到」返回 AlreadyCheckedInError；会话失效仅在此处记录日志，
// 不做 Observe(auth)——那条路径归 keepalive 负责。
func (c *Client) DailyCheckin(ctx context.Context, accountID string) (string, error) {
	credential, err := c.resolvedCredential(ctx, accountID)
	if err != nil {
		return "", err
	}
	var body []byte
	var status int
	var retrier checkinRetrier
	for {
		body, status, err = c.do(ctx, accountID, http.MethodPost, credential.BillingBase()+pathDailyCheckin, []byte("{}"),
			func(h http.Header) { SetBillingHeaders(h, credential) })
		wait, again := retrier.delay(err, status, body)
		if !again || !sleepContext(ctx, wait) {
			break
		}
	}
	text := strings.TrimSpace(string(body))
	classified := Classify(status, text)
	if classified.Kind == accounts.KindAuth {
		return "", fmt.Errorf("workbuddy checkin session dead: re-login required")
	}
	if msg, ok := alreadyCheckedInMessage(status, body); ok {
		return msg, AlreadyCheckedInError{Msg: msg}
	}
	if status >= 300 {
		return "", fmt.Errorf("checkin status=%d: %s", status, text)
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return "", fmt.Errorf("checkin parse: %w", err)
	}
	msg := strings.TrimSpace(env.Msg)
	if env.Code != 0 {
		if msg == "" {
			msg = fmt.Sprintf("checkin code=%d", env.Code)
		}
		return msg, fmt.Errorf("checkin code=%d msg=%s", env.Code, msg)
	}
	if msg == "" {
		msg = "ok"
	}
	return msg, nil
}

func alreadyCheckedInMessage(status int, body []byte) (string, bool) {
	var env envelope
	msg := ""
	if json.Unmarshal(body, &env) == nil {
		msg = strings.TrimSpace(env.Msg)
		if env.Code == 0 && status < 300 {
			return "", false
		}
	}
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	lower := strings.ToLower(msg)
	if strings.Contains(msg, "已签到") || (strings.Contains(lower, "already") && strings.Contains(lower, "check")) {
		return msg, true
	}
	return "", false
}

// checkinRetrier 记录每个失败类别已推进到哪一步。若共用一个计数器，某一类的
// 突发失败会消耗掉另一类的阶梯——典型场景是冷启动：前几次尝试因网络失败，之后
// 又因一个仍在启动中的代理返回 5xx 而失败。按类别计数可让每条阶梯保持完整；
// 总尝试次数仍以各阶梯之和为界。
type checkinRetrier struct {
	network    int
	server     int
	processing int
}

// delay 返回下一次尝试前应等待多久。again=false 表示没有适用的类别
// （或该类别的阶梯已耗尽，调用方必须把上游的答复作为最终结果返回）。
func (r *checkinRetrier) delay(err error, status int, body []byte) (time.Duration, bool) {
	switch {
	case status == http.StatusTooManyRequests && checkinRequestProcessing(body):
		if r.processing >= len(checkinProcessingRetryDelays) {
			return 0, false
		}
		next := checkinProcessingRetryDelays[r.processing]
		r.processing++
		return next, true
	case status >= http.StatusInternalServerError:
		if r.server >= len(checkinServerRetryDelays) {
			return 0, false
		}
		next := checkinServerRetryDelays[r.server]
		r.server++
		return next, true
	case err != nil:
		// 已取消或已过期的 context 会立刻再次失败。
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 0, false
		}
		if r.network >= len(checkinNetworkRetryDelays) {
			return 0, false
		}
		next := checkinNetworkRetryDelays[r.network]
		r.network++
		return next, true
	default:
		// 4xx 业务答复是最终的；重试它只会重复同一答复。
		return 0, false
	}
}

// sleepContext 等待 wait，若 ctx 先结束则返回 false。
// 非正的 wait 会立即返回。
func sleepContext(ctx context.Context, wait time.Duration) bool {
	if wait <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func checkinRequestProcessing(body []byte) bool {
	var env envelope
	message := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &env) == nil && strings.TrimSpace(env.Msg) != "" {
		message = strings.TrimSpace(env.Msg)
	}
	lower := strings.ToLower(message)
	return strings.Contains(message, "请求处理中") ||
		strings.Contains(lower, "request is being processed") ||
		strings.Contains(lower, "request processing")
}

// Keepalive 强制为该账号刷新一次 token。会话失效走既有的 Refresh Observe(auth) 路径。
func (c *Client) Keepalive(ctx context.Context, accountID string) error {
	_, payload, err := c.store.LoadCredentialPayload(ctx, accountID)
	if err != nil {
		return err
	}
	credential, err := DecodeCredential(payload)
	if err != nil {
		return err
	}
	_, err = c.Refresh(ctx, accountID, credential)
	return err
}

// UserResource 从 get-user-resource 汇总套餐的剩余/已用/总量。
// packages 携带从 CycleEndTime 解析出的每套餐过期明细。
func (c *Client) UserResource(ctx context.Context, accountID string, credential Credential) (remain, used, total int64, packages []providers.QuotaPackage, err error) {
	accounts, totalDosage, err := c.fetchResourceAccounts(ctx, accountID, credential)
	if err != nil {
		return 0, 0, 0, nil, err
	}
	raw := decodeResourcePackages(accounts)
	remain, used, total = aggregateUserResource(raw, totalDosage)
	return remain, used, total, quotaPackages(raw, time.Now()), nil
}

// fetchResourceAccounts 执行 get-user-resource 请求，返回原始的 Accounts 条目
// 与 TotalDosage。调用方用 decodeResourcePackages（取值）解码这些条目，或检查
// 原始键（可观测性），因此计费资源端点只有一条请求/解析路径。
func (c *Client) fetchResourceAccounts(ctx context.Context, accountID string, credential Credential) ([]json.RawMessage, int64, error) {
	now := time.Now()
	payload, err := json.Marshal(map[string]any{
		"PageNumber":               1,
		"PageSize":                 100,
		"ProductCode":              "p_tcaca",
		"Status":                   []int{0, 3},
		"PackageEndTimeRangeBegin": now.Format("2006-01-02 15:04:05"),
		"PackageEndTimeRangeEnd":   now.Add(365 * 101 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		return nil, 0, err
	}
	body, status, err := c.do(ctx, accountID, http.MethodPost, credential.BillingBase()+pathUserResource, payload,
		func(h http.Header) { SetBillingHeaders(h, credential) })
	if err != nil {
		return nil, 0, err
	}
	if status >= 300 {
		return nil, 0, fmt.Errorf("user-resource status=%d: %s", status, strings.TrimSpace(string(body)))
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Response struct {
				Data struct {
					TotalDosage int64             `json:"TotalDosage"`
					Accounts    []json.RawMessage `json:"Accounts"`
				} `json:"Data"`
			} `json:"Response"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, 0, fmt.Errorf("user-resource parse: %w", err)
	}
	if env.Code != 0 {
		return nil, 0, fmt.Errorf("user-resource code=%d msg=%s", env.Code, env.Msg)
	}
	return env.Data.Response.Data.Accounts, env.Data.Response.Data.TotalDosage, nil
}

// decodeResourcePackages 把原始 Accounts 条目解码为 resourcePackage。
// 解码失败的条目不处理。
func decodeResourcePackages(accounts []json.RawMessage) []resourcePackage {
	out := make([]resourcePackage, 0, len(accounts))
	for _, raw := range accounts {
		var pkg resourcePackage
		if json.Unmarshal(raw, &pkg) != nil {
			continue
		}
		out = append(out, pkg)
	}
	return out
}

// resourcePackage 是上游 get-user-resource 的套餐结构。字段名已依据一次实时
// 上游抓包（CN 家族）确认：身份字段是 ResourceId / ResourceCycleId /
// PackageCode（没有 "PackageId"），周期起点是 CycleStartTime（没有
// "CycleBeginTime"），CreateTime 是 Unix 毫秒。CycleEndTime（"2006-01-02 15:04:05"）
// 已在下方用于过期时间。
type resourcePackage struct {
	CapacityRemain      int64  `json:"CapacityRemain"`
	CapacityUsed        int64  `json:"CapacityUsed"`
	CapacitySize        int64  `json:"CapacitySize"`
	CycleCapacityRemain int64  `json:"CycleCapacityRemain"`
	CycleCapacityUsed   int64  `json:"CycleCapacityUsed"`
	CycleCapacitySize   int64  `json:"CycleCapacitySize"`
	CycleStartTime      string `json:"CycleStartTime"`
	CycleEndTime        string `json:"CycleEndTime"`
	CreateTime          int64  `json:"CreateTime"`
	ResourceID          string `json:"ResourceId"`
	ResourceCycleID     int64  `json:"ResourceCycleId"`
	PackageCode         string `json:"PackageCode"`
	PackageName         string `json:"PackageName"`

	// 备用的过期时间载体。只有 CycleEndTime 经实时抓包确认；这些作为兜底读取，
	// 这样上游静默改名时会退化为「无过期时间」，而不是「错误的值落在一个没人
	// 读取的字段里」。不存在的名字只会解析为空，所以多带几个未确认的字段没有
	// 成本——但除非有抓包显示其有值，否则不要把它们提升为主字段。
	ExpiredTime    string `json:"ExpiredTime"`
	PackageEndTime string `json:"PackageEndTime"`
	ExpireTime     string `json:"ExpireTime"`
	ValidEndTime   string `json:"ValidEndTime"`
	EndTime        string `json:"EndTime"`
	ExpireAt       string `json:"ExpireAt"`
}

// expiryCandidates 是套餐过期时间的读取顺序。CycleEndTime 排第一，
// 因为它是唯一经实时抓包确认的。
var expiryCandidates = []func(resourcePackage) string{
	func(p resourcePackage) string { return p.CycleEndTime },
	func(p resourcePackage) string { return p.ExpiredTime },
	func(p resourcePackage) string { return p.PackageEndTime },
	func(p resourcePackage) string { return p.ExpireTime },
	func(p resourcePackage) string { return p.ValidEndTime },
	func(p resourcePackage) string { return p.EndTime },
	func(p resourcePackage) string { return p.ExpireAt },
}

// packageExpiry 选取第一个能解析为上游周期结束时间的候选值。它有意取第一个
// 可解析的候选，而非所有候选中的最早者：一个套餐常常把真实周期结束时间与一个
// 十年后的占位值放在一起，取「最早」会让占位值胜出。
//
// 当值可解析但远在未来、不合常理时，endsAt 为零（见 farFutureExpiryLimit）；
// 两种情况都返回 raw，使上游的值在控制台中仍可观测。
func packageExpiry(pkg resourcePackage, now time.Time) (raw string, endsAt int64, parsed bool) {
	for _, candidate := range expiryCandidates {
		trimmed := strings.TrimSpace(candidate(pkg))
		if trimmed == "" {
			continue
		}
		at, err := time.ParseInLocation("2006-01-02 15:04:05", trimmed, cycleEndTimeZone)
		if err != nil {
			continue
		}
		if at.Sub(now) > farFutureExpiryLimit {
			return trimmed, 0, true
		}
		return trimmed, at.Unix(), true
	}
	return "", 0, false
}

// cycleEndTimeZone 是上游计费 API 用于 CycleEndTime（"2006-01-02 15:04:05"）
// 的 UTC+8 挂钟时区。
var cycleEndTimeZone = time.FixedZone("UTC+8", 8*60*60)

// farFutureExpiryLimit 界定一个可信的 CycleEndTime 上界。上游会把十年量级的
// 占位值（到 2040 年代）与真实的周期结束时间并存。把一个占位值当作截止时间来读，
// 会让即将过期额度的排序被一个永远落不进路由窗口的值打乱，并在控制台中显示
// 毫无意义的「N 天后过期」。
const farFutureExpiryLimit = 730 * 24 * time.Hour

// quotaPackages 把原始套餐转换为控制台用的每套餐过期明细。对于没有可解析
// CycleEndTime 的套餐，以及 CycleEndTime 是远未来占位值的套餐，EndsAt 保持为
// 零；两种情况都保留原始 EndTime，使上游的值仍可观测。
func quotaPackages(packages []resourcePackage, now time.Time) []providers.QuotaPackage {
	out := make([]providers.QuotaPackage, 0, len(packages))
	for _, pkg := range packages {
		remain, used, size := packageRemainUsed(pkg)
		entry := providers.QuotaPackage{
			Remain: float64(remain),
			Used:   float64(used),
			Size:   float64(size),
			Unit:   "credits",
		}
		if raw, endsAt, ok := packageExpiry(pkg, now); ok {
			entry.EndTime = raw
			entry.EndsAt = endsAt
		}
		out = append(out, entry)
	}
	return out
}

// soonestExpiry 返回最早的非零套餐过期时间，以及在该时刻过期的剩余量。
func soonestExpiry(packages []providers.QuotaPackage) (expiresAt int64, expiringRemain float64) {
	for _, pkg := range packages {
		if pkg.EndsAt <= 0 {
			continue
		}
		if expiresAt == 0 || pkg.EndsAt < expiresAt {
			expiresAt = pkg.EndsAt
			expiringRemain = pkg.Remain
		} else if pkg.EndsAt == expiresAt {
			expiringRemain += pkg.Remain
		}
	}
	return expiresAt, expiringRemain
}

func packageRemainUsed(pkg resourcePackage) (remain, used, size int64) {
	if pkg.CycleCapacitySize > 0 {
		remain = pkg.CycleCapacityRemain
		size = pkg.CycleCapacitySize
		if remain < 0 {
			remain = 0
		}
		if remain > size {
			remain = size
		}
		used = size - remain
		if pkg.CycleCapacityUsed > used {
			used = pkg.CycleCapacityUsed
			if size >= used {
				remain = size - used
			}
		}
		return remain, used, size
	}
	if pkg.CycleCapacityRemain > 0 || pkg.CycleCapacityUsed > 0 {
		remain = pkg.CycleCapacityRemain
		used = pkg.CycleCapacityUsed
		size = pkg.CycleCapacitySize
		if remain < 0 {
			remain = 0
		}
		return remain, used, size
	}
	remain = pkg.CapacityRemain
	used = pkg.CapacityUsed
	size = pkg.CapacitySize
	if remain < 0 {
		remain = 0
	}
	if used == 0 && size > remain {
		used = size - remain
	}
	return remain, used, size
}

func aggregateUserResource(packages []resourcePackage, totalDosage int64) (remain, used, size int64) {
	for _, pkg := range packages {
		r, u, s := packageRemainUsed(pkg)
		remain += r
		used += u
		size += s
	}
	if size > 0 {
		if derived := size - remain; derived > used {
			used = derived
		}
	}
	if totalDosage > size {
		size = totalDosage
		if derived := size - remain; derived > used {
			used = derived
		}
	}
	return remain, used, size
}

// Adapter 返回用于注册的 provider 能力包。
func (c *Client) Adapter() providers.Adapter {
	return providers.Adapter{
		ID:         "workbuddy",
		Credential: credentialCodec{},
		Login:      c,
		Chat:       c,
		Models:     c,
		Classifier: classifier{},
		Prober:     c,
		Checkin:    c,
		Growth:     growthRunner{client: c},
	}
}

type credentialCodec struct{}

func (credentialCodec) Validate(payload []byte) error { return ValidateCredential(payload) }

type classifier struct{}

func (classifier) Classify(status int, body string) providers.ClassifiedError {
	return Classify(status, body)
}

func (c *Client) hasCatalogEntry(model string) bool {
	model = strings.TrimSpace(model)
	if model == "" || c == nil {
		return false
	}
	canonical := accounts.CanonicalModelID(model)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.catalog[model]; ok {
		return true
	}
	_, ok := c.catalog[canonical]
	return ok
}

// upstreamModelID 在请求模型是已知目录条目时返回目录中的 NativeModel；
// 否则原样发送请求模型。不存在硬编码的 id 重写表。
func (c *Client) upstreamModelID(model string) string {
	canonical := accounts.CanonicalModelID(model)
	if c != nil {
		c.mu.Lock()
		info, ok := c.catalog[model]
		if !ok {
			info, ok = c.catalog[canonical]
		}
		c.mu.Unlock()
		if ok && strings.TrimSpace(info.NativeModel) != "" {
			return info.NativeModel
		}
	}
	return model
}
