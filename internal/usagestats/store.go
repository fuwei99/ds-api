package usagestats

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ds2api/internal/config"
)

const (
	FileVersion   = 2
	FlushInterval = time.Second
)

// gmt8 keeps day bucketing aligned with DeepSeek's official usage page.
var gmt8 = time.FixedZone("GMT+8", 8*60*60)

// ErrInvalidUsageSettings marks user-input validation failures in
// UsageSettings/SaveSettings so HTTP handlers can map them to 400.
var ErrInvalidUsageSettings = errors.New("invalid usage settings")

// Entry 表示某一天某个模型在某个调用方（API Key）下的累计 Token 用量与费用。
type Entry struct {
	Date             string  `json:"date"`
	Model            string  `json:"model"`
	CallerID         string  `json:"caller_id,omitempty"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	ReasoningTokens  int64   `json:"reasoning_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Cost             float64 `json:"cost,omitempty"`
	Calls            int64   `json:"calls"`
}

// BackfillItem 是聊天历史回填的输入条目。
type BackfillItem struct {
	Timestamp  int64
	Model      string
	CallerID   string
	Prompt     int64
	Completion int64
	Reasoning  int64
	Total      int64
}

type fileData struct {
	Version   int     `json:"version"`
	UpdatedAt int64   `json:"updated_at"`
	Entries   []Entry `json:"entries"`
}

// legacyModelUsage 与 legacyDayUsage 是 v1（按天+模型聚合）账本的旧结构，
// 仅在加载时用于迁移，不再写出。
type legacyModelUsage struct {
	Requests     int64 `json:"requests"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

type legacyDayUsage struct {
	Date   string                      `json:"date"`
	Models map[string]legacyModelUsage `json:"models"`
}

type legacyV1File struct {
	Version int                        `json:"version"`
	Days    map[string]*legacyDayUsage `json:"days"`
}

// Store 是内容无关的 Token 用量账本：按 GMT+8 日期 + 模型 + 调用方聚合
// 数值累计与费用估算，不保存提示词、回复内容、账号或密钥本体。
// 落盘采用版本化文件 + 后台定时 flush，费用按请求发生时刻的波峰倍率累计。
type Store struct {
	mu           sync.Mutex
	path         string
	settingsPath string
	entries      map[string]*Entry
	settings     UsageSettings
	revision     int64
	dirty        bool
	stop         chan struct{}
	done         chan struct{}
	closeOnce    sync.Once
}

func New(path string) *Store {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	s := &Store{
		path:         filepath.Clean(strings.TrimSpace(path)),
		settingsPath: strings.TrimSpace(path) + ".settings",
		entries:      map[string]*Entry{},
		settings:     DefaultSettings(),
	}
	s.mu.Lock()
	if err := s.loadLocked(); err != nil {
		config.Logger.Warn("[usage_stats] unavailable at startup", "path", s.path, "error", err)
		s.entries = map[string]*Entry{}
	}
	s.mu.Unlock()
	s.loadSettings()
	s.startFlusher()
	return s
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Record 在每次成功（或被中止但已有产出）的请求后累计一条用量记录。
// 即使聊天历史关闭也会生效；at 通常传请求开始时间，用于日期分桶与波峰计价。
// 「记录用量」关闭（settings.Enabled=false）时完全不记录。
func (s *Store) Record(model, callerID string, usage map[string]any, at time.Time) {
	if s == nil {
		return
	}
	prompt, completion, reasoning, total := ParseUsage(usage)
	if total <= 0 {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	model, callerID = normalizeModel(model), normalizeCallerID(callerID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.settings.Enabled {
		return
	}

	cost := s.computeCostLocked(model, prompt, completion, at)
	key := s.entryKeyLocked(at, model, callerID)
	entry := s.entries[key]
	if entry == nil {
		date := at.In(gmt8).Format("2006-01-02")
		entry = &Entry{Date: date, Model: model, CallerID: callerID}
		s.entries[key] = entry
	}
	entry.PromptTokens += prompt
	entry.CompletionTokens += completion
	entry.ReasoningTokens += reasoning
	entry.TotalTokens += total
	entry.Calls++
	entry.Cost += cost

	s.dataChangedLocked()
}

func (s *Store) entryKeyLocked(at time.Time, model, callerID string) string {
	return at.In(gmt8).Format("2006-01-02") + "\x00" + model + "\x00" + callerID
}

func (s *Store) dataChangedLocked() {
	s.revision++
	s.dirty = true
}

// Summary 返回按日期倒序、模型/调用方升序的累计条目。费用是记录时刻按
// 当时单价一次估算的（含波峰倍率），不做追溯重算：调价只影响之后的请求，
// 也避免「记录用量」开关反复切换导致的历史费用漂移。
func (s *Store) Summary() []Entry {
	if s == nil {
		return []Entry{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].CallerID < out[j].CallerID
	})
	return out
}

// Backfill 仅在账本为空时把聊天历史里的历史请求一次性灌入，避免重复计数。
func (s *Store) Backfill(items []BackfillItem) (bool, error) {
	if s == nil {
		return false, nil
	}
	s.mu.Lock()
	if len(s.entries) > 0 {
		s.mu.Unlock()
		return false, nil
	}

	rebuilt := map[string]*Entry{}
	for _, item := range items {
		if item.Total <= 0 {
			continue
		}
		model := normalizeModel(item.Model)
		callerID := normalizeCallerID(item.CallerID)
		ts := item.Timestamp
		if ts <= 0 {
			ts = time.Now().UnixMilli()
		}
		at := time.UnixMilli(ts)
		cost := s.computeCostLocked(model, item.Prompt, item.Completion, at)
		key := s.entryKeyLocked(at, model, callerID)
		entry := rebuilt[key]
		if entry == nil {
			date := at.In(gmt8).Format("2006-01-02")
			entry = &Entry{Date: date, Model: model, CallerID: callerID}
			rebuilt[key] = entry
		}
		entry.PromptTokens += item.Prompt
		entry.CompletionTokens += item.Completion
		entry.ReasoningTokens += item.Reasoning
		entry.TotalTokens += item.Total
		entry.Calls++
		entry.Cost += cost
	}
	s.entries = rebuilt
	s.dataChangedLocked()
	s.mu.Unlock()
	return true, s.flushNow()
}

// Import 把外部导出（CSV 等）还原出的条目合并进账本：与既有条目
// 同日期 + 模型 + 调用方时数值累加、费用原样相加，否则新建条目。
// 传入条目的费用不会被重新估算，从而保留导出端记录时刻的计价结果。
// 返回成功合并的条目数。
func (s *Store) Import(entries []Entry) (int, error) {
	if s == nil {
		return 0, nil
	}
	s.mu.Lock()
	merged := 0
	for _, entry := range entries {
		date := strings.TrimSpace(entry.Date)
		if _, err := time.Parse("2006-01-02", date); err != nil {
			continue
		}
		if strings.TrimSpace(entry.Model) == "" {
			continue
		}
		model := normalizeModel(entry.Model)
		callerID := normalizeCallerID(entry.CallerID)
		key := date + "\x00" + model + "\x00" + callerID
		target := s.entries[key]
		if target == nil {
			copied := entry
			copied.Date = date
			copied.Model = model
			copied.CallerID = callerID
			s.entries[key] = &copied
		} else {
			target.PromptTokens += entry.PromptTokens
			target.CompletionTokens += entry.CompletionTokens
			target.ReasoningTokens += entry.ReasoningTokens
			target.TotalTokens += entry.TotalTokens
			target.Cost += entry.Cost
			target.Calls += entry.Calls
		}
		merged++
	}
	if merged > 0 {
		s.dataChangedLocked()
	}
	s.mu.Unlock()
	if merged == 0 {
		return 0, nil
	}
	return merged, s.flushNow()
}

// Clear 清空全部用量数据并同步落盘。
func (s *Store) Clear() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.entries = map[string]*Entry{}
	s.dataChangedLocked()
	s.mu.Unlock()
	return s.flushNow()
}

func (s *Store) Settings() UsageSettings {
	if s == nil {
		return DefaultSettings()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSettings(s.settings)
}

// cloneSettings 深拷贝设置，避免调用方改到 Store 内部状态。Models 表与
// 「模拟缓存命中」段的指针字段都必须重新分配。
func cloneSettings(settings UsageSettings) UsageSettings {
	out := settings
	out.Models = make(map[string]ModelPrice, len(settings.Models))
	for key, price := range settings.Models {
		copied := price
		copied.InputMissPrice = cloneFloat64Ptr(price.InputMissPrice)
		out.Models[key] = copied
	}
	out.CacheHit = CacheHitSettings{
		Enabled: cloneBoolPtr(settings.CacheHit.Enabled),
		Rate:    cloneFloat64Ptr(settings.CacheHit.Rate),
	}
	return out
}

func (s *Store) SaveSettings(settings UsageSettings) error {
	if s == nil {
		return nil
	}
	if err := validatePeakSettings(settings.Peak); err != nil {
		return err
	}
	if err := validateModelPrices(settings.Models); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = normalizeSettings(settings)
	return s.saveSettingsLocked()
}

func (s *Store) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() { close(s.stop) })
	<-s.done
}

func (s *Store) startFlusher() {
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go s.flushLoop()
}

func (s *Store) flushLoop() {
	ticker := time.NewTicker(FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			if err := s.flushNow(); err != nil {
				config.Logger.Warn("[usage_stats] final flush failed", "path", s.path, "error", err)
			}
			close(s.done)
			return
		case <-ticker.C:
			if err := s.flushNow(); err != nil {
				config.Logger.Warn("[usage_stats] save failed", "path", s.path, "error", err)
			}
		}
	}
}

func (s *Store) flushNow() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	revision := s.revision
	payload := s.snapshotLocked()
	s.mu.Unlock()

	body, err := json.MarshalIndent(payload, "", "  ")
	if err == nil {
		err = writeFileAtomic(s.path, append(body, '\n'))
	}
	if err != nil {
		return fmt.Errorf("save usage stats: %w", err)
	}

	s.mu.Lock()
	if s.revision == revision {
		s.dirty = false
	}
	s.mu.Unlock()
	return nil
}

func (s *Store) snapshotLocked() fileData {
	payload := fileData{
		Version:   FileVersion,
		UpdatedAt: time.Now().UnixMilli(),
		Entries:   make([]Entry, 0, len(s.entries)),
	}
	for _, entry := range s.entries {
		payload.Entries = append(payload.Entries, *entry)
	}
	sort.Slice(payload.Entries, func(i, j int) bool {
		if payload.Entries[i].Date != payload.Entries[j].Date {
			return payload.Entries[i].Date < payload.Entries[j].Date
		}
		if payload.Entries[i].Model != payload.Entries[j].Model {
			return payload.Entries[i].Model < payload.Entries[j].Model
		}
		return payload.Entries[i].CallerID < payload.Entries[j].CallerID
	})
	return payload
}

func (s *Store) loadLocked() error {
	if s.path == "" {
		return errors.New("usage stats path is required")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil && filepath.Dir(s.path) != "." {
		return fmt.Errorf("create usage stats dir: %w", err)
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read usage stats file: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}
	if entries, ok := decodeLegacyArray(raw); ok {
		s.ingestEntries(entries)
		return nil
	}
	var loaded fileData
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return fmt.Errorf("decode usage stats file: %w", err)
	}
	if len(loaded.Entries) > 0 {
		s.ingestEntries(loaded.Entries)
		return nil
	}
	if days, ok := decodeLegacyV1Days(raw); ok {
		s.entries = migrateLegacyV1Days(days)
		return nil
	}
	return nil
}

// decodeLegacyArray 兼容旧版（无版本包装的纯 Entry 数组）文件。
func decodeLegacyArray(raw []byte) ([]Entry, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	var list []Entry
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, false
	}
	return list, true
}

// decodeLegacyV1Days 识别 v1 按天+模型聚合的旧账本结构。
func decodeLegacyV1Days(raw []byte) (map[string]*legacyDayUsage, bool) {
	var v1 legacyV1File
	if err := json.Unmarshal(raw, &v1); err != nil || len(v1.Days) == 0 {
		return nil, false
	}
	return v1.Days, true
}

func migrateLegacyV1Days(days map[string]*legacyDayUsage) map[string]*Entry {
	entries := map[string]*Entry{}
	for date, day := range days {
		if day == nil {
			continue
		}
		for model, usage := range day.Models {
			normalized := normalizeModel(model)
			key := date + "\x00" + normalized + "\x00" + "unknown"
			entry := entries[key]
			if entry == nil {
				entry = &Entry{Date: date, Model: normalized, CallerID: "unknown"}
				entries[key] = entry
			}
			entry.PromptTokens += usage.InputTokens
			entry.CompletionTokens += usage.OutputTokens
			entry.TotalTokens += usage.InputTokens + usage.OutputTokens
			entry.Calls += usage.Requests
		}
	}
	return entries
}

func (s *Store) ingestEntries(list []Entry) {
	for _, entry := range list {
		model := normalizeModel(entry.Model)
		callerID := normalizeCallerID(entry.CallerID)
		key := entry.Date + "\x00" + model + "\x00" + callerID
		if existing := s.entries[key]; existing != nil {
			existing.PromptTokens += entry.PromptTokens
			existing.CompletionTokens += entry.CompletionTokens
			existing.ReasoningTokens += entry.ReasoningTokens
			existing.TotalTokens += entry.TotalTokens
			existing.Cost += entry.Cost
			existing.Calls += entry.Calls
			continue
		}
		copied := entry
		copied.Model = model
		copied.CallerID = callerID
		s.entries[key] = &copied
	}
}

func (s *Store) loadSettings() {
	if s == nil || s.settingsPath == "" {
		return
	}
	raw, err := os.ReadFile(s.settingsPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			config.Logger.Warn("[usage_stats] settings load failed", "error", err)
		}
		return
	}
	var settings UsageSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		config.Logger.Warn("[usage_stats] settings parse failed", "error", err)
		return
	}
	settings = normalizeSettings(settings)
	if err := validatePeakSettings(settings.Peak); err != nil {
		config.Logger.Warn("[usage_stats] settings peak invalid, using defaults", "error", err)
		settings.Peak = DefaultSettings().Peak
	}
	s.mu.Lock()
	s.settings = settings
	if err := s.saveSettingsLocked(); err != nil {
		config.Logger.Warn("[usage_stats] settings normalize/write failed", "error", err)
	}
	s.mu.Unlock()
}

func (s *Store) saveSettingsLocked() error {
	if s == nil || s.settingsPath == "" {
		return nil
	}
	raw, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return fmt.Errorf("settings marshal: %w", err)
	}
	return writeFileAtomic(s.settingsPath, append(raw, '\n'))
}

func normalizeModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return "unknown"
	}
	return model
}

func normalizeCallerID(callerID string) string {
	callerID = strings.TrimSpace(callerID)
	if callerID == "" {
		return "unknown"
	}
	return callerID
}

func writeFileAtomic(path string, body []byte) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil && dir != "." {
		return fmt.Errorf("create usage stats dir: %w", err)
	}
	tmpFile, err := os.CreateTemp(dir, ".usage-stats-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp usage stats file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	if _, err := tmpFile.Write(body); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return fmt.Errorf("write temp usage stats file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return fmt.Errorf("sync temp usage stats file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp usage stats file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("promote temp usage stats file: %w", err)
	}
	return nil
}
