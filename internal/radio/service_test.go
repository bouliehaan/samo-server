package radio

import (
	"testing"
	"time"
)

func TestCurrentSlotUsesDeterministicRotation(t *testing.T) {
	service, err := NewService(Config{Stations: []StationConfig{{
		ID:    "memory",
		Name:  "Memory Radio",
		Epoch: "2026-01-01T00:00:00Z",
		Media: []MediaItemConfig{
			{ID: "first", Title: "First", Path: "/tmp/first.mp3", DurationSeconds: 60},
			{ID: "second", Title: "Second", Path: "/tmp/second.mp3", DurationSeconds: 30},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	at := mustParseTime(t, "2026-01-01T00:01:10Z")
	slot, err := service.CurrentSlot("memory", at)
	if err != nil {
		t.Fatal(err)
	}

	if slot.MediaID != "second" {
		t.Fatalf("MediaID = %q, want second", slot.MediaID)
	}
	if slot.OffsetSeconds != 10 {
		t.Fatalf("OffsetSeconds = %d, want 10", slot.OffsetSeconds)
	}
}

func TestWeightedItemsRepeatInRotation(t *testing.T) {
	service, err := NewService(Config{Stations: []StationConfig{{
		ID:    "weighted",
		Epoch: "2026-01-01T00:00:00Z",
		Media: []MediaItemConfig{
			{ID: "a", Title: "A", Path: "/tmp/a.mp3", DurationSeconds: 10, Weight: 2},
			{ID: "b", Title: "B", Path: "/tmp/b.mp3", DurationSeconds: 10},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		at   string
		want string
	}{
		{"2026-01-01T00:00:05Z", "a"},
		{"2026-01-01T00:00:15Z", "a"},
		{"2026-01-01T00:00:25Z", "b"},
		{"2026-01-01T00:00:35Z", "a"},
	}

	for _, tt := range tests {
		slot, err := service.CurrentSlot("weighted", mustParseTime(t, tt.at))
		if err != nil {
			t.Fatal(err)
		}
		if slot.MediaID != tt.want {
			t.Fatalf("at %s MediaID = %q, want %q", tt.at, slot.MediaID, tt.want)
		}
	}
}

func TestUpcomingStartsWithCurrentSlot(t *testing.T) {
	service, err := NewService(Config{Stations: []StationConfig{{
		ID:    "schedule",
		Epoch: "2026-01-01T00:00:00Z",
		Media: []MediaItemConfig{
			{ID: "a", Title: "A", Path: "/tmp/a.mp3", DurationSeconds: 10},
			{ID: "b", Title: "B", Path: "/tmp/b.mp3", DurationSeconds: 20},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	slots, err := service.Upcoming("schedule", mustParseTime(t, "2026-01-01T00:00:05Z"), 3)
	if err != nil {
		t.Fatal(err)
	}

	got := []string{slots[0].MediaID, slots[1].MediaID, slots[2].MediaID}
	want := []string{"a", "b", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slot %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
