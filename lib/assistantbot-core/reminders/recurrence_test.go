package reminders

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestMonthlyDayOfMonthNoDrift(t *testing.T) {
	loc := mustLoc(t, "Europe/Moscow")
	anchor := time.Date(2026, 1, 30, 9, 0, 0, 0, loc)
	rule := RecurrenceRule{
		Interval:    1,
		Frequency:   FrequencyMonth,
		MonthlyMode: MonthlyDayOfMonth,
		End:         RecurrenceEnd{Type: EndNever},
		Timezone:    "Europe/Moscow",
	}

	first, ok := NextDue(anchor, anchor.Add(-time.Second), rule, 0)
	if !ok || !first.Equal(anchor) {
		t.Fatalf("first = %v ok=%v", first, ok)
	}

	second, ok := NextDue(anchor, anchor, rule, 1)
	if !ok {
		t.Fatal("expected second occurrence")
	}
	wantFeb := time.Date(2026, 2, 28, 9, 0, 0, 0, loc)
	if !second.Equal(wantFeb) {
		t.Fatalf("second = %v want %v", second, wantFeb)
	}

	third, ok := NextDue(anchor, second, rule, 2)
	if !ok {
		t.Fatal("expected third occurrence")
	}
	wantMar := time.Date(2026, 3, 30, 9, 0, 0, 0, loc)
	if !third.Equal(wantMar) {
		t.Fatalf("third = %v want %v", third, wantMar)
	}
}

func TestMonthlyLastDayOfMonth(t *testing.T) {
	loc := mustLoc(t, "Europe/Moscow")
	anchor := time.Date(2026, 1, 15, 9, 0, 0, 0, loc)
	rule := RecurrenceRule{
		Interval:    1,
		Frequency:   FrequencyMonth,
		MonthlyMode: MonthlyLastDayOfMonth,
		End:         RecurrenceEnd{Type: EndNever},
		Timezone:    "Europe/Moscow",
	}

	first, ok := NextDue(anchor, anchor.Add(-time.Second), rule, 0)
	if !ok {
		t.Fatal("expected first")
	}
	wantJan := time.Date(2026, 1, 31, 9, 0, 0, 0, loc)
	if !first.Equal(wantJan) {
		t.Fatalf("first = %v want %v", first, wantJan)
	}

	second, ok := NextDue(anchor, first, rule, 1)
	if !ok {
		t.Fatal("expected second")
	}
	wantFeb := time.Date(2026, 2, 28, 9, 0, 0, 0, loc)
	if !second.Equal(wantFeb) {
		t.Fatalf("second = %v want %v", second, wantFeb)
	}
}

func TestMonthlyNthWeekday(t *testing.T) {
	loc := mustLoc(t, "UTC")
	anchor := time.Date(2026, 6, 10, 12, 0, 0, 0, loc) // arbitrary anchor
	rule := RecurrenceRule{
		Interval:    1,
		Frequency:   FrequencyMonth,
		MonthlyMode: MonthlyNthWeekday,
		NthWeekday:  2,
		Weekday:     time.Tuesday,
		End:         RecurrenceEnd{Type: EndNever},
		Timezone:    "UTC",
	}

	first, ok := NextDue(anchor, time.Date(2026, 6, 1, 0, 0, 0, 0, loc), rule, 0)
	if !ok {
		t.Fatal("expected first")
	}
	want := time.Date(2026, 6, 9, 12, 0, 0, 0, loc) // 2nd Tuesday of June 2026
	if !first.Equal(want) {
		t.Fatalf("first = %v want %v", first, want)
	}
}

func TestWeeklyInterval(t *testing.T) {
	loc := mustLoc(t, "UTC")
	anchor := time.Date(2026, 6, 2, 9, 0, 0, 0, loc) // Tuesday
	rule := RecurrenceRule{
		Interval:  2,
		Frequency: FrequencyWeek,
		Weekdays:  []time.Weekday{time.Tuesday},
		End:       RecurrenceEnd{Type: EndNever},
		Timezone:  "UTC",
	}

	second, ok := NextDue(anchor, anchor, rule, 1)
	if !ok {
		t.Fatal("expected second")
	}
	want := time.Date(2026, 6, 16, 9, 0, 0, 0, loc)
	if !second.Equal(want) {
		t.Fatalf("second = %v want %v", second, want)
	}
}

func TestHourlyInterval(t *testing.T) {
	anchor := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	rule := RecurrenceRule{
		Interval:  2,
		Frequency: FrequencyHour,
		End:       RecurrenceEnd{Type: EndNever},
		Timezone:  "UTC",
	}

	second, ok := NextDue(anchor, anchor, rule, 1)
	if !ok {
		t.Fatal("expected second")
	}
	want := time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC)
	if !second.Equal(want) {
		t.Fatalf("second = %v want %v", second, want)
	}
}

func TestMinutelyInterval(t *testing.T) {
	anchor := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	rule := RecurrenceRule{
		Interval:  15,
		Frequency: FrequencyMinute,
		End:       RecurrenceEnd{Type: EndNever},
		Timezone:  "UTC",
	}

	second, ok := NextDue(anchor, anchor, rule, 1)
	if !ok {
		t.Fatal("expected second")
	}
	want := time.Date(2026, 6, 1, 9, 15, 0, 0, time.UTC)
	if !second.Equal(want) {
		t.Fatalf("second = %v want %v", second, want)
	}
}

func TestMinutelyIntervalOldAnchor(t *testing.T) {
	anchor := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC)
	after := time.Date(2026, 6, 1, 9, 44, 0, 0, time.UTC)
	rule := RecurrenceRule{
		Interval:  15,
		Frequency: FrequencyMinute,
		End:       RecurrenceEnd{Type: EndNever},
		Timezone:  "UTC",
	}

	next, ok := NextDue(anchor, after, rule, 0)
	if !ok {
		t.Fatal("expected next occurrence with old anchor")
	}
	want := time.Date(2026, 6, 1, 9, 45, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v want %v", next, want)
	}
}

func TestDailyInterval(t *testing.T) {
	loc := mustLoc(t, "UTC")
	anchor := time.Date(2026, 6, 1, 9, 0, 0, 0, loc)
	rule := RecurrenceRule{
		Interval:  2,
		Frequency: FrequencyDay,
		End:       RecurrenceEnd{Type: EndNever},
		Timezone:  "UTC",
	}

	second, ok := NextDue(anchor, anchor, rule, 1)
	if !ok {
		t.Fatal("expected second")
	}
	want := time.Date(2026, 6, 3, 9, 0, 0, 0, loc)
	if !second.Equal(want) {
		t.Fatalf("second = %v want %v", second, want)
	}
}

func TestEndAfterCount(t *testing.T) {
	loc := mustLoc(t, "UTC")
	anchor := time.Date(2026, 6, 1, 9, 0, 0, 0, loc)
	rule := RecurrenceRule{
		Interval:  1,
		Frequency: FrequencyDay,
		End:       RecurrenceEnd{Type: EndAfterCount, Count: 3},
		Timezone:  "UTC",
	}

	_, ok := NextDue(anchor, anchor, rule, 3)
	if ok {
		t.Fatal("series should end after 3 deliveries")
	}
}

func TestEndOnDate(t *testing.T) {
	loc := mustLoc(t, "UTC")
	anchor := time.Date(2026, 6, 1, 9, 0, 0, 0, loc)
	end := time.Date(2026, 6, 3, 0, 0, 0, 0, loc)
	rule := RecurrenceRule{
		Interval:  1,
		Frequency: FrequencyDay,
		End:       RecurrenceEnd{Type: EndOnDate, Date: &end},
		Timezone:  "UTC",
	}

	next, ok := NextDue(anchor, time.Date(2026, 6, 2, 9, 0, 0, 0, loc), rule, 2)
	if !ok {
		t.Fatal("expected next on end date")
	}
	want := time.Date(2026, 6, 3, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %v want %v", next, want)
	}

	_, ok = NextDue(anchor, next, rule, 3)
	if ok {
		t.Fatal("should not schedule after end date")
	}
}

func TestOfflineSkipToFuture(t *testing.T) {
	loc := mustLoc(t, "UTC")
	anchor := time.Date(2026, 6, 1, 9, 0, 0, 0, loc)
	rule := RecurrenceRule{
		Interval:  1,
		Frequency: FrequencyDay,
		End:       RecurrenceEnd{Type: EndNever},
		Timezone:  "UTC",
	}

	now := time.Date(2026, 6, 10, 12, 0, 0, 0, loc)
	next, ok := NextDue(anchor, now, rule, 1)
	if !ok {
		t.Fatal("expected next")
	}
	want := time.Date(2026, 6, 11, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %v want %v", next, want)
	}
}

func TestYearlyDayOfMonth(t *testing.T) {
	loc := mustLoc(t, "UTC")
	anchor := time.Date(2026, 9, 1, 9, 0, 0, 0, loc)
	rule := RecurrenceRule{
		Interval:    1,
		Frequency:   FrequencyYear,
		MonthlyMode: MonthlyDayOfMonth,
		End:         RecurrenceEnd{Type: EndNever},
		Timezone:    "UTC",
	}

	next, ok := NextDue(anchor, anchor, rule, 1)
	if !ok {
		t.Fatal("expected next")
	}
	want := time.Date(2027, 9, 1, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %v want %v", next, want)
	}
}
