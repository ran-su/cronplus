package core

import (
	"testing"
	"time"
)

func TestParseCron_Valid(t *testing.T) {
	tests := []struct {
		expr string
	}{
		{"* * * * *"},
		{"*/5 * * * *"},
		{"0 * * * *"},
		{"0 9 * * 1-5"},
		{"30 2 1 * *"},
		{"0,15,30,45 * * * *"},
		{"0 0 * * 0"},
		{"0 0 * * 7"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			_, err := ParseCron(tt.expr)
			if err != nil {
				t.Errorf("ParseCron(%q) returned error: %v", tt.expr, err)
			}
		})
	}
}

func TestParseCron_Invalid(t *testing.T) {
	tests := []struct {
		expr string
	}{
		{""},
		{"* *"},
		{"* * * *"},
		{"* * * * * *"},
		{"*/0 * * * *"},
		{"abc * * * *"},
		{"5-2 * * * *"},
		{"60 * * * *"},
		{"* 24 * * *"},
		{"* * 0 * *"},
		{"* * * 13 *"},
		{"* * * * 8"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			_, err := ParseCron(tt.expr)
			if err == nil {
				t.Errorf("ParseCron(%q) should have returned error", tt.expr)
			}
		})
	}
}

func TestCronMatches(t *testing.T) {
	tests := []struct {
		expr    string
		time    time.Time
		matches bool
	}{
		{"* * * * *", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"0 * * * *", time.Date(2025, 1, 1, 5, 0, 0, 0, time.UTC), true},
		{"0 * * * *", time.Date(2025, 1, 1, 5, 30, 0, 0, time.UTC), false},
		{"*/15 * * * *", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"*/15 * * * *", time.Date(2025, 1, 1, 0, 15, 0, 0, time.UTC), true},
		{"*/15 * * * *", time.Date(2025, 1, 1, 0, 7, 0, 0, time.UTC), false},
		{"0 9 * * 1", time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC), true},  // Monday
		{"0 9 * * 1", time.Date(2025, 1, 7, 9, 0, 0, 0, time.UTC), false}, // Tuesday
		{"0 9 1 * 1", time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC), true},  // day-of-month match
		{"0 9 1 * 1", time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC), true},  // day-of-week match
		{"0 9 1 * 1", time.Date(2025, 1, 2, 9, 0, 0, 0, time.UTC), false}, // neither day field matches
		{"0 0 1 1 *", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), true},  // Jan 1
		{"0 0 1 1 *", time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC), false}, // Feb 1
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			cron, err := ParseCron(tt.expr)
			if err != nil {
				t.Fatalf("ParseCron(%q) error: %v", tt.expr, err)
			}
			got := cron.Matches(tt.time)
			if got != tt.matches {
				t.Errorf("Matches(%v) = %v, want %v", tt.time, got, tt.matches)
			}
		})
	}
}

func TestCronNextRun(t *testing.T) {
	// "0 * * * *" = every hour at :00
	cron, _ := ParseCron("0 * * * *")
	after := time.Date(2025, 1, 1, 5, 30, 0, 0, time.UTC)
	next := cron.NextRun(after, time.UTC)

	if next == nil {
		t.Fatal("NextRun returned nil")
	}
	expected := time.Date(2025, 1, 1, 6, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("NextRun = %v, want %v", next, expected)
	}
}

func TestSundayAlias(t *testing.T) {
	// Both 0 and 7 should match Sunday
	cron0, _ := ParseCron("0 0 * * 0")
	cron7, _ := ParseCron("0 0 * * 7")

	sunday := time.Date(2025, 1, 5, 0, 0, 0, 0, time.UTC) // Sunday
	monday := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC) // Monday

	if !cron0.Matches(sunday) {
		t.Error("day-of-week 0 should match Sunday")
	}
	if !cron7.Matches(sunday) {
		t.Error("day-of-week 7 should match Sunday")
	}
	if cron0.Matches(monday) {
		t.Error("day-of-week 0 should not match Monday")
	}
	if cron7.Matches(monday) {
		t.Error("day-of-week 7 should not match Monday")
	}
}
