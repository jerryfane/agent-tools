package usage

import (
	"context"
	"time"
)

type Provider interface {
	Name() string
	Enabled() bool
	Limits(context.Context) ([]LimitSnapshot, error)
	Usage(context.Context, time.Time) (UsageSummary, error)
	Sessions(context.Context, time.Time) ([]SessionUsage, error)
	Active(context.Context) ([]ActiveSession, error)
	Doctor(context.Context) []Check
}

type ProviderSummary struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Detail  string `json:"detail"`
}

type LimitSnapshot struct {
	Provider                 string     `json:"provider"`
	Profile                  string     `json:"profile"`
	Label                    string     `json:"label,omitempty"`
	Plan                     string     `json:"plan,omitempty"`
	FiveHourRemainingPercent *float64   `json:"five_hour_remaining_percent,omitempty"`
	WeeklyRemainingPercent   *float64   `json:"weekly_remaining_percent,omitempty"`
	FiveHourResetAt          *time.Time `json:"five_hour_reset_at,omitempty"`
	WeeklyResetAt            *time.Time `json:"weekly_reset_at,omitempty"`
	Source                   string     `json:"source"`
	CacheAgeSeconds          *int64     `json:"cache_age_seconds,omitempty"`
	Error                    string     `json:"error,omitempty"`
}

type UsageSummary struct {
	Provider    string       `json:"provider"`
	Date        string       `json:"date"`
	TotalTokens int64        `json:"total_tokens"`
	CostUSD     float64      `json:"cost_usd"`
	Groups      []UsageGroup `json:"groups"`
}

type UsageGroup struct {
	Provider string  `json:"provider"`
	Repo     string  `json:"repo"`
	Type     string  `json:"type"`
	Active   bool    `json:"active"`
	Sessions int     `json:"sessions"`
	Tokens   int64   `json:"tokens"`
	CostUSD  float64 `json:"cost_usd"`
}

type SessionUsage struct {
	Provider   string  `json:"provider"`
	SessionID  string  `json:"session_id"`
	Repo       string  `json:"repo"`
	Type       string  `json:"type"`
	Tokens     int64   `json:"tokens"`
	CostUSD    float64 `json:"cost_usd"`
	Active     bool    `json:"active"`
	LastPrompt string  `json:"last_prompt"`
}

type ActiveSession struct {
	Provider  string `json:"provider"`
	SessionID string `json:"session_id"`
	PID       int    `json:"pid"`
	Command   string `json:"command"`
}

type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}
