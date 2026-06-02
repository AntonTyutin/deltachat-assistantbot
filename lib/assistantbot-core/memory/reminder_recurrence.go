package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AntonTyutin/assistantbot-core/reminders"
)

type recurrenceArgs struct {
	Interval    int      `json:"interval"`
	Frequency   string   `json:"frequency"`
	Weekdays    []string `json:"weekdays"`
	MonthlyMode string   `json:"monthly_mode"`
	NthWeekday  int      `json:"nth_weekday"`
	Weekday     string   `json:"weekday"`
	End         struct {
		Type  string `json:"type"`
		Date  string `json:"date"`
		Count int    `json:"count"`
	} `json:"end"`
}

func parseRecurrenceRule(raw json.RawMessage, timezone string) (*reminders.RecurrenceRule, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var args recurrenceArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Frequency == "" {
		return nil, nil
	}

	rule := reminders.RecurrenceRule{
		Interval:    args.Interval,
		Frequency:   reminders.Frequency(strings.ToLower(strings.TrimSpace(args.Frequency))),
		MonthlyMode: reminders.MonthlyMode(strings.ToLower(strings.TrimSpace(args.MonthlyMode))),
		NthWeekday:  args.NthWeekday,
		Timezone:    timezone,
	}
	if rule.Interval < 1 {
		rule.Interval = 1
	}
	days, err := parseWeekdayNames(args.Weekdays)
	if err != nil {
		return nil, err
	}
	rule.Weekdays = days
	if wd := strings.TrimSpace(args.Weekday); wd != "" {
		day, err := reminders.ParseWeekday(strings.ToLower(wd))
		if err != nil {
			return nil, err
		}
		rule.Weekday = day
	}
	endType := strings.ToLower(strings.TrimSpace(args.End.Type))
	if endType == "" {
		endType = string(reminders.EndNever)
	}
	rule.End.Type = reminders.EndType(endType)
	switch rule.End.Type {
	case reminders.EndOnDate:
		if args.End.Date == "" {
			return nil, fmt.Errorf("recurrence.end.date is required for on_date")
		}
		endDate, err := time.Parse(time.RFC3339, args.End.Date)
		if err != nil {
			return nil, fmt.Errorf("recurrence.end.date: %w", err)
		}
		rule.End.Date = &endDate
	case reminders.EndAfterCount:
		rule.End.Count = args.End.Count
	}
	if err := reminders.Validate(rule); err != nil {
		return nil, err
	}
	return &rule, nil
}

// mergeRecurrenceRule applies a partial recurrence patch onto existing (if any).
func mergeRecurrenceRule(existing *reminders.RecurrenceRule, patch json.RawMessage, timezone string) (*reminders.RecurrenceRule, error) {
	if len(patch) == 0 || string(patch) == "null" {
		return existing, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(patch, &fields); err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return existing, nil
	}

	rule := reminders.RecurrenceRule{
		Interval:  1,
		Frequency: "",
		End:       reminders.RecurrenceEnd{Type: reminders.EndNever},
		Timezone:  timezone,
	}
	if existing != nil {
		rule = *existing
	}
	if rule.Timezone == "" {
		rule.Timezone = timezone
	}

	if raw, ok := fields["interval"]; ok {
		var interval int
		if err := json.Unmarshal(raw, &interval); err != nil {
			return nil, err
		}
		rule.Interval = interval
	}
	if raw, ok := fields["frequency"]; ok {
		var frequency string
		if err := json.Unmarshal(raw, &frequency); err != nil {
			return nil, err
		}
		frequency = strings.ToLower(strings.TrimSpace(frequency))
		if frequency == "" {
			return nil, fmt.Errorf("recurrence.frequency must not be empty")
		}
		rule.Frequency = reminders.Frequency(frequency)
	}
	if raw, ok := fields["weekdays"]; ok {
		var names []string
		if err := json.Unmarshal(raw, &names); err != nil {
			return nil, err
		}
		days, err := parseWeekdayNames(names)
		if err != nil {
			return nil, err
		}
		rule.Weekdays = days
	}
	if raw, ok := fields["monthly_mode"]; ok {
		var mode string
		if err := json.Unmarshal(raw, &mode); err != nil {
			return nil, err
		}
		mode = strings.ToLower(strings.TrimSpace(mode))
		if mode == "" {
			return nil, fmt.Errorf("recurrence.monthly_mode must not be empty")
		}
		rule.MonthlyMode = reminders.MonthlyMode(mode)
	}
	if raw, ok := fields["nth_weekday"]; ok {
		var nth int
		if err := json.Unmarshal(raw, &nth); err != nil {
			return nil, err
		}
		rule.NthWeekday = nth
	}
	if raw, ok := fields["weekday"]; ok {
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return nil, err
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return nil, fmt.Errorf("recurrence.weekday must not be empty")
		}
		day, err := reminders.ParseWeekday(name)
		if err != nil {
			return nil, err
		}
		rule.Weekday = day
	}
	if raw, ok := fields["end"]; ok {
		if err := mergeRecurrenceEnd(&rule.End, raw); err != nil {
			return nil, err
		}
	}

	if rule.Frequency == "" {
		if existing == nil {
			return nil, fmt.Errorf("recurrence.frequency is required when adding recurrence")
		}
		return nil, fmt.Errorf("recurrence is missing frequency")
	}
	if rule.Interval < 1 {
		rule.Interval = 1
	}
	if err := reminders.Validate(rule); err != nil {
		return nil, err
	}
	return &rule, nil
}

func mergeRecurrenceEnd(end *reminders.RecurrenceEnd, patch json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(patch, &fields); err != nil {
		return err
	}
	if len(fields) == 0 {
		return nil
	}
	if raw, ok := fields["type"]; ok {
		var endType string
		if err := json.Unmarshal(raw, &endType); err != nil {
			return err
		}
		endType = strings.ToLower(strings.TrimSpace(endType))
		if endType == "" {
			return fmt.Errorf("recurrence.end.type must not be empty")
		}
		end.Type = reminders.EndType(endType)
		if end.Type == reminders.EndNever {
			end.Date = nil
			end.Count = 0
		}
	}
	if raw, ok := fields["date"]; ok {
		var dateStr string
		if err := json.Unmarshal(raw, &dateStr); err != nil {
			return err
		}
		dateStr = strings.TrimSpace(dateStr)
		if dateStr == "" {
			end.Date = nil
		} else {
			endDate, err := time.Parse(time.RFC3339, dateStr)
			if err != nil {
				return fmt.Errorf("recurrence.end.date: %w", err)
			}
			end.Date = &endDate
		}
	}
	if raw, ok := fields["count"]; ok {
		var count int
		if err := json.Unmarshal(raw, &count); err != nil {
			return err
		}
		end.Count = count
	}
	return nil
}

func parseWeekdayNames(names []string) ([]time.Weekday, error) {
	days := make([]time.Weekday, 0, len(names))
	for _, name := range names {
		day, err := reminders.ParseWeekday(strings.ToLower(strings.TrimSpace(name)))
		if err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, nil
}

func (r *ToolRegistry) requesterTimezone(ctx context.Context, requesterID string) string {
	if requesterID == "" {
		return ""
	}
	profile, ok, err := r.store.GetProfile(ctx, requesterID)
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(profile.Timezone)
}
