package reminders

import (
	"fmt"
	"time"
)

type Frequency string

const (
	FrequencyMinute Frequency = "minute"
	FrequencyHour   Frequency = "hour"
	FrequencyDay    Frequency = "day"
	FrequencyWeek   Frequency = "week"
	FrequencyMonth  Frequency = "month"
	FrequencyYear   Frequency = "year"
)

type EndType string

const (
	EndNever      EndType = "never"
	EndOnDate     EndType = "on_date"
	EndAfterCount EndType = "after_count"
)

type MonthlyMode string

const (
	MonthlyDayOfMonth     MonthlyMode = "day_of_month"
	MonthlyLastDayOfMonth MonthlyMode = "last_day_of_month"
	MonthlyNthWeekday     MonthlyMode = "nth_weekday"
)

type RecurrenceEnd struct {
	Type  EndType    `json:"type"`
	Date  *time.Time `json:"date,omitempty"`
	Count int        `json:"count,omitempty"`
}

type RecurrenceRule struct {
	Interval    int            `json:"interval"`
	Frequency   Frequency      `json:"frequency"`
	Weekdays    []time.Weekday `json:"weekdays,omitempty"`
	MonthlyMode MonthlyMode    `json:"monthly_mode,omitempty"`
	NthWeekday  int            `json:"nth_weekday,omitempty"`
	Weekday     time.Weekday   `json:"weekday,omitempty"`
	End         RecurrenceEnd  `json:"end"`
	Timezone    string         `json:"timezone,omitempty"`
}

func (r RecurrenceRule) normalizedInterval() int {
	if r.Interval < 1 {
		return 1
	}
	return r.Interval
}

func (r RecurrenceRule) location() *time.Location {
	if tz := r.Timezone; tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return time.UTC
}

// NextDue returns the first scheduled instant strictly after after, or ok=false if the series has ended.
// occurrenceCount is the number of deliveries already completed (after the latest delivery).
func NextDue(anchor, after time.Time, rule RecurrenceRule, occurrenceCount int) (time.Time, bool) {
	if rule.End.Type == EndAfterCount && rule.End.Count > 0 && occurrenceCount >= rule.End.Count {
		return time.Time{}, false
	}

	loc := rule.location()
	next, ok := computeNext(anchor, after, rule, loc)
	if !ok {
		return time.Time{}, false
	}

	if rule.End.Type == EndOnDate && rule.End.Date != nil {
		endLocal := rule.End.Date.In(loc)
		endOfDay := time.Date(endLocal.Year(), endLocal.Month(), endLocal.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), loc)
		if next.After(endOfDay) {
			return time.Time{}, false
		}
	}

	return next, true
}

func computeNext(anchor, after time.Time, rule RecurrenceRule, loc *time.Location) (time.Time, bool) {
	switch rule.Frequency {
	case FrequencyMinute:
		return nextFixedInterval(anchor, after, rule, time.Minute)
	case FrequencyHour:
		return nextFixedInterval(anchor, after, rule, time.Hour)
	case FrequencyDay:
		return nextDaily(anchor, after, rule, loc)
	case FrequencyWeek:
		return nextWeekly(anchor, after, rule, loc)
	case FrequencyMonth:
		return nextMonthly(anchor, after, rule, loc)
	case FrequencyYear:
		return nextYearly(anchor, after, rule, loc)
	default:
		return time.Time{}, false
	}
}

func nextFixedInterval(anchor, after time.Time, rule RecurrenceRule, unit time.Duration) (time.Time, bool) {
	step := time.Duration(rule.normalizedInterval()) * unit
	if step <= 0 {
		return time.Time{}, false
	}
	if anchor.After(after) {
		return anchor, true
	}
	elapsed := after.Sub(anchor)
	n := elapsed / step
	next := anchor.Add((n + 1) * step)
	if !next.After(after) {
		next = next.Add(step)
	}
	return next, true
}

func nextDaily(anchor, after time.Time, rule RecurrenceRule, loc *time.Location) (time.Time, bool) {
	interval := rule.normalizedInterval()
	anchorLocal := anchor.In(loc)
	h, m, s := anchorLocal.Clock()

	for k := 0; k < 100000; k++ {
		candidate := anchorLocal.AddDate(0, 0, k*interval)
		candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), h, m, s, 0, loc)
		if candidate.After(after) {
			return candidate, true
		}
	}
	return time.Time{}, false
}

func nextWeekly(anchor, after time.Time, rule RecurrenceRule, loc *time.Location) (time.Time, bool) {
	interval := rule.normalizedInterval()
	anchorLocal := anchor.In(loc)
	h, m, s := anchorLocal.Clock()
	weekdays := rule.Weekdays
	if len(weekdays) == 0 {
		weekdays = []time.Weekday{anchorLocal.Weekday()}
	}
	anchorWeekStart := weekStartMonday(anchorLocal)

	afterLocal := after.In(loc)
	startDay := time.Date(afterLocal.Year(), afterLocal.Month(), afterLocal.Day(), 0, 0, 0, 0, loc)

	for dayOffset := 0; dayOffset < 366*3; dayOffset++ {
		day := startDay.AddDate(0, 0, dayOffset)
		if !weekdaySet(weekdays)[day.Weekday()] {
			continue
		}
		candidate := time.Date(day.Year(), day.Month(), day.Day(), h, m, s, 0, loc)
		if !candidate.After(after) {
			continue
		}
		weeks := weeksBetween(anchorWeekStart, weekStartMonday(day))
		if weeks%interval != 0 {
			continue
		}
		return candidate, true
	}
	return time.Time{}, false
}

func nextMonthly(anchor, after time.Time, rule RecurrenceRule, loc *time.Location) (time.Time, bool) {
	interval := rule.normalizedInterval()
	mode := rule.MonthlyMode
	if mode == "" {
		mode = MonthlyDayOfMonth
	}

	for k := 0; k < 1200; k++ {
		candidate := monthCandidate(anchor, k*interval, mode, rule, loc)
		if candidate.IsZero() {
			continue
		}
		if candidate.After(after) {
			return candidate, true
		}
	}
	return time.Time{}, false
}

func nextYearly(anchor, after time.Time, rule RecurrenceRule, loc *time.Location) (time.Time, bool) {
	interval := rule.normalizedInterval()
	mode := rule.MonthlyMode
	if mode == "" {
		mode = MonthlyDayOfMonth
	}

	for k := 0; k < 500; k++ {
		candidate := yearCandidate(anchor, k*interval, mode, rule, loc)
		if candidate.IsZero() {
			continue
		}
		if candidate.After(after) {
			return candidate, true
		}
	}
	return time.Time{}, false
}

func monthCandidate(anchor time.Time, monthOffset int, mode MonthlyMode, rule RecurrenceRule, loc *time.Location) time.Time {
	anchorLocal := anchor.In(loc)
	h, m, s := anchorLocal.Clock()
	y, mo, _ := anchorLocal.Date()
	targetY, targetM := addMonths(y, mo, monthOffset)
	return calendarInstant(targetY, targetM, mode, rule, anchorLocal, h, m, s, loc)
}

func yearCandidate(anchor time.Time, yearOffset int, mode MonthlyMode, rule RecurrenceRule, loc *time.Location) time.Time {
	anchorLocal := anchor.In(loc)
	h, m, s := anchorLocal.Clock()
	y, mo, _ := anchorLocal.Date()
	targetY := y + yearOffset
	return calendarInstant(targetY, mo, mode, rule, anchorLocal, h, m, s, loc)
}

func calendarInstant(year int, month time.Month, mode MonthlyMode, rule RecurrenceRule, anchorLocal time.Time, h, m, s int, loc *time.Location) time.Time {
	switch mode {
	case MonthlyLastDayOfMonth:
		last := lastDayOfMonth(year, month)
		return time.Date(year, month, last, h, m, s, 0, loc)
	case MonthlyNthWeekday:
		return nthWeekdayInMonth(year, month, rule.NthWeekday, rule.Weekday, h, m, s, loc)
	case MonthlyDayOfMonth:
		targetDay := anchorLocal.Day()
		last := lastDayOfMonth(year, month)
		day := targetDay
		if day > last {
			day = last
		}
		return time.Date(year, month, day, h, m, s, 0, loc)
	default:
		return time.Time{}
	}
}

func nthWeekdayInMonth(year int, month time.Month, nth int, weekday time.Weekday, h, m, s int, loc *time.Location) time.Time {
	if nth == -1 {
		last := lastDayOfMonth(year, month)
		for d := last; d >= 1; d-- {
			t := time.Date(year, month, d, h, m, s, 0, loc)
			if t.Weekday() == weekday {
				return t
			}
		}
		return time.Time{}
	}
	if nth < 1 {
		return time.Time{}
	}
	count := 0
	last := lastDayOfMonth(year, month)
	for d := 1; d <= last; d++ {
		t := time.Date(year, month, d, h, m, s, 0, loc)
		if t.Weekday() == weekday {
			count++
			if count == nth {
				return t
			}
		}
	}
	return time.Time{}
}

func lastDayOfMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func addMonths(year int, month time.Month, delta int) (int, time.Month) {
	totalMonths := year*12 + int(month) - 1 + delta
	y := totalMonths / 12
	m := time.Month(totalMonths%12 + 1)
	return y, m
}

func weekStartMonday(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()-(weekday-1), 0, 0, 0, 0, t.Location())
}

func weeksBetween(start, end time.Time) int {
	if end.Before(start) {
		start, end = end, start
	}
	days := int(end.Sub(start).Hours() / 24)
	return days / 7
}

func weekdaySet(days []time.Weekday) map[time.Weekday]bool {
	set := make(map[time.Weekday]bool, len(days))
	for _, d := range days {
		set[d] = true
	}
	return set
}

// ParseWeekday accepts English day names (monday, tue, ...).
func ParseWeekday(name string) (time.Weekday, error) {
	switch name {
	case "sunday", "sun":
		return time.Sunday, nil
	case "monday", "mon":
		return time.Monday, nil
	case "tuesday", "tue":
		return time.Tuesday, nil
	case "wednesday", "wed":
		return time.Wednesday, nil
	case "thursday", "thu":
		return time.Thursday, nil
	case "friday", "fri":
		return time.Friday, nil
	case "saturday", "sat":
		return time.Saturday, nil
	default:
		return time.Sunday, fmt.Errorf("unknown weekday %q", name)
	}
}

// Validate checks recurrence rule fields.
func Validate(rule RecurrenceRule) error {
	if rule.Interval < 1 {
		return fmt.Errorf("interval must be >= 1")
	}
	switch rule.Frequency {
	case FrequencyMinute, FrequencyHour, FrequencyDay, FrequencyWeek, FrequencyMonth, FrequencyYear:
	default:
		return fmt.Errorf("unknown frequency %q", rule.Frequency)
	}
	switch rule.Frequency {
	case FrequencyMonth, FrequencyYear:
		mode := rule.MonthlyMode
		if mode == "" {
			mode = MonthlyDayOfMonth
		}
		switch mode {
		case MonthlyDayOfMonth, MonthlyLastDayOfMonth:
		case MonthlyNthWeekday:
			if rule.NthWeekday == 0 || rule.NthWeekday < -1 || rule.NthWeekday > 5 {
				return fmt.Errorf("nth_weekday must be 1..5 or -1")
			}
			if rule.Weekday < time.Sunday || rule.Weekday > time.Saturday {
				return fmt.Errorf("weekday is required for nth_weekday mode")
			}
		default:
			return fmt.Errorf("unknown monthly_mode %q", mode)
		}
	}
	switch rule.End.Type {
	case EndNever, "":
	case EndOnDate:
		if rule.End.Date == nil || rule.End.Date.IsZero() {
			return fmt.Errorf("end.date is required for on_date")
		}
	case EndAfterCount:
		if rule.End.Count < 1 {
			return fmt.Errorf("end.count must be >= 1 for after_count")
		}
	default:
		return fmt.Errorf("unknown end type %q", rule.End.Type)
	}
	return nil
}
