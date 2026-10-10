package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
	"agent2api/internal/proxy"
	"modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

// errDatabaseUnusable 标记“现有文件作为 SQLite 数据库已损坏”（不是数据库 /
// 页结构畸形）的失败。它被有意与其他所有打开失败区分开：被锁定、权限被拒或
// 其他不可访问的文件必须暴露给运维人员，而诸如校验和不匹配之类的逻辑迁移
// 错误不得触发静默重建、把 schema 丢掉。
var errDatabaseUnusable = errors.New("sqlite database unusable")

// 表示“该文件不是可用数据库”的 SQLite 主结果码。只有这些才足以把文件隔离；
// 见 openStoreOnce。
// https://www.sqlite.org/rescode.html
const (
	sqliteCorrupt = 11 // SQLITE_CORRUPT：文件是数据库，但某个页结构畸形
	sqliteNotADB  = 26 // SQLITE_NOTADB：文件头不是数据库
)

// sqliteBusyTimeoutMS 是一条语句在放弃并返回 SQLITE_BUSY 之前，等待竞争锁的
// 时长。它必须在探测之前生效：否则旧进程仍在关闭（或容器重启重叠）的那段
// 较宽窗口，会把一个短暂锁变成一次失败的探测。
const sqliteBusyTimeoutMS = 5000

// sqliteDSN 为 path 构建 modernc.org/sqlite 的 DSN。连接 pragma 放在 DSN 里，
// 而不是在探测之后另外发一条 Exec，这样它们会在连接创建时（即 probeSQLite
// 的第一条语句之前）就被应用，并且在连接池打开的每一个连接上都会重新应用，
// 而不只是第一个连接。这些都是该驱动已验证的简写键，因此写错的值会在任何
// 语句执行之前就让连接失败；驱动会先于其他键应用 _busy_timeout，因此该超时
// 也覆盖 WAL 转换过程。
//
// 驱动会把不带 "file:" 前缀的 DSN 当作普通文件名，并从第一个 "?" 起截断
// 后面所有内容，因此 path 本身不得包含 "?"。所有调用方都用 filepath.Join
// 构建 path（见 internal/app），所以这一点成立。
func sqliteDSN(path string) string {
	return fmt.Sprintf("%s?_busy_timeout=%d&_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=1",
		path, sqliteBusyTimeoutMS)
}

func OpenStore(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("sqlite path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}
	store, err := openStoreOnce(path)
	if err == nil {
		return store, nil
	}
	if !errors.Is(err, errDatabaseUnusable) {
		// 被锁定、权限被拒或其他不可访问的文件并不是损坏的文件。上报它并
		// 不动那些字节：在这里隔离会把一个健康的数据库移走、换回一个空库，
		// 那就是静默的数据丢失。
		return nil, err
	}
	// 文件确实已损坏（SQLITE_NOTADB / SQLITE_CORRUPT）。把损坏的字节移走
	//（保留下来以便诊断），并重建一个空数据库，而不是拒绝启动：一个无法
	// 启动的网关，运维人员根本无从恢复，而它背后的账号数据是可以重新导入的。
	// 仅在真正损坏时才做这个取舍。
	moved, quarantineErr := quarantineUnusableDatabase(path)
	if quarantineErr != nil {
		return nil, fmt.Errorf("%w (quarantine failed: %w)", err, quarantineErr)
	}
	log.Printf("[store] database at %s is damaged (%v); moved %v aside and rebuilding", path, err, moved)
	retried, retryErr := openStoreOnce(path)
	if retryErr != nil {
		return nil, fmt.Errorf("open sqlite after quarantine: %w", retryErr)
	}
	return retried, nil
}

func openStoreOnce(path string) (*Store, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	// 可读性探测：健康（或仅仅为空）的文件会返回行数，而结构损坏的文件会在
	// 这里以 SQLITE_NOTADB / SQLITE_CORRUPT 失败。其他所有失败（锁定、权限、
	// I/O）都按原样返回；只有真正的损坏才可以被隔离。
	if err := probeSQLite(db); err != nil {
		db.Close()
		if isCorruption(err) {
			return nil, fmt.Errorf("%w: %v", errDatabaseUnusable, err)
		}
		return nil, fmt.Errorf("probe sqlite: %w", err)
	}
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return store, nil
}

// isCorruption 报告 err 是否是 SQLite 在说“该文件不是可用数据库”
// （SQLITE_NOTADB）或“其中的某个页结构畸形”（SQLITE_CORRUPT）。
//
// 判断依据是驱动的结果码，而非消息文本：modernc.org/sqlite 的错误会暴露
// Code()，扩展码会被掩码到其主值，因此像 SQLITE_CORRUPT_INDEX 这样的变体
// 仍能被识别。无法归属到某个码的错误绝不按损坏处理 —— 保守失败（fail
// closed）至多导致一个网关无法启动，而误判会把一个健康的数据库移走。
func isCorruption(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code() & 0xff {
	case sqliteCorrupt, sqliteNotADB:
		return true
	default:
		return false
	}
}

// probeSQLite 读取 schema 头部。结构损坏的文件在这里失败
// （SQLITE_NOTADB / SQLITE_CORRUPT），而健康或为空的文件则成功。
func probeSQLite(db *sql.DB) error {
	var tables int
	return db.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&tables)
}

// quarantineStamp 返回一个文件名安全、纳秒精度的 UTC 时间戳。此前的秒级精度
// 格式在同一秒内发生两次隔离时就会冲突。
func quarantineStamp() string {
	return time.Now().UTC().Format("20060102T150405.000000000Z")
}

// quarantineUnusableDatabase 把数据库及其附属文件重命名为
// *.corrupt-<stamp>，保留损坏的字节以便诊断，并把原路径空出来给新的数据库。
// 不删除任何东西。
func quarantineUnusableDatabase(path string) ([]string, error) {
	return quarantineUnusableDatabaseWith(path, quarantineStamp(), os.Rename)
}

// quarantineUnusableDatabaseWith 是注入了时间戳与重命名操作的
// quarantineUnusableDatabase，以便测试回滚路径。
//
// 移动是全有或全无的：若任一次重命名失败，已移走的文件会被重命名回去，
// 而如果该回滚也失败，错误会列出遗留的每一条路径，以便运维人员手工找回。
func quarantineUnusableDatabaseWith(path, stamp string, rename func(oldpath, newpath string) error) ([]string, error) {
	type move struct{ src, dst string }
	var done []move
	moved := make([]string, 0, 4)
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		src := path + suffix
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := unusedQuarantineName(src, stamp)
		if err := rename(src, dst); err != nil {
			var stuck []string
			restored := 0
			for i := len(done) - 1; i >= 0; i-- {
				if rbErr := rename(done[i].dst, done[i].src); rbErr != nil {
					stuck = append(stuck, fmt.Sprintf("%s (restore failed: %v)", done[i].dst, rbErr))
				} else {
					restored++
				}
			}
			if len(stuck) > 0 {
				return nil, fmt.Errorf("quarantine %s: %w; rollback failed, files left moved: %s",
					src, err, strings.Join(stuck, ", "))
			}
			return nil, fmt.Errorf("quarantine %s: %w (rolled back %d already-moved file(s))",
				src, err, restored)
		}
		done = append(done, move{src: src, dst: dst})
		moved = append(moved, dst)
	}
	return moved, nil
}

// unusedQuarantineName 返回一个尚不存在的 .corrupt-<stamp> 名字。重命名到
// 已存在的文件上会静默覆盖先前的隔离文件，因此会追加数字后缀直到该名字空闲。
func unusedQuarantineName(src, stamp string) string {
	base := fmt.Sprintf("%s.corrupt-%s", src, stamp)
	dst := base
	for i := 1; ; i++ {
		if _, err := os.Lstat(dst); errors.Is(err, os.ErrNotExist) {
			return dst
		}
		dst = fmt.Sprintf("%s.%d", base, i)
	}
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB 暴露 SQLite 句柄，供那些在 Manager 的持久化 goroutine 仍在运行时关闭或
// 重新打开同一文件的测试使用。
func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// ReplaceDB 替换 SQLite 句柄。持久化失败的测试会在 Manager 仍在运行时关闭
// 原连接并重新打开同一文件。
func (s *Store) ReplaceDB(db *sql.DB) {
	if s == nil {
		return
	}
	s.db = db
}

func (s *Store) migrate(ctx context.Context) error {
	if err := s.runMigrations(ctx); err != nil {
		return err
	}
	// 在 schema 就位之后运行：旧值重写也会触及追加迁移所创建的列。
	return s.normalizeTimestampWidth(ctx)
}

func validateAccountProxy(providerID, region, raw string) error {
	return accounts.ValidateAccountProxy(providerID, region, raw)
}

func (s *Store) Create(ctx context.Context, input accounts.CreateAccount) (accounts.Account, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return accounts.Account{}, fmt.Errorf("account name required")
	}
	descriptor, region, err := providers.Resolve(input.Provider, input.Region)
	if err != nil {
		return accounts.Account{}, err
	}
	if err := validateAccountProxy(input.Provider, input.Region, input.ProxyURL); err != nil {
		return accounts.Account{}, err
	}
	maxInFlight := accounts.DefaultMaxInFlightValue(input.MaxInFlight)
	priority := accounts.DefaultPriorityValue(input.Priority)
	now := time.Now().UTC()
	dropSystemPrompt := accounts.DefaultDropSystemPrompt(input.DropSystemPrompt)
	autoCheckin := accounts.DefaultWorkBuddyAutoCheckin(input.WorkBuddyAutoCheckin)
	checkinTime, err := s.resolveWorkBuddyCheckinTime(ctx, input.WorkBuddyCheckinTime)
	if err != nil {
		return accounts.Account{}, err
	}
	override := strings.TrimSpace(input.CheckinTime)
	genericAutoCheckin := false
	if descriptor.ID == "workbuddy" {
		genericAutoCheckin = autoCheckin
		if override == "" {
			override = strings.TrimSpace(input.WorkBuddyCheckinTime)
		}
	}
	if input.AutoCheckin != nil {
		genericAutoCheckin = *input.AutoCheckin
	}
	if err := accounts.ValidateCheckinSettings(descriptor.ID, region.ID, genericAutoCheckin, override); err != nil {
		return accounts.Account{}, err
	}
	reserveCredits := int64Value(input.ReserveCredits)
	dailyTokenLimit := int64Value(input.DailyTokenLimit)
	dailyCreditLimit := int64Value(input.DailyCreditLimit)
	dailyModelTokenLimit := int64Value(input.DailyModelTokenLimit)
	if err := accounts.ValidateAccountGuards(reserveCredits, dailyTokenLimit, dailyCreditLimit, dailyModelTokenLimit); err != nil {
		return accounts.Account{}, err
	}
	if descriptor.ID == "workbuddy" {
		autoCheckin = genericAutoCheckin
		if override != "" {
			checkinTime = override
		}
	}
	account := accounts.Account{
		ID:                   newAccountID(),
		Name:                 name,
		Provider:             descriptor.ID,
		ProviderRegion:       region.ID,
		AuthType:             "none",
		Enabled:              input.Enabled,
		MaxInFlight:          maxInFlight,
		Priority:             priority,
		DropSystemPrompt:     dropSystemPrompt,
		WorkBuddyAutoCheckin: autoCheckin,
		WorkBuddyCheckinTime: checkinTime,
		AutoCheckin:          genericAutoCheckin,
		CheckinTime:          override,
		ProxyURL:             strings.TrimSpace(input.ProxyURL),
		ReserveCredits:       reserveCredits,
		DailyTokenLimit:      dailyTokenLimit,
		DailyCreditLimit:     dailyCreditLimit,
		DailyModelTokenLimit: dailyModelTokenLimit,
		Status:               "offline",
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	_, err = s.db.ExecContext(ctx, `
	INSERT INTO accounts (
	  id, name, provider, provider_region, auth_type, enabled, max_inflight, priority, drop_system_prompt,
	  workbuddy_auto_checkin, workbuddy_checkin_time, proxy_url, status, created_at, updated_at, auto_checkin, checkin_time
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		account.ID, account.Name, account.Provider, account.ProviderRegion, account.AuthType,
		account.Enabled, account.MaxInFlight, account.Priority, account.DropSystemPrompt,
		account.WorkBuddyAutoCheckin, account.WorkBuddyCheckinTime, account.ProxyURL, account.Status,
		formatTime(account.CreatedAt), formatTime(account.UpdatedAt),
		account.AutoCheckin, account.CheckinTime,
	)
	if err != nil {
		return accounts.Account{}, fmt.Errorf("create account: %w", err)
	}
	if err := s.upsertAccountGuards(ctx, account.ID, reserveCredits, dailyTokenLimit, dailyCreditLimit, dailyModelTokenLimit); err != nil {
		return accounts.Account{}, err
	}
	return account, nil
}

func (s *Store) Get(ctx context.Context, id string) (accounts.Account, error) {
	row := s.db.QueryRowContext(ctx, `
	SELECT a.id, a.name, a.provider, a.provider_region, a.remote_uid, a.auth_type, a.enabled, a.max_inflight, a.priority,
	       a.drop_system_prompt, a.workbuddy_auto_checkin, a.workbuddy_checkin_time, a.proxy_url, a.last_checkin_at, a.last_checkin_msg, a.last_checkin_status,
	       a.status, a.last_error, a.last_error_kind, a.cooldown_until, a.quota_json, a.created_at, a.updated_at, a.auto_checkin, a.checkin_time,
	       COALESCE(g.reserve_credits, 0), COALESCE(g.daily_token_limit, 0), COALESCE(g.daily_credit_limit, 0), COALESCE(g.daily_model_token_limit, 0)
	FROM accounts a LEFT JOIN account_guards g ON g.account_id = a.id WHERE a.id = ?`, strings.TrimSpace(id))
	account, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return accounts.Account{}, accounts.ErrAccountNotFound
	}
	if err != nil {
		return accounts.Account{}, fmt.Errorf("get account: %w", err)
	}
	return account, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanAccount(row rowScanner) (accounts.Account, error) {
	var account accounts.Account
	var cooldown, quotaJSON, created, updated sql.NullString
	err := row.Scan(
		&account.ID, &account.Name, &account.Provider, &account.ProviderRegion, &account.RemoteUID,
		&account.AuthType, &account.Enabled, &account.MaxInFlight, &account.Priority,
		&account.DropSystemPrompt, &account.WorkBuddyAutoCheckin, &account.WorkBuddyCheckinTime, &account.ProxyURL, &account.LastCheckinAt, &account.LastCheckinMsg, &account.LastCheckinStatus,
		&account.Status, &account.LastError, &account.LastErrorKind, &cooldown, &quotaJSON, &created, &updated,
		&account.AutoCheckin, &account.CheckinTime,
		&account.ReserveCredits, &account.DailyTokenLimit, &account.DailyCreditLimit, &account.DailyModelTokenLimit,
	)
	if err != nil {
		return accounts.Account{}, err
	}
	if account.ProviderRegion == "" {
		account.ProviderRegion = "global"
	}
	if account.WorkBuddyCheckinTime == "" {
		account.WorkBuddyCheckinTime = accounts.DefaultWorkBuddyCheckinTime
	}
	account.CreatedAt = parseTime(created.String)
	account.UpdatedAt = parseTime(updated.String)
	if cooldown.Valid && cooldown.String != "" {
		parsed := parseTime(cooldown.String)
		account.CooldownUntil = &parsed
	}
	if quotaJSON.Valid && quotaJSON.String != "" {
		var quota accounts.QuotaSnapshot
		if json.Unmarshal([]byte(quotaJSON.String), &quota) == nil {
			account.Quota = &quota
		}
	}
	return account, nil
}

func newAccountID() string {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("acc_%d", time.Now().UnixNano())
	}
	return "acc_" + hex.EncodeToString(raw)
}

// timestampLayout 以固定宽度渲染时间戳，使存储的文本按其时间先后顺序比较时
// 结果一致。
//
// time.RFC3339Nano 会裁剪小数部分的末尾零，从而破坏同一秒内的文本排序：
// "…40.1246Z" 会排在 "…40.12463Z" 之后，因为 'Z' > '3'。这些列上的每一处
// ORDER BY（以及回收旧 request logs 的有界 DELETE）都依赖文本顺序，因此
// 小数部分必须保持其宽度。旧行由 normalizeTimestampWidth 重写。
const timestampLayout = "2006-01-02T15:04:05.000000000Z"

// timestampColumns 列出由 formatTime 写入的每一个 TEXT 列。请让它与 schema
// 保持同步：这里缺失的列会保留旧宽度的值，从而只在该列上重新引入排序缺陷。
var timestampColumns = []struct{ table, column string }{
	{"accounts", "created_at"},
	{"accounts", "updated_at"},
	{"accounts", "cooldown_until"},
	{"accounts", "last_checkin_at"},
	{"account_credential_payloads", "updated_at"},
	{"account_credentials", "updated_at"},
	{"account_cooldowns", "down_until"},
	{"account_cooldowns", "updated_at"},
	{"account_model_free", "updated_at"},
	{"app_secrets", "created_at"},
	{"app_secrets", "updated_at"},
	{"checkin_records", "created_at"},
	{"growth_observations", "created_at"},
	{"model_catalog_snapshot", "updated_at"},
	{"model_settings", "updated_at"},
	{"provider_model_settings", "updated_at"},
	{"request_attempts", "started_at"},
	{"request_attempts", "finished_at"},
	{"request_logs", "created_at"},
	{"request_logs", "finished_at"},
	{"request_stream_diagnostics", "created_at"},
	{"request_stream_diagnostics", "finished_at"},
	{"request_timings", "created_at"},
	{"request_usage_details", "created_at"},
	{"schema_migrations", "applied_at"},
}

// canonicalTimestampGlob 匹配已处于 timestampLayout 形态的值。它是重写的
// 预过滤器，且有意做形态匹配而非宽度检查：一个恰好等于规范宽度、但并非规范
// 形态的值（比如 "+08:00" 这样的偏移形式）仍必须被重新渲染。
const canonicalTimestampGlob = "????-??-??T??:??:??.?????????Z"

// isTimestampColumn 报告某个 schema 列名是否承载时间戳，也是
// TestTimestampColumnsCoverEveryTimestampColumn 判断重写列表必须覆盖哪些列
// 的依据。
//
// 它是一条命名规则，而非语义规则：日后一个不叫 "<x>_at" 的时间戳列
// （valid_until、expires_on……）必须手工加入重写列表，因为该谓词不会把它
// 标为缺失。
func isTimestampColumn(name string) bool {
	return strings.HasSuffix(name, "_at") || name == "down_until" || name == "cooldown_until"
}

func formatTime(value time.Time) string {
	return value.UTC().Format(timestampLayout)
}

// monotonicTimestamp 返回严格晚于 prev 的时间戳串。
//
// 版本列就是写入时刻（见 LoadCredentialPayloadWithVersion / SaveCredentialPayloadIfUnchanged）。
// 在时间粒度较粗的平台上，两次写入可能落在同一时刻，从而得到相同的版本串——
// 于是"比较并写入"会把并发的第二次写入误判为无竞态（或反之），令牌轮换的
// 竞态抑制随之失效。这里保证版本严格单调：同刻则比 prev 前进 1ns。
func monotonicTimestamp(now time.Time, prev string) string {
	stamp := formatTime(now)
	if prev == "" || stamp > prev {
		return stamp
	}
	if parsed, err := time.Parse(time.RFC3339Nano, prev); err == nil {
		return formatTime(parsed.Add(time.Nanosecond))
	}
	return stamp
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func (s *Store) List(ctx context.Context) ([]accounts.Account, error) {
	rows, err := s.db.QueryContext(ctx, `
	SELECT a.id, a.name, a.provider, a.provider_region, a.remote_uid, a.auth_type, a.enabled, a.max_inflight, a.priority,
	       a.drop_system_prompt, a.workbuddy_auto_checkin, a.workbuddy_checkin_time, a.proxy_url, a.last_checkin_at, a.last_checkin_msg, a.last_checkin_status,
	       a.status, a.last_error, a.last_error_kind, a.cooldown_until, a.quota_json, a.created_at, a.updated_at, a.auto_checkin, a.checkin_time,
	       COALESCE(g.reserve_credits, 0), COALESCE(g.daily_token_limit, 0), COALESCE(g.daily_credit_limit, 0), COALESCE(g.daily_model_token_limit, 0)
	FROM accounts a LEFT JOIN account_guards g ON g.account_id = a.id ORDER BY a.created_at, a.id`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	var accounts []accounts.Account
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (s *Store) Update(ctx context.Context, id string, input accounts.UpdateAccount) error {
	account, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if name := strings.TrimSpace(input.Name); name != "" {
		account.Name = name
	}
	if input.Enabled != nil {
		account.Enabled = *input.Enabled
	}
	if input.MaxInFlight != nil && *input.MaxInFlight > 0 {
		account.MaxInFlight = *input.MaxInFlight
	}
	if input.Priority != nil && *input.Priority > 0 {
		account.Priority = *input.Priority
	}
	if input.DropSystemPrompt != nil {
		account.DropSystemPrompt = *input.DropSystemPrompt
	}
	if input.WorkBuddyAutoCheckin != nil {
		account.WorkBuddyAutoCheckin = *input.WorkBuddyAutoCheckin
		if account.Provider == "workbuddy" {
			account.AutoCheckin = *input.WorkBuddyAutoCheckin
		}
	}
	if input.WorkBuddyCheckinTime != nil {
		account.WorkBuddyCheckinTime, err = s.resolveWorkBuddyCheckinTime(ctx, *input.WorkBuddyCheckinTime)
		if err != nil {
			return err
		}
		if account.Provider == "workbuddy" {
			account.CheckinTime = strings.TrimSpace(*input.WorkBuddyCheckinTime)
		}
	}
	if input.AutoCheckin != nil {
		account.AutoCheckin = *input.AutoCheckin
	}
	if input.CheckinTime != nil {
		account.CheckinTime = strings.TrimSpace(*input.CheckinTime)
	}
	if err := accounts.ValidateCheckinSettings(account.Provider, account.ProviderRegion, account.AutoCheckin, account.CheckinTime); err != nil {
		return err
	}
	if account.Provider == "workbuddy" {
		account.WorkBuddyAutoCheckin = account.AutoCheckin
		account.WorkBuddyCheckinTime, err = accounts.ResolveCheckinTime(ctx, s, account)
		if err != nil {
			return err
		}
	}
	if input.ProxyURL != nil {
		proxyURL := proxy.Preserve(account.ProxyURL, *input.ProxyURL)
		if err := validateAccountProxy(account.Provider, account.ProviderRegion, proxyURL); err != nil {
			return err
		}
		account.ProxyURL = proxyURL
	}
	if input.ReserveCredits != nil {
		account.ReserveCredits = *input.ReserveCredits
	}
	if input.DailyTokenLimit != nil {
		account.DailyTokenLimit = *input.DailyTokenLimit
	}
	if input.DailyCreditLimit != nil {
		account.DailyCreditLimit = *input.DailyCreditLimit
	}
	if input.DailyModelTokenLimit != nil {
		account.DailyModelTokenLimit = *input.DailyModelTokenLimit
	}
	if err := accounts.ValidateAccountGuards(account.ReserveCredits, account.DailyTokenLimit, account.DailyCreditLimit, account.DailyModelTokenLimit); err != nil {
		return err
	}
	account.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
	UPDATE accounts SET name = ?, enabled = ?, max_inflight = ?, priority = ?, drop_system_prompt = ?,
	                    workbuddy_auto_checkin = ?, workbuddy_checkin_time = ?, proxy_url = ?, updated_at = ?, auto_checkin = ?, checkin_time = ?
	WHERE id = ?`, account.Name, account.Enabled, account.MaxInFlight, account.Priority, account.DropSystemPrompt,
		account.WorkBuddyAutoCheckin, account.WorkBuddyCheckinTime, account.ProxyURL, formatTime(account.UpdatedAt), account.AutoCheckin, account.CheckinTime, account.ID)
	if err != nil {
		return fmt.Errorf("update account: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return accounts.ErrAccountNotFound
	}
	if input.ReserveCredits != nil || input.DailyTokenLimit != nil || input.DailyCreditLimit != nil || input.DailyModelTokenLimit != nil {
		if err := s.upsertAccountGuards(ctx, account.ID, account.ReserveCredits, account.DailyTokenLimit, account.DailyCreditLimit, account.DailyModelTokenLimit); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) WorkBuddyCheckinTimeDefault(ctx context.Context) string {
	value, ok, err := s.GetSecret(ctx, accounts.WorkBuddyCheckinTimeSecret)
	if err != nil || !ok {
		return accounts.DefaultWorkBuddyCheckinTime
	}
	normalized, err := accounts.NormalizeWorkBuddyCheckinTime(value)
	if err != nil {
		return accounts.DefaultWorkBuddyCheckinTime
	}
	return normalized
}

func (s *Store) resolveWorkBuddyCheckinTime(ctx context.Context, value string) (string, error) {
	return accounts.ResolveWorkBuddyCheckinTime(value, s.WorkBuddyCheckinTimeDefault(ctx))
}

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return accounts.ErrAccountNotFound
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM account_guards WHERE account_id = ?`, strings.TrimSpace(id)); err != nil {
		return fmt.Errorf("delete account guards: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM account_model_free WHERE account_id = ?`, strings.TrimSpace(id)); err != nil {
		return fmt.Errorf("delete account model free: %w", err)
	}
	return nil
}

func (s *Store) SetModelContext(ctx context.Context, modelID string, contextLength int) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return fmt.Errorf("model id required")
	}
	if err := accounts.ValidateModelContextLength(contextLength); err != nil {
		return err
	}
	if contextLength == 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM model_settings WHERE model_id = ?`, modelID)
		if err != nil {
			return fmt.Errorf("delete model context: %w", err)
		}
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO model_settings (model_id, context_length, updated_at) VALUES (?, ?, ?)
ON CONFLICT(model_id) DO UPDATE SET context_length=excluded.context_length, updated_at=excluded.updated_at`,
		modelID, contextLength, formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("save model context: %w", err)
	}
	return nil
}

func (s *Store) GetModelContext(ctx context.Context, modelID string) (int, bool, error) {
	var contextLength int
	err := s.db.QueryRowContext(ctx, `SELECT context_length FROM model_settings WHERE model_id = ?`, strings.TrimSpace(modelID)).Scan(&contextLength)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get model context: %w", err)
	}
	return contextLength, true, nil
}

func (s *Store) ListModelContexts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT model_id, context_length FROM model_settings`)
	if err != nil {
		return nil, fmt.Errorf("list model contexts: %w", err)
	}
	defer rows.Close()
	result := make(map[string]int)
	for rows.Next() {
		var modelID string
		var contextLength int
		if err := rows.Scan(&modelID, &contextLength); err != nil {
			return nil, fmt.Errorf("scan model context: %w", err)
		}
		result[modelID] = contextLength
	}
	return result, rows.Err()
}

func providerModelKey(provider, modelID string) (string, string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelID = strings.TrimSpace(modelID)
	if provider == "" || modelID == "" {
		return "", "", fmt.Errorf("provider and model id required")
	}
	return provider, modelID, nil
}

func (s *Store) SetProviderModelSetting(ctx context.Context, provider, modelID string, setting accounts.ProviderModelSetting) error {
	provider, modelID, err := providerModelKey(provider, modelID)
	if err != nil {
		return err
	}
	setting.ReasoningEffort = strings.ToLower(strings.TrimSpace(setting.ReasoningEffort))
	if !setting.MaxMode && setting.ReasoningEffort == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM provider_model_settings WHERE provider = ? AND model_id = ?`, provider, modelID)
		if err != nil {
			return fmt.Errorf("delete provider model setting: %w", err)
		}
		return nil
	}
	maxMode := 0
	if setting.MaxMode {
		maxMode = 1
	}
	_, err = s.db.ExecContext(ctx, `
	INSERT INTO provider_model_settings (provider, model_id, max_mode, reasoning_effort, updated_at) VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(provider, model_id) DO UPDATE SET max_mode=excluded.max_mode, reasoning_effort=excluded.reasoning_effort, updated_at=excluded.updated_at`,
		provider, modelID, maxMode, setting.ReasoningEffort, formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("save provider model setting: %w", err)
	}
	return nil
}

func (s *Store) GetProviderModelSetting(ctx context.Context, provider, modelID string) (accounts.ProviderModelSetting, error) {
	provider, modelID, err := providerModelKey(provider, modelID)
	if err != nil {
		return accounts.ProviderModelSetting{}, err
	}
	var maxMode int
	var effort string
	err = s.db.QueryRowContext(ctx, `SELECT max_mode, reasoning_effort FROM provider_model_settings WHERE provider = ? AND model_id = ?`, provider, modelID).Scan(&maxMode, &effort)
	if errors.Is(err, sql.ErrNoRows) {
		return accounts.ProviderModelSetting{}, nil
	}
	if err != nil {
		return accounts.ProviderModelSetting{}, fmt.Errorf("get provider model setting: %w", err)
	}
	return accounts.ProviderModelSetting{MaxMode: maxMode != 0, ReasoningEffort: strings.TrimSpace(effort)}, nil
}

func (s *Store) SetProviderModelMaxMode(ctx context.Context, provider, modelID string, maxMode bool) error {
	setting, err := s.GetProviderModelSetting(ctx, provider, modelID)
	if err != nil {
		return err
	}
	setting.MaxMode = maxMode
	return s.SetProviderModelSetting(ctx, provider, modelID, setting)
}

func (s *Store) GetProviderModelMaxMode(ctx context.Context, provider, modelID string) (bool, error) {
	setting, err := s.GetProviderModelSetting(ctx, provider, modelID)
	if err != nil {
		return false, err
	}
	return setting.MaxMode, nil
}

func (s *Store) ListProviderModelMaxModes(ctx context.Context, provider string) (map[string]bool, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return nil, fmt.Errorf("provider required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT model_id, max_mode FROM provider_model_settings WHERE provider = ?`, provider)
	if err != nil {
		return nil, fmt.Errorf("list provider model settings: %w", err)
	}
	defer rows.Close()
	result := make(map[string]bool)
	for rows.Next() {
		var modelID string
		var maxMode int
		if err := rows.Scan(&modelID, &maxMode); err != nil {
			return nil, fmt.Errorf("scan provider model setting: %w", err)
		}
		result[modelID] = maxMode != 0
	}
	return result, rows.Err()
}

func (s *Store) GetSecret(ctx context.Context, name string) (string, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false, fmt.Errorf("secret name required")
	}
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name = ?`, name).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get secret: %w", err)
	}
	return value, true, nil
}

func (s *Store) SetSecret(ctx context.Context, name, value string) error {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name == "" {
		return fmt.Errorf("secret name required")
	}
	if value == "" {
		return fmt.Errorf("secret value required")
	}
	now := formatTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO app_secrets (name, value, created_at, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		name, value, now, now)
	if err != nil {
		return fmt.Errorf("save secret: %w", err)
	}
	return nil
}

// SetSecretOrEmpty 存储一个 secret，允许显式空值持久化。当“已清空”是一种
// 有意义、必须与“从未配置”区分开的状态时使用它（例如全局代理：行不存在会
// 触发从环境变量做首次启动引导）。
func (s *Store) SetSecretOrEmpty(ctx context.Context, name, value string) error {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name == "" {
		return fmt.Errorf("secret name required")
	}
	now := formatTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO app_secrets (name, value, created_at, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		name, value, now, now)
	if err != nil {
		return fmt.Errorf("save secret: %w", err)
	}
	return nil
}

func (s *Store) DeleteSecret(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("secret name required")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM app_secrets WHERE name = ?`, name); err != nil {
		return fmt.Errorf("delete secret: %w", err)
	}
	return nil
}

func (s *Store) SaveCredentialPayload(ctx context.Context, accountID, format string, payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("credential payload required")
	}
	account, err := s.Get(ctx, accountID)
	if err != nil {
		return err
	}
	if err := providers.ValidateCredentialFormat(account.Provider, format); err != nil {
		return err
	}
	now := formatTime(time.Now().UTC())
	_, err = s.db.ExecContext(ctx, `
INSERT INTO account_credential_payloads (account_id, format, payload, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(account_id) DO UPDATE SET format=excluded.format, payload=excluded.payload, updated_at=excluded.updated_at`,
		accountID, format, payload, now)
	if err != nil {
		return fmt.Errorf("save credential payload: %w", err)
	}
	return nil
}

func (s *Store) LoadCredentialPayload(ctx context.Context, accountID string) (string, []byte, error) {
	var format string
	var payload []byte
	err := s.db.QueryRowContext(ctx, `
SELECT format, payload FROM account_credential_payloads WHERE account_id = ?`, accountID).
		Scan(&format, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, accounts.ErrAccountNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("load credential payload: %w", err)
	}
	return format, payload, nil
}

// LoadCredentialPayloadWithVersion 是 LoadCredentialPayload 加上标识该版本的
// 时间戳。把这个时间戳交给 SaveCredentialPayloadIfUnchanged，即可把回写变成
// 一次“比较后写入”。
func (s *Store) LoadCredentialPayloadWithVersion(ctx context.Context, accountID string) (string, []byte, string, error) {
	var format, updatedAt string
	var payload []byte
	err := s.db.QueryRowContext(ctx, `
SELECT format, payload, updated_at FROM account_credential_payloads WHERE account_id = ?`, accountID).
		Scan(&format, &payload, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, "", accounts.ErrAccountNotFound
	}
	if err != nil {
		return "", nil, "", fmt.Errorf("load credential payload: %w", err)
	}
	return format, payload, updatedAt, nil
}

// SaveCredentialPayloadIfUnchanged 仅在已存储的行仍是调用方读到的那个版本时
// 才写入载荷。它会报告写入是否发生：false 表示更新的载荷先落地了，调用方的
// 值已过期。
//
// 凭证端点会轮换 refresh token，因此竞态中落败的刷新若无条件覆盖，会使胜者
// 刚存下的 token 失效。
func (s *Store) SaveCredentialPayloadIfUnchanged(ctx context.Context, accountID, format string, payload []byte, expectedUpdatedAt string) (bool, error) {
	if len(payload) == 0 {
		return false, fmt.Errorf("credential payload required")
	}
	account, err := s.Get(ctx, accountID)
	if err != nil {
		return false, err
	}
	if err := providers.ValidateCredentialFormat(account.Provider, format); err != nil {
		return false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var current string
	err = tx.QueryRowContext(ctx, `SELECT updated_at FROM account_credential_payloads WHERE account_id = ?`, accountID).Scan(&current)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// 尚未存储任何载荷：这是第一次写入，没什么可丢的。
	case err != nil:
		return false, fmt.Errorf("read credential version: %w", err)
	case current != expectedUpdatedAt:
		return false, nil
	}

	now := monotonicTimestamp(time.Now().UTC(), current)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO account_credential_payloads (account_id, format, payload, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(account_id) DO UPDATE SET format=excluded.format, payload=excluded.payload, updated_at=excluded.updated_at`,
		accountID, format, payload, now); err != nil {
		return false, fmt.Errorf("save credential payload: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) SaveQuota(ctx context.Context, id string, quota *accounts.QuotaSnapshot) error {
	if quota == nil {
		return nil
	}
	payload, err := json.Marshal(quota)
	if err != nil {
		return fmt.Errorf("marshal quota: %w", err)
	}
	status := "ready"
	if quota.Exceeded {
		status = "quota_exhausted"
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE accounts SET quota_json = ?, status = CASE
  WHEN ? = 'quota_exhausted' THEN 'quota_exhausted'
  WHEN status = 'quota_exhausted' THEN 'ready'
  ELSE status
END, updated_at = ?
WHERE id = ?`, string(payload), status, formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("save quota: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return accounts.ErrAccountNotFound
	}
	return nil
}
func (s *Store) Observe(ctx context.Context, id, remoteUID, status, lastError, lastKind string) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE accounts SET remote_uid = ?, status = CASE
  WHEN accounts.status = 'quota_exhausted' AND ? = 'ready' THEN accounts.status
  ELSE ?
END, last_error = ?, last_error_kind = ?, updated_at = ?
WHERE id = ?`, remoteUID, status, status, lastError, lastKind, formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("observe account: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return accounts.ErrAccountNotFound
	}
	return nil
}

func newCheckinRecordID() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("checkin_%d", time.Now().UnixNano())
	}
	return "checkin_" + hex.EncodeToString(raw)
}

func (s *Store) RecordCheckin(ctx context.Context, id, status, msg string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	accountID := strings.TrimSpace(id)
	status = strings.TrimSpace(status)
	if status == "" {
		status = "success"
	}
	message := strings.TrimSpace(msg)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin record checkin: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE accounts SET last_checkin_at = ?, last_checkin_msg = ?, last_checkin_status = ?, updated_at = ?
WHERE id = ?`, formatTime(at), message, status, formatTime(time.Now().UTC()), accountID)
	if err != nil {
		return fmt.Errorf("record checkin: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return accounts.ErrAccountNotFound
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO checkin_records (id, account_id, status, message, created_at) VALUES (?, ?, ?, ?, ?)`,
		newCheckinRecordID(), accountID, status, message, formatTime(at)); err != nil {
		return fmt.Errorf("insert checkin record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit checkin record: %w", err)
	}
	return nil
}

func (s *Store) ListCheckinRecords(ctx context.Context, accountID string, limit int) ([]accounts.CheckinRecord, error) {
	if _, err := s.Get(ctx, accountID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, account_id, status, message, created_at
FROM checkin_records WHERE account_id = ?
ORDER BY created_at DESC, id DESC LIMIT ?`, strings.TrimSpace(accountID), limit)
	if err != nil {
		return nil, fmt.Errorf("list checkin records: %w", err)
	}
	defer rows.Close()
	records := make([]accounts.CheckinRecord, 0)
	for rows.Next() {
		var record accounts.CheckinRecord
		var created string
		if err := rows.Scan(&record.ID, &record.AccountID, &record.Status, &record.Message, &created); err != nil {
			return nil, fmt.Errorf("scan checkin record: %w", err)
		}
		record.CreatedAt = parseTime(created)
		records = append(records, record)
	}
	return records, rows.Err()
}

// RecordGrowthObservations 把一次成长领取运行的结果追加到成长日志。空输入
// 是 no-op；账号必须存在。
func (s *Store) RecordGrowthObservations(ctx context.Context, accountID string, observations []accounts.GrowthObservation) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || len(observations) == 0 {
		return nil
	}
	if _, err := s.Get(ctx, accountID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin record growth observations: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, observation := range observations {
		at := observation.At
		if at.IsZero() {
			at = now
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO growth_observations (account_id, target, action, status, message, credit, energy, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			accountID, strings.TrimSpace(observation.Target), strings.TrimSpace(observation.Action),
			strings.TrimSpace(observation.Status), strings.TrimSpace(observation.Message),
			observation.Credit, observation.Energy, formatTime(at)); err != nil {
			return fmt.Errorf("insert growth observation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit growth observations: %w", err)
	}
	return nil
}

// ListGrowthObservations 返回最新的成长记录在前。
func (s *Store) ListGrowthObservations(ctx context.Context, accountID string, limit int) ([]accounts.GrowthObservation, error) {
	if _, err := s.Get(ctx, accountID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, account_id, target, action, status, message, credit, energy, created_at
FROM growth_observations WHERE account_id = ?
ORDER BY id DESC LIMIT ?`, strings.TrimSpace(accountID), limit)
	if err != nil {
		return nil, fmt.Errorf("list growth observations: %w", err)
	}
	defer rows.Close()
	observations := make([]accounts.GrowthObservation, 0)
	for rows.Next() {
		var observation accounts.GrowthObservation
		var created string
		if err := rows.Scan(&observation.ID, &observation.AccountID, &observation.Target, &observation.Action,
			&observation.Status, &observation.Message, &observation.Credit, &observation.Energy, &created); err != nil {
			return nil, fmt.Errorf("scan growth observation: %w", err)
		}
		observation.At = parseTime(created)
		observations = append(observations, observation)
	}
	return observations, rows.Err()
}

// canonicalCooldownModel 为存储规范化一个模型 key。与 accounts.CanonicalModelID
// 不同，它保留空 key 不动："" 是一个真实的主键值，含义为“账号级”，而
// accounts.CanonicalModelID 会把它改写成 "auto"，把账号行并入模型命名空间。
func canonicalCooldownModel(model string) string {
	if strings.TrimSpace(model) == "" {
		return ""
	}
	return accounts.CanonicalModelID(model)
}

// SaveCooldowns 替换某个账号已持久化的冷却集合。到期时间已过的行会被丢弃
// 而不写入，因此长期运行的进程不会累积过期的条目。
func (s *Store) SaveCooldowns(ctx context.Context, accountID string, rows []accounts.CooldownRow) error {
	if accountID == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin cooldown save: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_cooldowns WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("clear cooldowns: %w", err)
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if row.DownUntil.IsZero() || !now.Before(row.DownUntil) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO account_cooldowns (account_id, model, down_until, backoff_level, kind, message, model_kind, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id, model) DO UPDATE SET
  down_until = excluded.down_until,
  backoff_level = excluded.backoff_level,
  kind = excluded.kind,
  message = excluded.message,
  model_kind = excluded.model_kind,
  updated_at = excluded.updated_at`,
			accountID, canonicalCooldownModel(row.Model), formatTime(row.DownUntil), clampBackoffLevel(row.BackoffLevel),
			row.Kind, row.Message, row.ModelKind, formatTime(now)); err != nil {
			return fmt.Errorf("save cooldown: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_cooldowns WHERE account_id = ? AND down_until <= ?`,
		accountID, formatTime(now)); err != nil {
		return fmt.Errorf("prune cooldowns: %w", err)
	}
	return tx.Commit()
}

// LoadCooldowns 返回所有到期时间仍在未来的冷却。过期的行会在同一次操作中
// 被清理，以免反复重启时重复读取陈旧条目。
func (s *Store) LoadCooldowns(ctx context.Context) ([]accounts.CooldownRow, error) {
	now := formatTime(time.Now().UTC())
	if _, err := s.db.ExecContext(ctx, `DELETE FROM account_cooldowns WHERE down_until <= ?`, now); err != nil {
		return nil, fmt.Errorf("prune expired cooldowns: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT account_id, model, down_until, backoff_level, kind, message, model_kind
FROM account_cooldowns WHERE down_until > ? ORDER BY account_id, model`, now)
	if err != nil {
		return nil, fmt.Errorf("load cooldowns: %w", err)
	}
	defer rows.Close()
	var out []accounts.CooldownRow
	for rows.Next() {
		var row accounts.CooldownRow
		var until, model string
		if err := rows.Scan(&row.AccountID, &model, &until, &row.BackoffLevel, &row.Kind, &row.Message, &row.ModelKind); err != nil {
			return nil, fmt.Errorf("scan cooldown: %w", err)
		}
		row.DownUntil = parseTime(until)
		row.Model = canonicalCooldownModel(model)
		out = append(out, row)
	}
	return out, rows.Err()
}

// ClearCooldown 删除某个账号已持久化的冷却。空模型会清除该账号的每一行
// （运维意图：把它解开）；指定模型则只清除那一行。它报告删除了多少行。
func (s *Store) ClearCooldown(ctx context.Context, accountID, model string) (int64, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, accounts.ErrAccountNotFound
	}
	var (
		result sql.Result
		err    error
	)
	if strings.TrimSpace(model) == "" {
		result, err = s.db.ExecContext(ctx, `DELETE FROM account_cooldowns WHERE account_id = ?`, accountID)
	} else {
		result, err = s.db.ExecContext(ctx, `DELETE FROM account_cooldowns WHERE account_id = ? AND model = ?`,
			accountID, canonicalCooldownModel(model))
	}
	if err != nil {
		return 0, fmt.Errorf("clear cooldown: %w", err)
	}
	cleared, _ := result.RowsAffected()
	return cleared, nil
}

// LoadCatalogSnapshots 返回每一个已持久化的展示目录。无法解码的行会被跳过，
// 而不是让调用方失败：这张表是缓存，不是事实来源。
func (s *Store) LoadCatalogSnapshots(ctx context.Context) ([]accounts.CatalogSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT cache_key, models_json, updated_at FROM model_catalog_snapshot`)
	if err != nil {
		return nil, fmt.Errorf("load catalog snapshots: %w", err)
	}
	defer rows.Close()
	out := make([]accounts.CatalogSnapshot, 0)
	for rows.Next() {
		var key, payload, updatedAt string
		if err := rows.Scan(&key, &payload, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan catalog snapshot: %w", err)
		}
		var models []map[string]any
		if err := json.Unmarshal([]byte(payload), &models); err != nil {
			continue
		}
		out = append(out, accounts.CatalogSnapshot{Key: key, Models: models, UpdatedAt: parseTime(updatedAt)})
	}
	return out, rows.Err()
}

// SaveCatalogSnapshot 插入或更新一个展示目录。空 key 或空模型列表是 no-op：
// 持久化“空”会摧毁先前良好的快照。
func (s *Store) SaveCatalogSnapshot(ctx context.Context, snapshot accounts.CatalogSnapshot) error {
	key := strings.TrimSpace(snapshot.Key)
	if key == "" || len(snapshot.Models) == 0 {
		return nil
	}
	payload, err := json.Marshal(snapshot.Models)
	if err != nil {
		return fmt.Errorf("marshal catalog snapshot: %w", err)
	}
	updatedAt := snapshot.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO model_catalog_snapshot (cache_key, models_json, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(cache_key) DO UPDATE SET models_json=excluded.models_json, updated_at=excluded.updated_at`,
		key, string(payload), formatTime(updatedAt)); err != nil {
		return fmt.Errorf("save catalog snapshot: %w", err)
	}
	return nil
}

func (s *Store) RecordPoolState(ctx context.Context, state accounts.PoolState) error {
	var cooldown any
	status := "ready"
	if !state.DownUntil.IsZero() && time.Now().Before(state.DownUntil) {
		cooldown = formatTime(state.DownUntil)
		status = "cooling"
	}
	_, err := s.db.ExecContext(ctx, `
	UPDATE accounts SET status = ?, last_error = ?, last_error_kind = ?, cooldown_until = ?, updated_at = ?
	WHERE id = ?`, status, state.LastError, state.LastErrorKind, cooldown, formatTime(time.Now().UTC()), state.ID)
	if err != nil {
		return fmt.Errorf("record pool state: %w", err)
	}
	return nil
}

const backoffMaxLevel = 8

func clampBackoffLevel(level int) int {
	if level < 0 {
		return 0
	}
	if level > backoffMaxLevel {
		return backoffMaxLevel
	}
	return level
}
