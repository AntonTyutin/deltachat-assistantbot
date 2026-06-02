package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AntonTyutin/assistantbot-core/reminders"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

func recurrenceToolSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "Optional recurrence like Google Calendar",
		"properties": map[string]any{
			"interval":     map[string]any{"type": "integer", "description": "Repeat every N units (default 1)"},
			"frequency":    map[string]any{"type": "string", "enum": []string{"minute", "hour", "day", "week", "month", "year"}},
			"weekdays":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "For weekly: monday..sunday"},
			"monthly_mode": map[string]any{"type": "string", "enum": []string{"day_of_month", "last_day_of_month", "nth_weekday"}},
			"nth_weekday":  map[string]any{"type": "integer", "description": "For nth_weekday: 1..5 or -1 for last"},
			"weekday":      map[string]any{"type": "string", "description": "Weekday name for nth_weekday mode"},
			"end": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":  map[string]any{"type": "string", "enum": []string{"never", "on_date", "after_count"}},
					"date":  map[string]any{"type": "string", "description": "RFC3339 end date for on_date"},
					"count": map[string]any{"type": "integer", "description": "Max deliveries for after_count"},
				},
			},
		},
	}
}

func (r *ToolRegistry) updateReminder(ctx context.Context, chatID, requesterID, argumentsJSON string) (string, error) {
	var args struct {
		ReminderID string          `json:"reminder_id"`
		Text       string          `json:"text"`
		DueAt      string          `json:"due_at"`
		Time       string          `json:"time"`
		Weekdays   []string        `json:"weekdays"`
		Recurrence json.RawMessage `json:"recurrence"`
	}
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return "", err
	}
	reminderID := strings.TrimSpace(args.ReminderID)
	if reminderID == "" {
		return "", fmt.Errorf("reminder_id is required")
	}
	if args.Text == "" && args.DueAt == "" && args.Time == "" && len(args.Weekdays) == 0 && len(args.Recurrence) == 0 {
		return "", fmt.Errorf("at least one of text, due_at, time, weekdays, or recurrence is required")
	}

	reminder, ok, err := r.store.GetReminder(ctx, chatID, reminderID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("reminder not found")
	}

	tz := r.requesterTimezone(ctx, requesterID)
	if tz == "" {
		tz = "UTC"
	}
	if reminder.Recurrence != nil && reminder.Recurrence.Timezone != "" {
		tz = reminder.Recurrence.Timezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}

	if args.Text != "" {
		reminder.Text = args.Text
	}
	if len(args.Recurrence) > 0 {
		merged, err := mergeRecurrenceRule(reminder.Recurrence, args.Recurrence, tz)
		if err != nil {
			return "", err
		}
		reminder.Recurrence = merged
	}

	anchor := reminder.AnchorAt
	if anchor.IsZero() {
		anchor = reminder.DueAt
	}

	switch {
	case args.DueAt != "":
		newAnchor, err := r.parseReminderDueAt(ctx, requesterID, args.DueAt)
		if err != nil {
			return "", err
		}
		anchor = newAnchor
	case args.Time != "":
		anchor, err = applyLocalTime(anchor, args.Time, loc)
		if err != nil {
			return "", err
		}
	case len(args.Weekdays) > 0:
		days, err := parseWeekdayList(args.Weekdays)
		if err != nil {
			return "", err
		}
		anchor, err = applyWeekdays(reminder, anchor, days, loc)
		if err != nil {
			return "", err
		}
	}

	now := time.Now()
	if err := rescheduleReminder(&reminder, anchor, now); err != nil {
		return "", err
	}
	if err := r.store.UpsertReminder(ctx, reminder); err != nil {
		return "", err
	}

	out := map[string]any{
		"status":      "updated",
		"reminder_id": reminder.ID,
		"due_at":      reminder.DueAt.Format(time.RFC3339),
		"anchor_at":   reminder.AnchorAt.Format(time.RFC3339),
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func parseWeekdayList(names []string) ([]time.Weekday, error) {
	days := make([]time.Weekday, 0, len(names))
	for _, name := range names {
		day, err := reminders.ParseWeekday(strings.ToLower(strings.TrimSpace(name)))
		if err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	if len(days) == 0 {
		return nil, fmt.Errorf("weekdays must not be empty")
	}
	return days, nil
}

func applyLocalTime(anchor time.Time, timeValue string, loc *time.Location) (time.Time, error) {
	hour, min, sec, err := parseLocalClock(timeValue)
	if err != nil {
		return time.Time{}, err
	}
	anchor = anchor.In(loc)
	y, mo, d := anchor.Date()
	return time.Date(y, mo, d, hour, min, sec, 0, loc), nil
}

func parseLocalClock(value string) (hour, min, sec int, err error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, parseErr := time.Parse(layout, value); parseErr == nil {
			hour, min, sec = t.Clock()
			return hour, min, sec, nil
		}
	}
	return 0, 0, 0, fmt.Errorf("time must be HH:MM or HH:MM:SS")
}

func applyWeekdays(reminder storage.Reminder, anchor time.Time, weekdays []time.Weekday, loc *time.Location) (time.Time, error) {
	anchor = anchor.In(loc)
	h, m, s := anchor.Clock()
	from := time.Now().In(loc)

	if reminder.Recurrence != nil && reminder.Recurrence.Frequency == reminders.FrequencyWeek {
		reminder.Recurrence.Weekdays = weekdays
	}
	next := nextWeekdayWithClock(from, weekdays, h, m, s, loc)
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("could not find next weekday")
	}
	return next, nil
}

func nextWeekdayWithClock(from time.Time, weekdays []time.Weekday, hour, min, sec int, loc *time.Location) time.Time {
	set := make(map[time.Weekday]bool, len(weekdays))
	for _, d := range weekdays {
		set[d] = true
	}
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	for offset := 0; offset < 370; offset++ {
		day := start.AddDate(0, 0, offset)
		candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, min, sec, 0, loc)
		if !set[candidate.Weekday()] {
			continue
		}
		if !candidate.Before(from) {
			return candidate
		}
	}
	return time.Time{}
}

func rescheduleReminder(reminder *storage.Reminder, anchor time.Time, now time.Time) error {
	reminder.AnchorAt = anchor.UTC()
	if reminder.Recurrence == nil {
		reminder.DueAt = reminder.AnchorAt
		return nil
	}
	next, ok := reminders.NextDue(reminder.AnchorAt, now.Add(-time.Nanosecond), *reminder.Recurrence, reminder.OccurrenceCount)
	if !ok {
		return fmt.Errorf("no upcoming occurrence after update")
	}
	reminder.DueAt = next.UTC()
	return nil
}
