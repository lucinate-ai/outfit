package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Schedule is one cron-driven start or stop of an environment, as the
// schedule Lambda stores it.
type Schedule struct {
	// Action is "start" or "stop".
	Action string `json:"action"`
	// Cron is a five-field expression: minute, hour, day of month, month, day of week.
	Cron string `json:"cron"`
	// Timezone is an IANA zone name; the control plane reads an empty one as UTC.
	Timezone string `json:"timezone"`
}

// ScheduleList is the schedule Lambda's reply: the environment's schedules
// and when each action next fires (RFC 3339, empty when there is none).
type ScheduleList struct {
	Environment string     `json:"environment"`
	Schedules   []Schedule `json:"schedules"`
	Next        struct {
		Start string `json:"start"`
		Stop  string `json:"stop"`
	} `json:"next"`
}

// SetSchedules replaces the environment's schedules with the list given. The
// control plane validates every expression and zone before it changes
// anything, so a rejected list leaves the earlier schedules in place.
func SetSchedules(ctx context.Context, cfg Config, schedules []Schedule) (*ScheduleList, error) {
	if schedules == nil {
		schedules = []Schedule{}
	}
	body, err := json.Marshal(map[string][]Schedule{"schedules": schedules})
	if err != nil {
		return nil, err
	}
	return scheduleCall(ctx, cfg, http.MethodPut, body)
}

// GetSchedules returns the environment's schedules and their next runs.
func GetSchedules(ctx context.Context, cfg Config) (*ScheduleList, error) {
	return scheduleCall(ctx, cfg, http.MethodGet, nil)
}

// ClearSchedules removes every schedule of the environment.
func ClearSchedules(ctx context.Context, cfg Config) (*ScheduleList, error) {
	return scheduleCall(ctx, cfg, http.MethodDelete, nil)
}

// scheduleCall sends one request to the schedule Lambda. It has its own reply
// shape, so it uses send rather than call, and adds the environment itself.
func scheduleCall(ctx context.Context, cfg Config, method string, body []byte) (*ScheduleList, error) {
	if cfg.ScheduleURL == "" {
		return nil, fmt.Errorf(
			"no schedule_url configured: the control plane predates schedules — re-run `spinloop remote bootstrap` (or set SPINLOOP_REMOTE_SCHEDULE_URL)")
	}
	u, err := url.Parse(cfg.ScheduleURL)
	if err != nil {
		return nil, err
	}
	if cfg.Environment != "" {
		q := u.Query()
		q.Set("env", cfg.Environment)
		u.RawQuery = q.Encode()
	}
	status, respBody, err := send(ctx, cfg, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		var reply struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(respBody, &reply)
		detail := reply.Error
		if detail == "" {
			detail = reply.Message
		}
		if detail == "" {
			detail = string(respBody)
		}
		hint := ""
		if status == http.StatusForbidden {
			hint = forbiddenHint(cfg.Region, detail)
		}
		return nil, fmt.Errorf("schedule returned HTTP %d%s: %s", status, hint, truncate(detail, 200))
	}
	out := &ScheduleList{}
	if err := json.Unmarshal(respBody, out); err != nil {
		return nil, fmt.Errorf("schedule returned an unreadable reply: %s", truncate(string(respBody), 200))
	}
	return out, nil
}
