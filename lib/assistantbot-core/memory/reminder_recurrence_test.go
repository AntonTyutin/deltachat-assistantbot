package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/reminders"
)

func TestMergeRecurrenceEndOnly(t *testing.T) {
	existing := &reminders.RecurrenceRule{
		Interval:  1,
		Frequency: reminders.FrequencyWeek,
		Weekdays:  []time.Weekday{time.Tuesday},
		End:       reminders.RecurrenceEnd{Type: reminders.EndNever},
		Timezone:  "UTC",
	}
	patch := json.RawMessage(`{"end":{"type":"after_count","count":5}}`)
	merged, err := mergeRecurrenceRule(existing, patch, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if merged.Frequency != reminders.FrequencyWeek {
		t.Fatalf("frequency = %q", merged.Frequency)
	}
	if merged.End.Type != reminders.EndAfterCount || merged.End.Count != 5 {
		t.Fatalf("end = %+v", merged.End)
	}
}

func TestMergeRecurrenceRequiresFrequencyWhenAdding(t *testing.T) {
	patch := json.RawMessage(`{"end":{"type":"never"}}`)
	_, err := mergeRecurrenceRule(nil, patch, "UTC")
	if err == nil {
		t.Fatal("expected error when adding recurrence without frequency")
	}
}

func TestMergeRecurrencePartialDoesNotClearFrequency(t *testing.T) {
	existing := &reminders.RecurrenceRule{
		Interval:  1,
		Frequency: reminders.FrequencyDay,
		End:       reminders.RecurrenceEnd{Type: reminders.EndNever},
		Timezone:  "UTC",
	}
	patch := json.RawMessage(`{"interval":2}`)
	merged, err := mergeRecurrenceRule(existing, patch, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if merged.Frequency != reminders.FrequencyDay {
		t.Fatalf("frequency = %q", merged.Frequency)
	}
	if merged.Interval != 2 {
		t.Fatalf("interval = %d", merged.Interval)
	}
}
