package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronExpression represents a parsed 5-field cron expression.
type CronExpression struct {
	Minute     cronField
	Hour       cronField
	DayOfMonth cronField
	Month      cronField
	DayOfWeek  cronField
}

// ParseCron parses a standard 5-field cron expression string.
func ParseCron(expr string) (*CronExpression, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields, got %d", len(parts))
	}

	minute, err := parseField(parts[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minute field: %w", err)
	}
	hour, err := parseField(parts[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hour field: %w", err)
	}
	dom, err := parseField(parts[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("day-of-month field: %w", err)
	}
	month, err := parseField(parts[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("month field: %w", err)
	}
	dow, err := parseField(parts[4], 0, 7)
	if err != nil {
		return nil, fmt.Errorf("day-of-week field: %w", err)
	}

	return &CronExpression{
		Minute:     minute,
		Hour:       hour,
		DayOfMonth: dom,
		Month:      month,
		DayOfWeek:  dow,
	}, nil
}

// Matches returns true if the cron expression matches the given time.
func (c *CronExpression) Matches(t time.Time) bool {
	// Convert Go weekday (Sunday=0) to cron (Sunday=0 or 7)
	dow := int(t.Weekday())
	dayOfMonthMatches := c.DayOfMonth.contains(t.Day())
	dayOfWeekMatches := c.DayOfWeek.contains(dow)
	dayMatches := dayOfMonthMatches && dayOfWeekMatches
	if !c.DayOfMonth.wildcard && !c.DayOfWeek.wildcard {
		dayMatches = dayOfMonthMatches || dayOfWeekMatches
	}

	return c.Minute.contains(t.Minute()) &&
		c.Hour.contains(t.Hour()) &&
		c.Month.contains(int(t.Month())) &&
		dayMatches
}

// NextRun finds the next time after 'after' that matches the expression.
// Searches up to one year ahead.
func (c *CronExpression) NextRun(after time.Time, loc *time.Location) *time.Time {
	// Truncate to minute and advance by 1 minute
	t := after.In(loc).Truncate(time.Minute).Add(time.Minute)

	limit := 60 * 24 * 366 // one year of minutes
	for i := 0; i < limit; i++ {
		if c.Matches(t) {
			result := t
			return &result
		}
		t = t.Add(time.Minute)
	}
	return nil
}

// --- field parsing ---

type cronField struct {
	matcher  func(int) bool
	wildcard bool
}

func (f cronField) contains(val int) bool {
	return f.matcher(val)
}

func parseField(raw string, min, max int) (cronField, error) {
	if raw == "*" {
		return cronField{matcher: func(v int) bool { return v >= min && v <= max }, wildcard: true}, nil
	}

	// Step: */N
	if strings.HasPrefix(raw, "*/") {
		step, err := strconv.Atoi(raw[2:])
		if err != nil || step <= 0 {
			return cronField{}, fmt.Errorf("invalid step %q", raw)
		}
		if step > max-min+1 {
			return cronField{}, fmt.Errorf("step %q exceeds field range", raw)
		}
		return cronField{matcher: func(v int) bool {
			return v >= min && v <= max && ((v-min)%step == 0)
		}}, nil
	}

	// Comma-separated list: "1,3-5,7"
	allowed := make(map[int]bool)
	for _, seg := range strings.Split(raw, ",") {
		seg = strings.TrimSpace(seg)
		if idx := strings.Index(seg, "-"); idx >= 0 {
			lo, err1 := strconv.Atoi(seg[:idx])
			hi, err2 := strconv.Atoi(seg[idx+1:])
			if err1 != nil || err2 != nil || lo > hi {
				return cronField{}, fmt.Errorf("invalid range %q", seg)
			}
			if lo < min || hi > max {
				return cronField{}, fmt.Errorf("range %q out of bounds (%d-%d)", seg, min, max)
			}
			for v := lo; v <= hi; v++ {
				addWithSundayAlias(allowed, v, max)
			}
		} else {
			v, err := strconv.Atoi(seg)
			if err != nil {
				return cronField{}, fmt.Errorf("invalid value %q", seg)
			}
			if v < min || v > max {
				return cronField{}, fmt.Errorf("value %q out of bounds (%d-%d)", seg, min, max)
			}
			addWithSundayAlias(allowed, v, max)
		}
	}

	if len(allowed) == 0 {
		return cronField{}, fmt.Errorf("empty field")
	}

	return cronField{matcher: func(v int) bool { return allowed[v] }}, nil
}

// addWithSundayAlias handles day-of-week where 7 is an alias for 0 (both = Sunday).
func addWithSundayAlias(m map[int]bool, v, max int) {
	m[v] = true
	if max == 7 {
		if v == 7 {
			m[0] = true
		}
		if v == 0 {
			m[7] = true
		}
	}
}
