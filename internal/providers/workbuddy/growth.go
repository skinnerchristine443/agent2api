package workbuddy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"agent2api/internal/accounts"
)

// 成长中心活动 API 的路径。它们以 BillingBase() + pathGrowthRoot + 下列某个后缀
// 来寻址，与签到计费端点的寻址方式一致。
const (
	pathGrowthRoot = "/v2/activity/growth"

	pathGrowthTravelStatus = "/buddy/travel/status"
	pathGrowthTravelClaim  = "/buddy/travel/claim"
	pathGrowthTasks        = "/tasks"
	pathGrowthTasksAccept  = "/tasks/accept"
	pathGrowthStreak       = "/streak"
	pathGrowthEnergy       = "/energy"
	pathGrowthHeatmap      = "/heatmap"
)

// Task 的 accept_status 取值。只有 accept_status 为 not_accepted 的任务才可被接受，
// 只有 completed 的才可领取；其余取值要么是终态要么在进行中，必须原样不动。
const (
	growthAcceptNotAccepted = "not_accepted"
	growthAcceptCompleted   = "completed"
)

// growthTravelArrived 是领取操作唯一可作用的 travel 状态。
const growthTravelArrived = "arrived"

// growthAcceptBatchLimit 限定单次 accept 调用能携带多少个 task code。
const growthAcceptBatchLimit = 20

// growthBodyLimit 限定回显进错误里的上游明细的长度，
// 与其它计费读取器所用的截断一致。
const growthBodyLimit = 240

// GrowthTravelStatus 是只读的 buddy-travel 区块。
type GrowthTravelStatus struct {
	State             string
	RecordID          string
	DailyLimitReached bool
	// Available 报告现在是否可以尝试领取：buddy 已到达且 record id 存在。
	Available bool
	// Err 记录某个区块读取失败，但不致使整个聚合失败。
	Err string
}

// GrowthTask 是一个成长中心任务。
type GrowthTask struct {
	Code         string
	Title        string
	Locked       bool
	AcceptStatus string
	RewardCredit float64
	RewardEnergy float64
}

// Acceptable 报告该任务是否可被接受：已解锁且从未被接受。
func (t GrowthTask) Acceptable() bool {
	return !t.Locked && t.AcceptStatus == growthAcceptNotAccepted
}

// Claimable 报告该任务是否已完成且奖励尚未领取。
func (t GrowthTask) Claimable() bool { return t.AcceptStatus == growthAcceptCompleted }

// GrowthStreak 是只读的连续签到区块。
type GrowthStreak struct {
	Days        int
	HasDays     bool
	MakeupDates []string
	// Err 记录某个区块读取失败，但不致使整个聚合失败。
	Err string
}

// GrowthEnergy 是只读的能量区块。
type GrowthEnergy struct {
	Balance    float64
	HasBalance bool
	// Err 记录某个区块读取失败，但不致使整个聚合失败。
	Err string
}

// GrowthHeatmapCell 是活动日历中的一天；Score 是当天的发放次数，
// 0 表示漏签。
type GrowthHeatmapCell struct {
	Date  string
	Score int
}

// GrowthHeatmap 是只读的活动日历区块。
type GrowthHeatmap struct {
	Cells []GrowthHeatmapCell
	// Err 记录某个区块读取失败，但不致使整个聚合失败。
	Err string
}

// GrowthStatus 是成长中心的只读聚合。每个区块独立获取，因此某个区块失败时其余
// 区块仍留有数据，并以自己的 Err 记录失败，而不是让整个调用失败。
type GrowthStatus struct {
	Travel GrowthTravelStatus
	Tasks  []GrowthTask
	// TasksErr 记录任务列表读取失败。
	TasksErr string
	Streak   GrowthStreak
	Energy   GrowthEnergy
	Heatmap  GrowthHeatmap
}

// GrowthClaimStatus 对一次成长中心写尝试进行分类。
type GrowthClaimStatus string

const (
	// GrowthClaimSuccess 表示奖励或接受是新发放的。
	GrowthClaimSuccess GrowthClaimStatus = "success"
	// GrowthClaimAlreadyClaimed 表示上游报告奖励已被领取。这是一次幂等的成功，
	// 绝不是失败。
	GrowthClaimAlreadyClaimed GrowthClaimStatus = "already_claimed"
	// GrowthClaimSkipped 表示因观察到的状态不需要写操作而未发送任何写入。
	GrowthClaimSkipped GrowthClaimStatus = "skipped"
	// GrowthClaimFailed 表示该尝试被拒绝。
	GrowthClaimFailed GrowthClaimStatus = "failed"
)

// GrowthClaimOutcome 是一次成长中心写尝试。
type GrowthClaimOutcome struct {
	// Target 对 travel 领取为 "travel"，否则为 task code。
	Target string
	// Action 为 "accept" 或 "claim"。
	Action string
	Status GrowthClaimStatus
	// Message 在有上游消息时携带它。
	Message string
	// Credit 与 Energy 是上游报告的奖励（已知时）。
	Credit float64
	Energy float64
}

// GrowthClaimResult 是 ClaimGrowthRewards 的结果。无法尝试的区块记录在 Errors 中；
// 其余区块仍会运行。
type GrowthClaimResult struct {
	Outcomes []GrowthClaimOutcome
	Errors   []string
}

// GrowthStatus 读取成长中心而不改变任何状态：travel 状态、任务列表、连续签到计数
// 与能量余额。各区块相互独立，因此上游部分不可用仍能给出可用的快照。
func (client *Client) GrowthStatus(ctx context.Context, accountID string) (GrowthStatus, error) {
	credential, err := client.resolvedCredential(ctx, accountID)
	if err != nil {
		return GrowthStatus{}, err
	}
	var status GrowthStatus

	if raw, readErr := client.growthRead(ctx, accountID, credential, pathGrowthTravelStatus); readErr != nil {
		status.Travel.Err = readErr.Error()
	} else if travel, parseErr := parseGrowthTravelState(raw); parseErr != nil {
		status.Travel.Err = parseErr.Error()
	} else {
		status.Travel = travel.status()
	}

	if raw, readErr := client.growthRead(ctx, accountID, credential, pathGrowthTasks); readErr != nil {
		status.TasksErr = readErr.Error()
	} else if tasks, parseErr := parseGrowthTasks(raw); parseErr != nil {
		status.TasksErr = parseErr.Error()
	} else {
		status.Tasks = tasks
	}

	if raw, readErr := client.growthRead(ctx, accountID, credential, pathGrowthStreak); readErr != nil {
		status.Streak.Err = readErr.Error()
	} else if streak, parseErr := parseGrowthStreak(raw); parseErr != nil {
		status.Streak.Err = parseErr.Error()
	} else {
		status.Streak = streak
	}

	if raw, readErr := client.growthRead(ctx, accountID, credential, pathGrowthEnergy); readErr != nil {
		status.Energy.Err = readErr.Error()
	} else if energy, parseErr := parseGrowthEnergy(raw); parseErr != nil {
		status.Energy.Err = parseErr.Error()
	} else {
		status.Energy = energy
	}

	if raw, readErr := client.growthRead(ctx, accountID, credential, pathGrowthHeatmap); readErr != nil {
		status.Heatmap.Err = readErr.Error()
	} else if heatmap, parseErr := parseGrowthHeatmap(raw); parseErr != nil {
		status.Heatmap.Err = parseErr.Error()
	} else {
		status.Heatmap = heatmap
	}

	return status, nil
}

// ClaimGrowthRewards 只执行幂等的成长中心领取：buddy 已到达时的 travel 奖励、
// 已解锁任务的接受，以及已完成任务的奖励。它绝不触碰不可逆的路径（补签卡、兑换、
// 抽奖、buddy open/depart、heatmap 写入）。
//
// 某个区块的失败记录在 Errors 中，不会中断其它区块。它唯一返回的错误是凭据失败，
// 因为那会同等阻断每个区块。
func (client *Client) ClaimGrowthRewards(ctx context.Context, accountID string) (GrowthClaimResult, error) {
	credential, err := client.resolvedCredential(ctx, accountID)
	if err != nil {
		return GrowthClaimResult{}, err
	}
	var result GrowthClaimResult
	client.claimGrowthTravel(ctx, accountID, credential, &result)
	client.claimGrowthTasks(ctx, accountID, credential, &result)
	return result, nil
}

// growthRawStatus 是未解析的 travel 状态载荷。每个字段都是 json.RawMessage，因为
// 上游对同一个键混用布尔、数字和数字字符串，也因为原始 record id 必须原样进入 claim
// 请求体（数字 id 不得被重新编码成字符串）。
type growthRawStatus struct {
	State             json.RawMessage `json:"state"`
	RecordID          json.RawMessage `json:"record_id"`
	DailyLimitReached json.RawMessage `json:"daily_limit_reached"`
}

type growthTravelState struct {
	state             string
	recordID          json.RawMessage
	recordIDText      string
	dailyLimitReached bool
}

func parseGrowthTravelState(raw json.RawMessage) (growthTravelState, error) {
	var payload growthRawStatus
	if err := json.Unmarshal(raw, &payload); err != nil {
		return growthTravelState{}, fmt.Errorf("travel status parse: %w", err)
	}
	return growthTravelState{
		state:             rawString(payload.State),
		recordID:          payload.RecordID,
		recordIDText:      rawString(payload.RecordID),
		dailyLimitReached: rawBool(payload.DailyLimitReached),
	}, nil
}

func (s growthTravelState) status() GrowthTravelStatus {
	out := GrowthTravelStatus{
		State:             s.state,
		RecordID:          s.recordIDText,
		DailyLimitReached: s.dailyLimitReached,
	}
	out.Available = s.state == growthTravelArrived && s.recordIDText != ""
	return out
}

type growthRawTask struct {
	TaskCode     json.RawMessage `json:"task_code"`
	Title        json.RawMessage `json:"title"`
	Locked       json.RawMessage `json:"locked"`
	AcceptStatus json.RawMessage `json:"accept_status"`
	RewardCredit json.RawMessage `json:"reward_credit"`
	RewardEnergy json.RawMessage `json:"reward_energy"`
}

func parseGrowthTasks(raw json.RawMessage) ([]GrowthTask, error) {
	var payload struct {
		Tasks []growthRawTask `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("tasks parse: %w", err)
	}
	out := make([]GrowthTask, 0, len(payload.Tasks))
	for _, item := range payload.Tasks {
		task := GrowthTask{
			Code:         rawString(item.TaskCode),
			Title:        rawString(item.Title),
			Locked:       rawBool(item.Locked),
			AcceptStatus: rawString(item.AcceptStatus),
		}
		if v, ok := rawFloat(item.RewardCredit); ok {
			task.RewardCredit = v
		}
		if v, ok := rawFloat(item.RewardEnergy); ok {
			task.RewardEnergy = v
		}
		out = append(out, task)
	}
	return out, nil
}

func parseGrowthStreak(raw json.RawMessage) (GrowthStreak, error) {
	var payload struct {
		Streak json.RawMessage `json:"streak"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return GrowthStreak{}, fmt.Errorf("streak parse: %w", err)
	}
	target := raw
	if len(payload.Streak) > 0 && payload.Streak[0] == '{' {
		target = payload.Streak
	}
	var streak struct {
		Days        json.RawMessage   `json:"days"`
		MakeupDates []json.RawMessage `json:"makeup_dates"`
	}
	if err := json.Unmarshal(target, &streak); err != nil {
		return GrowthStreak{}, fmt.Errorf("streak parse: %w", err)
	}
	out := GrowthStreak{}
	if v, ok := rawFloat(streak.Days); ok {
		out.Days, out.HasDays = int(v), true
	}
	for _, day := range streak.MakeupDates {
		if text := rawString(day); text != "" {
			out.MakeupDates = append(out.MakeupDates, text)
		}
	}
	return out, nil
}

func parseGrowthEnergy(raw json.RawMessage) (GrowthEnergy, error) {
	var payload struct {
		Balance json.RawMessage `json:"balance"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return GrowthEnergy{}, fmt.Errorf("energy parse: %w", err)
	}
	out := GrowthEnergy{}
	// rawFloat 已经拒绝非有限值，因此 NaN 余额不会泄漏进聚合结果。
	if v, ok := rawFloat(payload.Balance); ok {
		out.Balance, out.HasBalance = v, true
	}
	return out, nil
}

// parseGrowthHeatmap 接受上游家族提供的各种形态的活动日历：data.heatmap.cells、
// data.cells，或一个裸的 cell 数组。cell 字段经过容错的 raw 访问器（上游混用数字与
// 数字字符串）；格式错误的 cell 会被跳过而非让该区块失败，而完全没有任何 cell 的
// 区块是显式错误，这样形态不对的响应就不能伪装成空日历。
func parseGrowthHeatmap(raw json.RawMessage) (GrowthHeatmap, error) {
	if len(raw) > 0 && raw[0] == '[' {
		var cells []json.RawMessage
		if err := json.Unmarshal(raw, &cells); err != nil {
			return GrowthHeatmap{}, fmt.Errorf("heatmap parse: %w", err)
		}
		return heatmapFromCells(cells), nil
	}
	var payload struct {
		Heatmap json.RawMessage `json:"heatmap"`
		Cells   json.RawMessage `json:"cells"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return GrowthHeatmap{}, fmt.Errorf("heatmap parse: %w", err)
	}
	var target json.RawMessage
	switch {
	case len(payload.Heatmap) > 0 && string(payload.Heatmap) != "null":
		target = payload.Heatmap
	case len(payload.Cells) > 0 && string(payload.Cells) != "null":
		target = payload.Cells
	default:
		return GrowthHeatmap{}, fmt.Errorf("heatmap parse: no cells")
	}
	if len(target) > 0 && target[0] == '[' {
		var cells []json.RawMessage
		if err := json.Unmarshal(target, &cells); err != nil {
			return GrowthHeatmap{}, fmt.Errorf("heatmap parse: %w", err)
		}
		return heatmapFromCells(cells), nil
	}
	var section struct {
		Cells []json.RawMessage `json:"cells"`
	}
	if err := json.Unmarshal(target, &section); err != nil {
		return GrowthHeatmap{}, fmt.Errorf("heatmap parse: %w", err)
	}
	return heatmapFromCells(section.Cells), nil
}

// heatmapFromCells 转换原始的天条目，跳过格式错误的。
func heatmapFromCells(rawCells []json.RawMessage) GrowthHeatmap {
	out := GrowthHeatmap{Cells: make([]GrowthHeatmapCell, 0, len(rawCells))}
	for _, cellRaw := range rawCells {
		var cell struct {
			Date  json.RawMessage `json:"date"`
			Score json.RawMessage `json:"score"`
		}
		if err := json.Unmarshal(cellRaw, &cell); err != nil {
			continue
		}
		date := rawString(cell.Date)
		if date == "" {
			continue
		}
		score := 0
		if v, ok := rawFloat(cell.Score); ok {
			score = int(v)
		}
		out.Cells = append(out.Cells, GrowthHeatmapCell{Date: date, Score: score})
	}
	return out
}

// growthUnwrap 返回成长响应的对象载荷，同时接受 {code,msg,data} 信封和一个裸对象。
// 非零的信封 code 是业务错误，会作为错误报告而不返回载荷。
func growthUnwrap(body []byte) (json.RawMessage, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, fmt.Errorf("empty response")
	}
	if text[0] != '{' {
		return nil, fmt.Errorf("unexpected response: %s", growthBodyText(body))
	}
	var env envelope
	if json.Unmarshal(body, &env) == nil {
		if env.Code != 0 {
			msg := strings.TrimSpace(env.Msg)
			if msg == "" {
				msg = fmt.Sprintf("code=%d", env.Code)
			}
			return nil, fmt.Errorf("%s", msg)
		}
		if len(env.Data) > 0 && env.Data[0] == '{' {
			return env.Data, nil
		}
	}
	return json.RawMessage(body), nil
}

// growthField 以原始 JSON 返回对象载荷中的一个字段，当载荷不是对象或键不存在时返回
// nil。原始形式为宽松读取保留上游的编码（数字还是字符串）。
func growthField(raw json.RawMessage, key string) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields[key]
}

func growthBodyText(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > growthBodyLimit {
		text = text[:growthBodyLimit]
	}
	return text
}

// growthRead 执行一次只读成长请求并返回其解包后的对象载荷。
func (client *Client) growthRead(ctx context.Context, accountID string, credential Credential, path string) (json.RawMessage, error) {
	body, status, err := client.do(ctx, accountID, http.MethodGet, credential.BillingBase()+pathGrowthRoot+path, nil,
		func(h http.Header) { SetBillingHeaders(h, credential) })
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, fmt.Errorf("status=%d: %s", status, growthBodyText(body))
	}
	return growthUnwrap(body)
}

// growthCallResult 是原始成长响应加上其 HTTP 状态。信封被延迟解包，使调用方能
// 把业务码映射到自己的结果上，而不会丢失状态或响应体。
type growthCallResult struct {
	status int
	body   []byte
}

func (r growthCallResult) data() (json.RawMessage, error) { return growthUnwrap(r.body) }

// alreadyClaimed 报告响应是否意味着奖励已被领取：一个显式的 already_claimed 标志
// 或一个 4xx 业务码。两者都是幂等的未命中而非失败。会话失效或限流的 4xx 是真正的
// 失败，不会被吞掉。
func (r growthCallResult) alreadyClaimed() bool {
	if raw, err := r.data(); err == nil {
		if v, ok := rawBoolOK(growthField(raw, "already_claimed")); ok && v {
			return true
		}
	}
	if r.status < 400 || r.status >= 500 {
		return false
	}
	switch Classify(r.status, string(r.body)).Kind {
	case accounts.KindAuth, accounts.KindRateLimit:
		return false
	default:
		return true
	}
}

// growthPost 执行一次成长写入并返回原始响应。传输失败作为错误返回；HTTP 与业务
// 拒绝则放在结果里返回，由调用方分类。
func (client *Client) growthPost(ctx context.Context, accountID string, credential Credential, path string, payload []byte) (growthCallResult, error) {
	if payload == nil {
		payload = []byte("{}")
	}
	body, status, err := client.do(ctx, accountID, http.MethodPost, credential.BillingBase()+pathGrowthRoot+path, payload,
		func(h http.Header) { SetBillingHeaders(h, credential) })
	if err != nil {
		return growthCallResult{}, err
	}
	return growthCallResult{status: status, body: body}, nil
}

// growthTravelClaimBody 构造 {"record_id": <record_id>}，同时保留 id 的上游编码，
// 因此数字 id 不会被悄悄转成字符串。
func growthTravelClaimBody(recordID json.RawMessage) ([]byte, error) {
	trimmed := strings.TrimSpace(string(recordID))
	if trimmed == "" || trimmed == "null" {
		return nil, fmt.Errorf("travel record id is missing")
	}
	var probe any
	if json.Unmarshal(recordID, &probe) != nil {
		return nil, fmt.Errorf("travel record id is not valid json")
	}
	return json.Marshal(map[string]json.RawMessage{"record_id": recordID})
}

func (client *Client) claimGrowthTravel(ctx context.Context, accountID string, credential Credential, result *GrowthClaimResult) {
	raw, err := client.growthRead(ctx, accountID, credential, pathGrowthTravelStatus)
	if err != nil {
		result.Errors = append(result.Errors, "travel status: "+err.Error())
		return
	}
	travel, err := parseGrowthTravelState(raw)
	if err != nil {
		result.Errors = append(result.Errors, "travel status: "+err.Error())
		return
	}
	status := travel.status()
	if !status.Available {
		result.Outcomes = append(result.Outcomes, GrowthClaimOutcome{
			Target: "travel", Action: "claim", Status: GrowthClaimSkipped,
			Message: growthTravelSkipReason(status),
		})
		return
	}
	payload, err := growthTravelClaimBody(travel.recordID)
	if err != nil {
		result.Errors = append(result.Errors, "travel claim: "+err.Error())
		return
	}
	call, err := client.growthPost(ctx, accountID, credential, pathGrowthTravelClaim, payload)
	if err != nil {
		result.Outcomes = append(result.Outcomes, GrowthClaimOutcome{
			Target: "travel", Action: "claim", Status: GrowthClaimFailed, Message: err.Error(),
		})
		return
	}
	if call.alreadyClaimed() {
		result.Outcomes = append(result.Outcomes, GrowthClaimOutcome{
			Target: "travel", Action: "claim", Status: GrowthClaimAlreadyClaimed,
		})
		return
	}
	data, err := call.data()
	if err != nil {
		result.Outcomes = append(result.Outcomes, GrowthClaimOutcome{
			Target: "travel", Action: "claim", Status: GrowthClaimFailed, Message: err.Error(),
		})
		return
	}
	outcome := GrowthClaimOutcome{Target: "travel", Action: "claim", Status: GrowthClaimSuccess}
	if v, ok := rawFloat(growthField(data, "reward_credit")); ok {
		outcome.Credit = v
	}
	result.Outcomes = append(result.Outcomes, outcome)
}

func growthTravelSkipReason(status GrowthTravelStatus) string {
	if status.State == growthTravelArrived {
		return "missing record id"
	}
	if status.State == "" {
		return "buddy state unknown"
	}
	return "buddy state=" + status.State
}

func (client *Client) claimGrowthTasks(ctx context.Context, accountID string, credential Credential, result *GrowthClaimResult) {
	raw, err := client.growthRead(ctx, accountID, credential, pathGrowthTasks)
	if err != nil {
		result.Errors = append(result.Errors, "tasks: "+err.Error())
		return
	}
	tasks, err := parseGrowthTasks(raw)
	if err != nil {
		result.Errors = append(result.Errors, "tasks: "+err.Error())
		return
	}
	var acceptCodes []string
	var claimCodes []string
	for _, task := range tasks {
		if task.Code == "" {
			continue
		}
		switch {
		case task.Acceptable():
			acceptCodes = append(acceptCodes, task.Code)
		case task.Claimable():
			claimCodes = append(claimCodes, task.Code)
		}
	}
	client.acceptGrowthTasks(ctx, accountID, credential, acceptCodes, result)
	for _, code := range claimCodes {
		client.claimGrowthTaskReward(ctx, accountID, credential, code, result)
	}
}

func (client *Client) acceptGrowthTasks(ctx context.Context, accountID string, credential Credential, codes []string, result *GrowthClaimResult) {
	for start := 0; start < len(codes); start += growthAcceptBatchLimit {
		end := start + growthAcceptBatchLimit
		if end > len(codes) {
			end = len(codes)
		}
		batch := codes[start:end]
		payload, err := json.Marshal(map[string]any{"task_codes": batch})
		if err != nil {
			result.Errors = append(result.Errors, "accept: "+err.Error())
			continue
		}
		call, err := client.growthPost(ctx, accountID, credential, pathGrowthTasksAccept, payload)
		if err != nil {
			result.Errors = append(result.Errors, "accept: "+err.Error())
			continue
		}
		if call.alreadyClaimed() {
			for _, code := range batch {
				result.Outcomes = append(result.Outcomes, GrowthClaimOutcome{
					Target: code, Action: "accept", Status: GrowthClaimAlreadyClaimed,
				})
			}
			continue
		}
		data, err := call.data()
		if err != nil {
			result.Errors = append(result.Errors, "accept: "+err.Error())
			continue
		}
		recordGrowthAcceptResults(data, batch, result)
	}
}

// recordGrowthAcceptResults 映射一批 accept 的逐 code 结果。上游未报告的 code 会记为
// 失败，而不是被悄悄当作成功领取。
func recordGrowthAcceptResults(data json.RawMessage, batch []string, result *GrowthClaimResult) {
	var payload struct {
		Results []struct {
			TaskCode json.RawMessage `json:"task_code"`
			Status   json.RawMessage `json:"status"`
			Message  json.RawMessage `json:"message"`
		} `json:"results"`
	}
	reported := map[string]struct{ status, message string }{}
	if json.Unmarshal(data, &payload) == nil {
		for _, item := range payload.Results {
			if code := rawString(item.TaskCode); code != "" {
				reported[code] = struct{ status, message string }{rawString(item.Status), rawString(item.Message)}
			}
		}
	}
	for _, code := range batch {
		item, ok := reported[code]
		if !ok {
			result.Outcomes = append(result.Outcomes, GrowthClaimOutcome{
				Target: code, Action: "accept", Status: GrowthClaimFailed,
				Message: "upstream returned no result",
			})
			continue
		}
		result.Outcomes = append(result.Outcomes, GrowthClaimOutcome{
			Target:  code,
			Action:  "accept",
			Status:  growthAcceptStatus(item.status, item.message),
			Message: item.message,
		})
	}
}

// growthAcceptStatus 把 accept 结果的 status/message 映射为一种 outcome。显式的失败
// 标记优先于「already」标记，后者又优先于成功标记；任何无法识别的都算失败，这样拒绝
// 就绝不会被报告为成功。
func growthAcceptStatus(status, message string) GrowthClaimStatus {
	lowerStatus := strings.ToLower(strings.TrimSpace(status))
	lowerMessage := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(lowerStatus, "fail") || strings.Contains(lowerStatus, "error") ||
		strings.Contains(lowerStatus, "reject") || strings.Contains(lowerStatus, "invalid") ||
		strings.Contains(lowerStatus, "locked") || strings.Contains(lowerMessage, "失败"):
		return GrowthClaimFailed
	case strings.Contains(lowerStatus, "already") || strings.Contains(lowerMessage, "已接受") ||
		strings.Contains(lowerMessage, "已领取") || strings.Contains(lowerMessage, "already accepted"):
		return GrowthClaimAlreadyClaimed
	case lowerStatus == "accepted" || lowerStatus == "success" || lowerStatus == "ok" ||
		lowerStatus == "done" || strings.Contains(lowerStatus, "success"):
		return GrowthClaimSuccess
	default:
		return GrowthClaimFailed
	}
}

func (client *Client) claimGrowthTaskReward(ctx context.Context, accountID string, credential Credential, code string, result *GrowthClaimResult) {
	path := pathGrowthTasks + "/" + url.PathEscape(code) + "/claim"
	call, err := client.growthPost(ctx, accountID, credential, path, []byte("{}"))
	if err != nil {
		result.Outcomes = append(result.Outcomes, GrowthClaimOutcome{
			Target: code, Action: "claim", Status: GrowthClaimFailed, Message: err.Error(),
		})
		return
	}
	if call.alreadyClaimed() {
		result.Outcomes = append(result.Outcomes, GrowthClaimOutcome{
			Target: code, Action: "claim", Status: GrowthClaimAlreadyClaimed,
		})
		return
	}
	data, err := call.data()
	if err != nil {
		result.Outcomes = append(result.Outcomes, GrowthClaimOutcome{
			Target: code, Action: "claim", Status: GrowthClaimFailed, Message: err.Error(),
		})
		return
	}
	outcome := GrowthClaimOutcome{Target: code, Action: "claim", Status: GrowthClaimSuccess}
	if v, ok := rawFloat(growthField(data, "credit")); ok {
		outcome.Credit = v
	}
	if v, ok := rawFloat(growthField(data, "energy")); ok {
		outcome.Energy = v
	}
	result.Outcomes = append(result.Outcomes, outcome)
}
