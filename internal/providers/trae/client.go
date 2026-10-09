package trae

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/auth"
	"agent2api/internal/providers"
	"agent2api/internal/providers/credfresh"
	proxyutil "agent2api/internal/proxy"
	"agent2api/internal/translate"
)

// Store 是适配器所需的持久化接口。
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

// ModelSettingReader 是可选的。缺失它时，如果请求未设置 IsMaxMode，
// 则回退到旧的仅 max-mode 断言。
type ModelSettingReader interface {
	GetProviderModelSetting(context.Context, string, string) (accounts.ProviderModelSetting, error)
}

// ModelMaxModeReader 是 ModelSettingReader 缺失时使用的 Trae 专用兜底。
// 不要把它并入 Store；没有它的测试 fake 仍必须跳过。
type ModelMaxModeReader interface {
	GetProviderModelMaxMode(context.Context, string, string) (bool, error)
}

type loginPending struct {
	machineID    string
	deviceID     string
	deviceKey    string
	callbackURL  string
	codeVerifier string
	authCode     string
	trace        string
	createdAt    time.Time
	done         bool
	failed       bool
	message      string
	credential   Credential
}

type Client struct {
	store Store
	http  *http.Client

	transports proxyutil.TransportCache

	mu      sync.Mutex
	pending map[string]*loginPending
	// pendingTraces 把 login_trace_id 映射到签发了匹配 code_challenge 的
	// pending 条目。回调会回显 loginTraceID，因此即使更新的登录已替换了
	// 该账号的当前 pending 条目，粘贴的回调也能被匹配到其确切的 verifier。
	pendingTraces map[string]*loginPending
	listener      net.Listener
	catalog       map[string]providers.ModelInfo

	// refreshes 按账号合并 token 刷新；见 credential()。
	refreshes credfresh.Group[Credential]
}

const catalogTimeout = 15 * time.Second

func NewClient(store Store) *Client {
	return &Client{
		store: store,
		http: &http.Client{
			Timeout: 120 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		pending:       map[string]*loginPending{},
		pendingTraces: map[string]*loginPending{},
	}
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

func (c *Client) StartLogin(ctx context.Context, accountID string) (providers.LoginSession, error) {
	credential := Credential{Domain: DomainCN, APIHost: OAuthHost}
	if _, payload, err := c.store.LoadCredentialPayload(ctx, accountID); err == nil {
		if decoded, err := DecodeCredential(payload); err == nil {
			credential.MachineID = decoded.MachineID
			credential.DeviceID = decoded.DeviceID
			if decoded.Domain != "" {
				credential.Domain = decoded.Domain
			}
			if decoded.APIHost != "" {
				credential.APIHost = decoded.APIHost
			}
		}
	}
	credential = EnsureDevice(credential)
	credential = EnsureDeviceKey(credential)
	callbackURL, err := c.ensureCallback()
	if err != nil {
		return providers.LoginSession{}, err
	}
	trace := randomHex(8)
	verifier := ""
	if v, _, err := pkcePair(); err == nil {
		verifier = v
	}
	c.mu.Lock()
	if c.pendingTraces == nil {
		c.pendingTraces = map[string]*loginPending{}
	}
	entry := &loginPending{
		machineID:    credential.MachineID,
		deviceID:     credential.DeviceID,
		deviceKey:    credential.DevicePrivateKey,
		callbackURL:  callbackURL,
		codeVerifier: verifier,
		trace:        trace,
		createdAt:    time.Now(),
	}
	c.pending[accountID] = entry
	c.pendingTraces[trace] = entry
	c.mu.Unlock()
	authURL := buildLoginURL(credential.MachineID, credential.DeviceID, callbackURL, trace, verifier)
	return providers.LoginSession{AuthURL: authURL, State: trace}, nil
}

func (c *Client) PollLogin(ctx context.Context, accountID string) (bool, string, error) {
	c.mu.Lock()
	pending := c.pending[accountID]
	c.mu.Unlock()
	if pending == nil {
		return false, "", fmt.Errorf("login not started for account %s", accountID)
	}
	if time.Since(pending.createdAt) > loginPendingTTL {
		c.mu.Lock()
		delete(c.pending, accountID)
		c.mu.Unlock()
		return false, "", fmt.Errorf("login expired; start again")
	}
	c.mu.Lock()
	done := pending.done
	failed := pending.failed
	message := pending.message
	credential := pending.credential
	authCode := pending.authCode
	codeVerifier := pending.codeVerifier
	c.mu.Unlock()
	if failed {
		return false, "", fmt.Errorf("%s", firstNonEmpty(message, "login failed"))
	}
	if !done {
		return false, firstNonEmpty(message, "waiting for authorization"), nil
	}
	if err := c.finishCredential(ctx, accountID, credential, authCode, codeVerifier); err != nil {
		return false, "", err
	}
	c.mu.Lock()
	delete(c.pending, accountID)
	c.mu.Unlock()
	return true, "login complete", nil
}

func (c *Client) CompleteLogin(ctx context.Context, accountID, callbackURL string) error {
	info, err := ParseCallback(callbackURL)
	if err != nil {
		return err
	}
	credential := Credential{
		AccessToken:  info.AccessToken,
		RefreshToken: info.RefreshToken,
		ExpiresAt:    unixSeconds(info.ExpiresAt),
		UID:          info.UID,
		Nickname:     info.Nickname,
		EnterpriseID: info.EnterpriseID,
		Domain:       DomainCN,
		APIHost:      firstNonEmpty(info.APIHost, OAuthHost),
	}
	c.mu.Lock()
	pending := c.pending[accountID]
	// 优先采用 challenge 与回调的 loginTraceID 匹配的 pending 条目，
	// 使粘贴的回调与真正铸造其 code 的 verifier 配对，即使更新的登录
	// 已替换了该账号槽位。
	if info.Trace != "" {
		if traced := c.pendingTraces[info.Trace]; traced != nil {
			pending = traced
		}
	}
	c.mu.Unlock()
	codeVerifier := ""
	if pending != nil {
		if pending.done {
			c.mu.Lock()
			delete(c.pending, accountID)
			c.mu.Unlock()
			return nil
		}
		credential.MachineID = pending.machineID
		credential.DeviceID = pending.deviceID
		credential.DevicePrivateKey = pending.deviceKey
		codeVerifier = pending.codeVerifier
	} else if _, payload, err := c.store.LoadCredentialPayload(ctx, accountID); err == nil {
		if decoded, err := DecodeCredential(payload); err == nil {
			if decoded.Ready() {
				return nil
			}
			credential.MachineID = decoded.MachineID
			credential.DeviceID = decoded.DeviceID
		}
	}
	if err := c.finishCredential(ctx, accountID, credential, info.AuthCode, codeVerifier); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.pending, accountID)
	if pending != nil && pending.trace != "" {
		delete(c.pendingTraces, pending.trace)
	}
	c.mu.Unlock()
	return nil
}

func (c *Client) finishCredential(ctx context.Context, accountID string, credential Credential, authCode, codeVerifier string) error {
	switch {
	case strings.TrimSpace(authCode) != "":
		// 用 PKCE verifier 交换回调的 authorization code。
		refreshed, err := c.ExchangeAuthCode(ctx, accountID, credential, authCode, codeVerifier)
		if err != nil {
			return err
		}
		credential = refreshed
	case strings.TrimSpace(credential.RefreshToken) != "":
		refreshed, err := c.ExchangeToken(ctx, accountID, credential)
		if err != nil {
			return err
		}
		credential = refreshed
	}
	if strings.TrimSpace(credential.UID) == "" && strings.TrimSpace(credential.AccessToken) != "" {
		info, err := c.GetUserInfo(ctx, accountID, credential)
		if err == nil {
			credential.UID = info.UID
			if info.Nickname != "" {
				credential.Nickname = info.Nickname
			}
			if info.EnterpriseID != "" {
				credential.EnterpriseID = info.EnterpriseID
			}
		}
	}
	credential = EnsureDevice(credential)
	credential = EnsureDeviceKey(credential)
	payload, err := credential.Encode()
	if err != nil {
		return err
	}
	if err := c.store.SaveCredentialPayload(ctx, accountID, CredentialFormat, payload); err != nil {
		return err
	}
	_ = c.store.Observe(ctx, accountID, credential.UID, "ready", "", "")
	return nil
}

func (c *Client) ensureCallback() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.listener != nil {
		addr, ok := c.listener.Addr().(*net.TCPAddr)
		if ok {
			return fmt.Sprintf("http://127.0.0.1:%d%s", addr.Port, pathCallback), nil
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("trae callback listen: %w", err)
	}
	c.listener = ln
	auth.ServeLoopback(ln, pathCallback, "Trae", c.acceptCallback)
	addr := ln.Addr().(*net.TCPAddr)
	return fmt.Sprintf("http://127.0.0.1:%d%s", addr.Port, pathCallback), nil
}

func (c *Client) acceptCallback(ctx context.Context, rawURL string) error {
	info, err := ParseCallback(rawURL)
	if err != nil {
		c.markPendingFailed(err.Error())
		return err
	}
	credential := Credential{
		AccessToken:  info.AccessToken,
		RefreshToken: info.RefreshToken,
		ExpiresAt:    unixSeconds(info.ExpiresAt),
		UID:          info.UID,
		Nickname:     info.Nickname,
		EnterpriseID: info.EnterpriseID,
		Domain:       DomainCN,
		APIHost:      OAuthHost,
	}
	c.mu.Lock()
	for _, pending := range c.pending {
		if pending.done || pending.failed {
			continue
		}
		credential.MachineID = pending.machineID
		credential.DeviceID = pending.deviceID
		pending.credential = credential
		pending.authCode = info.AuthCode
		pending.done = true
		pending.message = "authorization received"
		break
	}
	c.mu.Unlock()
	return nil
}

func (c *Client) markPendingFailed(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, pending := range c.pending {
		if pending.done || pending.failed {
			continue
		}
		pending.failed = true
		pending.message = message
		return
	}
}

type callbackInfo struct {
	AuthCode     string
	RefreshToken string
	AccessToken  string
	UID          string
	Nickname     string
	EnterpriseID string
	ExpiresAt    int64
	APIHost      string
	Trace        string
}

func ParseCallback(rawURL string) (callbackInfo, error) {
	if strings.TrimSpace(rawURL) == "" {
		return callbackInfo{}, fmt.Errorf("empty callback url")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return callbackInfo{}, fmt.Errorf("parse callback url: %w", err)
	}
	query := parsed.Query()
	info := callbackInfo{
		AuthCode:     firstNonEmpty(query.Get("code"), query.Get("authCode"), query.Get("auth_code")),
		RefreshToken: firstNonEmpty(query.Get("refreshToken"), query.Get("refresh_token")),
		AccessToken:  firstNonEmpty(query.Get("accessToken"), query.Get("userJwt")),
		Trace:        firstNonEmpty(query.Get("loginTraceID"), query.Get("login_trace_id")),
	}
	// authorization code 被投递在 JSON authCodeInfo 参数内：
	// {"AuthCode":"...","ExpireAt":...,"ExpireDuration":...}。
	if info.AuthCode == "" {
		if raw := firstNonEmpty(query.Get("authCodeInfo"), query.Get("auth_code_info")); raw != "" {
			var nested struct {
				AuthCode string `json:"AuthCode"`
				Code     string `json:"code"`
			}
			if json.Unmarshal([]byte(raw), &nested) == nil {
				info.AuthCode = firstNonEmpty(nested.AuthCode, nested.Code)
			}
		}
	}
	// 回调还携带 host / userRegion 提示；优先采用回调 host，
	// 使 code 交换指向浏览器所登录的区域。
	if host := strings.TrimSpace(query.Get("host")); host != "" {
		host = strings.TrimRight(host, "/")
		if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
			info.APIHost = host
		}
	}
	if userInfo := query.Get("userInfo"); userInfo != "" {
		var nested struct {
			UserID       string `json:"UserID"`
			ScreenName   string `json:"ScreenName"`
			TenantID     string `json:"TenantID"`
			UID          string `json:"uid"`
			Nickname     string `json:"nickname"`
			EnterpriseID string `json:"enterpriseId"`
		}
		if json.Unmarshal([]byte(userInfo), &nested) == nil {
			info.UID = firstNonEmpty(nested.UserID, nested.UID)
			info.Nickname = firstNonEmpty(nested.ScreenName, nested.Nickname)
			info.EnterpriseID = firstNonEmpty(nested.TenantID, nested.EnterpriseID)
		}
	}
	if userJWT := query.Get("userJwt"); userJWT != "" {
		var jwt struct {
			Token         string `json:"Token"`
			RefreshToken  string `json:"RefreshToken"`
			TokenExpireAt int64  `json:"TokenExpireAt"`
		}
		if json.Unmarshal([]byte(userJWT), &jwt) == nil {
			if info.RefreshToken == "" {
				info.RefreshToken = jwt.RefreshToken
			}
			if jwt.Token != "" {
				info.AccessToken = jwt.Token
			}
			if jwt.TokenExpireAt != 0 {
				info.ExpiresAt = jwt.TokenExpireAt
			}
		}
	}
	if info.RefreshToken == "" && info.AccessToken == "" && info.AuthCode == "" {
		return callbackInfo{}, fmt.Errorf("callback missing code, refreshToken and userJwt.Token")
	}
	return info, nil
}

func buildLoginURL(machineID, deviceID, callbackURL, trace, codeVerifier string) string {
	values := url.Values{}
	values.Set("login_version", "1")
	values.Set("auth_from", AuthFrom)
	values.Set("login_channel", "native_ide")
	values.Set("plugin_version", PluginVersion)
	values.Set("auth_type", "local")
	values.Set("client_id", ClientID)
	values.Set("redirect", "0")
	values.Set("login_trace_id", trace)
	values.Set("auth_callback_url", callbackURL)
	values.Set("machine_id", machineID)
	values.Set("device_id", deviceID)
	values.Set("x_device_id", deviceID)
	values.Set("x_machine_id", machineID)
	values.Set("x_device_brand", "PC")
	values.Set("x_device_type", "PC")
	values.Set("x_os_version", "1.0")
	values.Set("x_env", "")
	values.Set("x_app_version", IDEVersion)
	values.Set("x_app_type", "stable")
	if codeVerifier == "" {
		codeVerifier, _ = pkceVerifier()
	}
	if codeVerifier != "" {
		values.Set("code_challenge", pkceChallenge(codeVerifier))
		values.Set("code_challenge_method", "S256")
	}
	return ConsoleHost + pathAuthorization + "?" + values.Encode()
}

func (c *Client) ExchangeToken(ctx context.Context, accountID string, credential Credential) (Credential, error) {
	return c.exchangeToken(ctx, accountID, credential, true)
}

// exchangeToken 执行一次刷新交换。allowHeal 为 true 时：若失败属于
// 「refresh token 与刷新 client 不匹配」且凭据尚无 client 绑定
// （PKCE 切换前的旧账号其绑定可能缺失），自动以 LegacyClientID 再试一次——
// 成功即返回带绑定的凭据（调用方随刷新结果持久化），失败则保留原始错误。
// 已绑定或无关错误的失败不触发重试；重试自身不再递归（防循环）。
func (c *Client) exchangeToken(ctx context.Context, accountID string, credential Credential, allowHeal bool) (Credential, error) {
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return credential, fmt.Errorf("no refreshToken")
	}
	body, err := json.Marshal(map[string]any{
		"ClientID":     c.refreshClientID(credential),
		"RefreshToken": credential.RefreshToken,
		"ClientSecret": "-",
		"UserID":       "",
	})
	if err != nil {
		return credential, err
	}
	payload, status, err := c.do(ctx, accountID, http.MethodPost, credential.AuthBase()+pathExchange, body, SetOAuthHeaders)
	if err != nil {
		return credential, err
	}
	if status >= 300 {
		classified := classifiedError(status, payload)
		if allowHeal && credential.RefreshClientID == "" && isClientMismatch(payload) {
			return c.retryWithLegacyClient(ctx, accountID, credential, classified)
		}
		return credential, classified
	}
	var env struct {
		Result struct {
			Token               string `json:"Token"`
			TokenExpireAt       int64  `json:"TokenExpireAt"`
			TokenExpireDuration int64  `json:"TokenExpireDuration"`
			RefreshToken        string `json:"RefreshToken"`
			RefreshExpireAt     int64  `json:"RefreshExpireAt"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return credential, fmt.Errorf("exchange token parse: %w", err)
	}
	if strings.TrimSpace(env.Result.Token) == "" {
		noToken := fmt.Errorf("refresh_failed: no token in response — re-login required")
		if allowHeal && credential.RefreshClientID == "" && isClientMismatch(payload) {
			return c.retryWithLegacyClient(ctx, accountID, credential, noToken)
		}
		return credential, noToken
	}
	credential.AccessToken = env.Result.Token
	if env.Result.RefreshToken != "" {
		credential.RefreshToken = env.Result.RefreshToken
	}
	if env.Result.TokenExpireAt != 0 {
		credential.ExpiresAt = unixSeconds(env.Result.TokenExpireAt)
	} else if env.Result.TokenExpireDuration > 0 {
		credential.ExpiresAt = time.Now().Add(time.Duration(env.Result.TokenExpireDuration) * time.Second).Unix()
	}
	if env.Result.RefreshExpireAt != 0 {
		credential.RefreshExpiresAt = unixSeconds(env.Result.RefreshExpireAt)
	}
	return credential, nil
}

// retryWithLegacyClient 以 LegacyClientID 重试一次刷新（自愈路径）。
// 成功返回带 client 绑定的新凭据（RefreshClientID=LegacyClientID，
// 由调用方持久化）；重试失败不掩盖原始错误。
func (c *Client) retryWithLegacyClient(ctx context.Context, accountID string, credential Credential, cause error) (Credential, error) {
	retry := credential
	retry.RefreshClientID = LegacyClientID
	next, err := c.exchangeToken(ctx, accountID, retry, false)
	if err != nil {
		return credential, cause
	}
	return next, nil
}

// isClientMismatch 报告刷新响应是否属于「refresh token 与刷新 client 不匹配」。
// 上游把该错误嵌在 ResponseMetadata.Error 里（业务码 10101 / 040004），顶层没有
// code 字段（extractCode 提取不到），因此按码值与消息文本双条件匹配。
func isClientMismatch(payload []byte) bool {
	text := string(payload)
	return strings.Contains(text, "10101") || strings.Contains(strings.ToLower(text), "not matched to the client")
}

// refreshClientID 是用于刷新凭据 token 的 OAuth client。
// 通常是登录 client；PKCE 切换之前创建的账号会记录铸造其 refresh token 的
// 旧版 SOLO client，因为 refresh token 绑定于签发它们的 client。
func (c *Client) refreshClientID(credential Credential) string {
	if id := strings.TrimSpace(credential.RefreshClientID); id != "" {
		return id
	}
	return ClientID
}

// ExchangeAuthCode 执行 PKCE authorization-code 交换：
// 用回调的 code 和匹配的 code verifier POST
// /trae/api/v3/oauth/ExchangeToken。响应携带与 refresh-token 交换相同的
// token 字段，因此填充同一个 Credential。
func (c *Client) ExchangeAuthCode(ctx context.Context, accountID string, credential Credential, authCode, codeVerifier string) (Credential, error) {
	if strings.TrimSpace(authCode) == "" {
		return credential, fmt.Errorf("no auth code")
	}
	pubKey := devicePublicKeyPEM(credential.DevicePrivateKey)
	deviceInfo := map[string]any{
		"DeviceID":      credential.DeviceID,
		"MachineID":     credential.MachineID,
		"PlatformCode":  "IDE_PC",
		"DeviceType":    "PC",
		"DeviceName":    "agent2api",
		"DeviceModel":   "",
		"ClientVersion": IDEVersion,
		"DeviceBrand":   "",
		"OSInfo":        "linux",
		"OSVersion":     "Ubuntu 24.04.4 LTS",
	}
	if pubKey != "" {
		deviceInfo["DevicePublicKey"] = pubKey
	}
	body, err := json.Marshal(map[string]any{
		"ClientID":     ClientID,
		"AuthCode":     authCode,
		"CodeVerifier": codeVerifier,
		"IDEVersion":   IDEVersion,
		"DeviceInfo":   deviceInfo,
	})
	if err != nil {
		return credential, err
	}
	payload, status, err := c.do(ctx, accountID, http.MethodPost, credential.AuthBase()+pathExchangeCode, body, SetOAuthHeaders)
	if err != nil {
		return credential, err
	}
	if status >= 300 {
		return credential, classifiedError(status, payload)
	}
	var env struct {
		Result struct {
			Token               string `json:"Token"`
			TokenExpireAt       int64  `json:"TokenExpireAt"`
			TokenExpireDuration int64  `json:"TokenExpireDuration"`
			RefreshToken        string `json:"RefreshToken"`
			RefreshExpireAt     int64  `json:"RefreshExpireAt"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return credential, fmt.Errorf("code exchange parse: %w", err)
	}
	if strings.TrimSpace(env.Result.Token) == "" {
		return credential, fmt.Errorf("code_exchange_failed: no token in response")
	}
	credential.AccessToken = env.Result.Token
	if env.Result.RefreshToken != "" {
		credential.RefreshToken = env.Result.RefreshToken
	}
	if env.Result.TokenExpireAt != 0 {
		credential.ExpiresAt = unixSeconds(env.Result.TokenExpireAt)
	} else if env.Result.TokenExpireDuration > 0 {
		credential.ExpiresAt = time.Now().Add(time.Duration(env.Result.TokenExpireDuration) * time.Second).Unix()
	}
	if env.Result.RefreshExpireAt != 0 {
		credential.RefreshExpiresAt = unixSeconds(env.Result.RefreshExpireAt)
	}
	return credential, nil
}

type userInfo struct {
	UID          string
	Nickname     string
	EnterpriseID string
}

func (c *Client) GetUserInfo(ctx context.Context, accountID string, credential Credential) (userInfo, error) {
	body, err := json.Marshal(map[string]any{
		"ReqSource":  "IDE",
		"IDEVersion": IDEVersion,
	})
	if err != nil {
		return userInfo{}, err
	}
	payload, status, err := c.do(ctx, accountID, http.MethodPost, credential.AuthBase()+pathUserInfo, body, func(h http.Header) {
		SetOAuthHeaders(h)
		if credential.AccessToken != "" {
			h.Set("X-Cloudide-Token", credential.AccessToken)
		}
	})
	if err != nil {
		return userInfo{}, err
	}
	if status >= 300 {
		return userInfo{}, classifiedError(status, payload)
	}
	var env struct {
		Result struct {
			UserID       string `json:"UserID"`
			ScreenName   string `json:"ScreenName"`
			EnterpriseID string `json:"EnterpriseID"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return userInfo{}, fmt.Errorf("user info parse: %w", err)
	}
	return userInfo{
		UID:          env.Result.UserID,
		Nickname:     env.Result.ScreenName,
		EnterpriseID: env.Result.EnterpriseID,
	}, nil
}

// credentialRefreshTimeout 限定一次共享刷新。刷新与启动它的调用方
// 脱离运行，因此需要自己的截止时间。
const credentialRefreshTimeout = 60 * time.Second

// credential 加载已存储的 token，并在临近过期时刷新它。
//
// 刷新按账号合并：该端点会轮换 refresh token，因此 N 个并发刷新会持久化
// N 个各不相同的 token，而后来的写入会使已经发出的那些失效。写回本身
// 也是比较并写入（见 persistRefreshed）。
func (c *Client) credential(ctx context.Context, accountID string) (Credential, error) {
	credential, err := c.loadCredential(ctx, accountID)
	if err != nil {
		return Credential{}, err
	}
	now := time.Now()
	if credential.refreshExpired(now) {
		_ = c.store.Observe(ctx, accountID, credential.UID, "login_required", "refresh token expired; re-login required", accounts.KindAuth)
		return credential, fmt.Errorf("trae refresh token expired; re-login required")
	}
	if !credential.needsRefresh(now) {
		return credential, nil
	}

	refreshed, err := c.refreshes.Do(ctx, accountID, credentialRefreshTimeout, func(runCtx context.Context) (Credential, error) {
		base := credential
		if current, loadErr := c.loadCredential(runCtx, accountID); loadErr == nil {
			// 刚刚完成的刷新使本次刷新变得不必要。
			if !current.needsRefresh(time.Now()) && !current.refreshExpired(time.Now()) {
				return current, nil
			}
			base = current
		}
		next, refreshErr := c.ExchangeToken(runCtx, accountID, base)
		if refreshErr != nil {
			_ = c.store.Observe(runCtx, accountID, base.UID, "login_required", refreshErr.Error(), accounts.KindAuth)
			return next, refreshErr
		}
		encoded, encodeErr := next.Encode()
		if encodeErr != nil {
			return next, encodeErr
		}
		if persistErr := c.persistRefreshed(runCtx, accountID, base, encoded); persistErr != nil {
			return next, persistErr
		}
		return next, nil
	})
	if err != nil {
		return credential, err
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

// persistRefreshed 仅当已存储的 payload 仍是本次刷新所基于的那个时，
// 才写入刷新后的凭据。该端点会轮换 refresh token，因此输掉竞态的刷新
// 绝不得覆盖胜者的 token——那样做会让账号持有一个无效 token。
//
// 不具备可选的比较并写入能力的 store 回退为无条件保存。
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
			// 本次刷新运行期间有更新的写入落地；我们这个已过期。
			return nil
		}
	}
	_, err = versioned.SaveCredentialPayloadIfUnchanged(ctx, accountID, CredentialFormat, payload, version)
	return err
}

func (c *Client) Models(ctx context.Context, accountID string) ([]providers.ModelInfo, error) {
	credential, err := c.credential(ctx, accountID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	out, err := c.fetchCatalogScene(ctx, accountID, credential, PrimaryScene)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("trae model catalog returned no models")
	}
	// 合并次 scene，使主 scene 隐藏的模型（例如 kimi-k2.6）变得可选。
	// 每个模型保留其提供服务的 scene；此处的失败不得拖垮主 catalog。
	if SecondaryScene != "" && SecondaryScene != PrimaryScene {
		if extra, err := c.fetchCatalogScene(ctx, accountID, credential, SecondaryScene); err == nil {
			out = mergeCatalogModels(out, extra)
		}
	}
	c.rememberCatalog(out)
	return out, nil
}

// fetchCatalogScene 获取并解析单个 catalog scene（function）。当该 scene
// 没有任何可用内容时，返回空切片（无错误）。
func (c *Client) fetchCatalogScene(ctx context.Context, accountID string, credential Credential, function string) ([]providers.ModelInfo, error) {
	body, err := json.Marshal(map[string]any{
		"function":            function,
		"config_names":        nil,
		"need_prompt":         false,
		"current_config_info": nil,
		"poly_prompt":         true,
		"mode_type":           nil,
		"agent_type":          nil,
	})
	if err != nil {
		return nil, err
	}
	payload, status, err := c.do(ctx, accountID, http.MethodPost, credential.ChatBase()+pathModels, body,
		func(h http.Header) { SetCatalogHeaders(h, credential) })
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, classifiedError(status, payload)
	}
	out, err := parseCatalogModels(payload, function)
	if err != nil {
		return nil, fmt.Errorf("models parse: %w", err)
	}
	return out, nil
}

func (c *Client) rememberCatalog(models []providers.ModelInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.catalog = make(map[string]providers.ModelInfo, len(models))
	for _, model := range models {
		c.catalog[model.NativeModel] = model
		c.catalog[strings.ToLower(model.NativeModel)] = model
	}
}

func (c *Client) capsFor(model string) providers.ModelCapabilities {
	if info, ok := c.lookupCatalogModel(model); ok {
		return info.Capabilities
	}
	return providers.ModelCapabilities{}
}

// sceneFor 返回该模型的对话必须使用的 scene。Trae 默认通过 chat_v3
// 服务整个 catalog；只有该 scene 未列出的模型（例如 kimi-k2.6）才回退到
// solo_work_lite。完全不在 catalog 中的模型使用 PrimaryScene。
func (c *Client) sceneFor(model string) string {
	if info, ok := c.lookupCatalogModel(model); ok && info.Scene != "" {
		return info.Scene
	}
	return PrimaryScene
}

// lookupCatalogModel 把请求/控制台的模型 id 解析为其 catalog 条目。
// Trae config_name 大小写混合；调用方可能发送控制台规范形式
// （小写、_ 折叠为 -），因此两个连接键都会被尝试。
func (c *Client) lookupCatalogModel(model string) (providers.ModelInfo, bool) {
	model = strings.TrimSpace(model)
	c.mu.Lock()
	defer c.mu.Unlock()
	if info, ok := c.catalog[model]; ok {
		return info, true
	}
	info, ok := c.catalog[accounts.CanonicalModelID(model)]
	return info, ok
}

// settingModelKey 镜像 control.ModelContextKey，使控制台保存的 provider 设置
// 在对话时能被再次找到。Trae config_name 大小写混合且可能包含下划线；
// 两侧必须做完全相同的规范化。
func settingModelKey(model string) string {
	return accounts.CanonicalModelID(model)
}

func (c *Client) chatRequest(ctx context.Context, credential Credential, req translate.ChatRequest, accountID string) (*http.Request, providers.ResolvedChat, error) {
	payload, err := json.Marshal(map[string]any{
		"model":       req.Model,
		"messages":    req.Messages,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
		"tools":       req.Tools,
		"tool_choice": req.ToolChoice,
	})
	if err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	rewritten := PrepareBody(payload, c.sceneFor(req.Model))
	resolved := providers.ResolvedChat{}
	var obj map[string]any
	if err := json.Unmarshal(rewritten, &obj); err == nil {
		maxMode := false
		storedLevel := ""
		if req.IsMaxMode != nil {
			maxMode = *req.IsMaxMode
		}
		caps := c.capsFor(req.Model)
		if setter, ok := c.store.(ModelSettingReader); ok {
			if stored, err := setter.GetProviderModelSetting(ctx, "trae", settingModelKey(req.Model)); err == nil {
				if req.IsMaxMode == nil {
					maxMode = stored.MaxMode
				}
				storedLevel = stored.ReasoningEffort
			}
		} else if req.IsMaxMode == nil {
			if maxSetter, ok := c.store.(ModelMaxModeReader); ok {
				if stored, err := maxSetter.GetProviderModelMaxMode(ctx, "trae", settingModelKey(req.Model)); err == nil {
					maxMode = stored
				}
			}
		}
		// caps 可能为空，因为 catalog 尚未获取（全新进程、首个请求）
		// 或该模型未知。只有未知这一类不可恢复；在决定前刷新一次。
		if len(caps.ReasoningOptions) == 0 && !caps.MaxMode && accountID != "" {
			_, _ = c.Models(ctx, accountID)
			caps = c.capsFor(req.Model)
		}
		if maxMode && !caps.MaxMode {
			log.Printf("trae model %q: max mode requested but catalog says unsupported; sending default context window", req.Model)
		}
		applySoloChatFields(obj, req, maxMode, storedLevel, caps)
		resolved.ReasoningLevel = resolvedReasoningLevel(req, storedLevel, caps)
		if encoded, err := json.Marshal(obj); err == nil {
			rewritten = encoded
		}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		credential.ChatBase()+pathChat, bytes.NewReader(rewritten))
	if err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	SetChatHeaders(httpReq.Header, credential)
	return httpReq, resolved, nil
}

func (c *Client) ChatNonStream(ctx context.Context, accountID string, req translate.ChatRequest) (providers.ChatOutcome, error) {
	credential, err := c.credential(ctx, accountID)
	if err != nil {
		return providers.ChatOutcome{}, err
	}
	httpReq, resolved, err := c.chatRequest(ctx, credential, req, accountID)
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
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		return providers.ChatOutcome{}, classifiedError(resp.StatusCode, body)
	}
	aggregate, err := Aggregate(bytes.NewReader(body))
	if err != nil {
		return providers.ChatOutcome{}, err
	}
	outcome, err := outcomeFromAggregate(aggregate, req.Model)
	if err != nil {
		return providers.ChatOutcome{}, err
	}
	outcome.ReasoningLevel = resolved.ReasoningLevel
	return outcome, nil
}

func (c *Client) ChatStream(ctx context.Context, accountID string, req translate.ChatRequest) (*http.Response, providers.ResolvedChat, error) {
	credential, err := c.credential(ctx, accountID)
	if err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	httpReq, resolved, err := c.chatRequest(ctx, credential, req, accountID)
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
		return nil, providers.ResolvedChat{}, classifiedError(resp.StatusCode, body)
	}
	streamResp, err := rewriteSoloStream(resp.Body, req.Model)
	if err != nil {
		return nil, providers.ResolvedChat{}, err
	}
	return streamResp, resolved, nil
}

func outcomeFromAggregate(aggregate map[string]any, fallbackModel string) (providers.ChatOutcome, error) {
	raw, err := json.Marshal(aggregate)
	if err != nil {
		return providers.ChatOutcome{}, err
	}
	var parsed struct {
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
			CacheReadTokens  *int   `json:"cache_read_tokens"`
			Source           string `json:"source"`
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
	out := providers.ChatOutcome{UsageSource: "upstream", FinishReason: "stop", Model: fallbackModel}
	if parsed.Model != "" {
		out.Model = parsed.Model
	}
	if len(parsed.Choices) > 0 {
		out.Content = parsed.Choices[0].Message.Content
		out.Reasoning = parsed.Choices[0].Message.ReasoningContent
		out.ToolCalls = parsed.Choices[0].Message.ToolCalls
		if parsed.Choices[0].FinishReason != "" {
			out.FinishReason = parsed.Choices[0].FinishReason
		}
	}
	out.PromptTokens = parsed.Usage.PromptTokens
	out.CompletionTokens = parsed.Usage.CompletionTokens
	out.CacheReadTokens = parsed.Usage.CacheReadTokens
	if out.CacheReadTokens == nil {
		out.CacheReadTokens = parsed.Usage.PromptDetails.CachedTokens
	}
	if parsed.Usage.Source != "" {
		out.UsageSource = parsed.Usage.Source
	}
	return out, nil
}

func classifiedError(status int, body []byte) error {
	return wrapClassified(Classify(status, string(body)), extractCode(string(body)))
}

func wrapClassified(classified providers.ClassifiedError, code string) error {
	return &providers.Error{
		Kind:    classified.Kind,
		Status:  classified.Status,
		Message: classified.Message,
		Code:    code,
	}
}

func extractCode(body string) string {
	var env struct {
		Code any `json:"code"`
	}
	if json.Unmarshal([]byte(body), &env) != nil || env.Code == nil {
		return ""
	}
	switch v := env.Code.(type) {
	case nil:
		return ""
	case float64:
		return fmt.Sprintf("%.0f", v)
	case string:
		return strings.TrimSpace(v)
	default:
		text := strings.TrimSpace(fmt.Sprint(v))
		if text == "" || text == "<nil>" {
			return ""
		}
		return text
	}
}

// Classify 把 Trae 的 HTTP/错误 body 映射到内部分类体系。
func Classify(status int, body string) providers.ClassifiedError {
	text := strings.ToLower(body)
	code := extractCode(body)
	if accounts.IsPromptLimitText(body) {
		return providers.ClassifiedError{Kind: accounts.KindInvalidRequest, Status: 400, Message: strings.TrimSpace(body)}
	}
	switch {
	case code == "1001" || status == 401 || strings.Contains(text, "unauthorized"):
		return providers.ClassifiedError{Kind: accounts.KindAuth, Status: 401, Message: firstNonEmpty(strings.TrimSpace(body), "session dead; re-login required")}
	case code == "1005" || (strings.Contains(text, "1005") && strings.Contains(text, "plan")):
		return providers.ClassifiedError{Kind: accounts.KindQuota, Status: 429, Message: firstNonEmpty(strings.TrimSpace(body), "plan limit")}
	case code == "4008":
		return providers.ClassifiedError{Kind: accounts.KindQuota, Status: 429, Message: firstNonEmpty(strings.TrimSpace(body), "solo credits exhausted")}
	case code == "4001":
		return providers.ClassifiedError{Kind: accounts.KindInvalidRequest, Status: 400, Message: firstNonEmpty(strings.TrimSpace(body), "model or ide version mismatch")}
	case code == "4011":
		return providers.ClassifiedError{Kind: accounts.KindRateLimit, Status: 429, Message: firstNonEmpty(strings.TrimSpace(body), "hard rate limit")}
	case code == "4023":
		// 业务码 4023 = 上游拒绝了请求 body 本身（例如它不原生支持的
		// agent/thinking 字段）。账号是健康的，同样的 body 在任何其他账号上
		// 也会失败，因此这是不可重试的无效请求，绝非账号故障转移。
		return providers.ClassifiedError{Kind: accounts.KindInvalidRequest, Status: 400, Message: firstNonEmpty(strings.TrimSpace(body), "request body rejected by upstream (4023)")}
	case status == 429 || strings.Contains(text, "too many requests"):
		return providers.ClassifiedError{Kind: accounts.KindRateLimit, Status: 429, Message: strings.TrimSpace(body)}
	case status == 404:
		return providers.ClassifiedError{Kind: accounts.KindUnavailable, Status: 404, Message: strings.TrimSpace(body)}
	case status == 400 || accounts.IsPromptLimitText(body) || accounts.IsInvalidRequestText(body):
		return providers.ClassifiedError{Kind: accounts.KindInvalidRequest, Status: firstNonEmptyStatus(status, 400), Message: strings.TrimSpace(body)}
	case status >= 500:
		return providers.ClassifiedError{Kind: accounts.KindUnavailable, Status: status, Message: strings.TrimSpace(body)}
	}
	if code != "" && code != "0" && status < 300 {
		if code == "9074" {
			return providers.ClassifiedError{}
		}
		return providers.ClassifiedError{Kind: accounts.KindUnavailable, Status: 502, Message: strings.TrimSpace(body)}
	}
	return providers.ClassifiedError{}
}

func firstNonEmptyStatus(status, fallback int) int {
	if status >= 400 {
		return status
	}
	return fallback
}

func (c *Client) Probe(ctx context.Context, accountID string) (providers.AccountHealth, error) {
	credential, err := c.credential(ctx, accountID)
	if err != nil {
		return providers.AccountHealth{LastError: err.Error()}, nil
	}
	if !credential.Ready() {
		msg := "trae credential incomplete; re-login required"
		return providers.AccountHealth{UID: credential.UID, LastError: msg}, nil
	}
	return providers.AccountHealth{
		Ready: true,
		Hot:   true,
		UID:   credential.UID,
	}, nil
}

func (c *Client) Quota(ctx context.Context, accountID string) (*providers.QuotaInfo, error) {
	credential, err := c.credential(ctx, accountID)
	if err != nil {
		return nil, err
	}
	general, _, err := c.UserEntUsage(ctx, accountID, credential)
	if err != nil {
		return nil, err
	}
	// 只有 General 桶会被暴露。Work-only 桶会被解析
	// （见 parseEntitlementBuckets）但刻意不计入、不展示。
	remain, used, total := general.remain, general.used, general.total
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
	return &providers.QuotaInfo{
		Used:       float64(used),
		Total:      float64(total),
		Remaining:  float64(remain),
		Percentage: percentage,
		Unit:       QuotaUnit,
		Exceeded:   total > 0 && remain <= 0,
		FetchedAt:  time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (c *Client) UserEntUsage(ctx context.Context, accountID string, credential Credential) (general, work entitlementBucket, err error) {
	body, status, err := c.do(ctx, accountID, http.MethodPost, credential.BillingBase()+pathEntUsage, []byte("{}"),
		func(h http.Header) { SetUgHeaders(h, credential) })
	if err != nil {
		return entitlementBucket{}, entitlementBucket{}, err
	}
	if status >= 300 {
		return entitlementBucket{}, entitlementBucket{}, classifiedError(status, body)
	}
	general, work = parseEntitlementBuckets(body)
	if general.total == 0 && general.remain == 0 && general.used == 0 && work.total == 0 && work.remain == 0 && work.used == 0 {
		return entitlementBucket{}, entitlementBucket{}, fmt.Errorf("entitlement parse: empty pack list")
	}
	return general, work, nil
}

// entitlementEndpointWork 是标记 "Work 专属积分" 桶的 available_endpoint 值；
// 其他一切都属于 General。
const entitlementEndpointWork = 1

type entitlementBucket struct {
	remain int64
	used   int64
	total  int64
}

// parseEntitlementUsage 汇总每个 credit 包（两个桶都算）。
func parseEntitlementUsage(body []byte) (remain, used, total int64) {
	general, work := parseEntitlementBuckets(body)
	return general.remain + work.remain, general.used + work.used, general.total + work.total
}

// parseEntitlementBuckets 把 credit 包拆分为 General 桶（available_endpoint 0）
// 和 Work-only 桶（available_endpoint 1）。Trae 仪表盘将它们分别报告为
// 通用积分 / Work 专属。
func parseEntitlementBuckets(body []byte) (general, work entitlementBucket) {
	var root any
	if json.Unmarshal(body, &root) != nil {
		return entitlementBucket{}, entitlementBucket{}
	}
	for _, pack := range entitlementPacks(root) {
		limit := int64FromAny(lookupPath(pack, "entitlement_base_info", "quota", "credits_limit"))
		if limit <= 0 {
			limit = int64FromAny(lookupPath(pack, "quota", "credits_limit"))
		}
		if limit <= 0 {
			continue
		}
		consumed := int64FromAny(lookupPath(pack, "usage", "credits_amount"))
		if consumed < 0 {
			consumed = 0
		}
		left := limit - consumed
		if left < 0 {
			left = 0
		}
		bucket := &general
		if isWorkPack(pack) {
			bucket = &work
		}
		bucket.total += limit
		bucket.used += consumed
		bucket.remain += left
	}
	return general, work
}

// isWorkPack 报告某个 credit 包是否属于 Work-only 桶。
func isWorkPack(pack any) bool {
	value := lookupPath(pack, "entitlement_base_info", "available_endpoint")
	if value == nil {
		value = lookupPath(pack, "available_endpoint")
	}
	return int64FromAny(value) == entitlementEndpointWork
}

func entitlementPacks(root any) []any {
	switch typed := root.(type) {
	case []any:
		return typed
	case map[string]any:
		for _, key := range []string{"user_entitlement_pack_list", "data", "Result", "result"} {
			if nested, ok := typed[key]; ok {
				if packs := entitlementPacks(nested); len(packs) > 0 {
					return packs
				}
			}
		}
	}
	return nil
}

func lookupPath(value any, keys ...string) any {
	current := value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		next, ok := object[key]
		if !ok {
			found := false
			for existing, nested := range object {
				if strings.EqualFold(existing, key) {
					next = nested
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}
		current = next
	}
	return current
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case json.Number:
		n, _ := typed.Int64()
		return n
	case string:
		n, _ := json.Number(strings.TrimSpace(typed)).Int64()
		return n
	default:
		return 0
	}
}

func (c *Client) CheckinStatus(ctx context.Context, accountID string, credential Credential) ([]byte, error) {
	body, status, err := c.do(ctx, accountID, http.MethodPost, credential.BillingBase()+pathCheckinStatus, []byte("{}"),
		func(h http.Header) { SetUgHeaders(h, credential) })
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, classifiedError(status, body)
	}
	return body, nil
}

func (c *Client) CheckinClaim(ctx context.Context, accountID string, credential Credential) ([]byte, error) {
	body, status, err := c.do(ctx, accountID, http.MethodPost, credential.BillingBase()+pathCheckinClaim, []byte("{}"),
		func(h http.Header) { SetUgHeaders(h, credential) })
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, classifiedError(status, body)
	}
	return body, nil
}

func (c *Client) Adapter() providers.Adapter {
	return providers.Adapter{
		ID:         "trae",
		Credential: credentialCodec{},
		Login:      c,
		Chat:       c,
		Models:     c,
		Classifier: classifier{},
		Prober:     c,
		Checkin:    c,
	}
}

type credentialCodec struct{}

func (credentialCodec) Validate(payload []byte) error { return ValidateCredential(payload) }

type classifier struct{}

func (classifier) Classify(status int, body string) providers.ClassifiedError {
	return Classify(status, body)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
