package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is standard five-field local-time cron: minute hour day month weekday.
// Supports *, */N, single values, ranges and comma-separated fields.
type Cron struct {
	bits                      [5]map[int]bool
	wildcardDay, wildcardWeek bool
}

func ParseCron(spec string) (Cron, error) {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return Cron{}, fmt.Errorf("expected 5-field cron: minute hour day month weekday")
	}
	bounds := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	var cron Cron
	cron.wildcardDay = fields[2] == "*"
	cron.wildcardWeek = fields[4] == "*"
	for i, f := range fields {
		selected := map[int]bool{}
		for _, chunk := range strings.Split(f, ",") {
			step := 1
			base := chunk
			if strings.Contains(chunk, "/") {
				parts := strings.Split(chunk, "/")
				if len(parts) != 2 {
					return cron, fmt.Errorf("bad step %q", chunk)
				}
				n, e := strconv.Atoi(parts[1])
				if e != nil || n < 1 {
					return cron, fmt.Errorf("bad step %q", chunk)
				}
				step = n
				base = parts[0]
			}
			low, high := bounds[i][0], bounds[i][1]
			if base != "*" {
				if strings.Contains(base, "-") {
					parts := strings.Split(base, "-")
					if len(parts) != 2 {
						return cron, fmt.Errorf("bad range %q", base)
					}
					var e error
					low, e = strconv.Atoi(parts[0])
					if e != nil {
						return cron, e
					}
					high, e = strconv.Atoi(parts[1])
					if e != nil {
						return cron, e
					}
				} else {
					n, e := strconv.Atoi(base)
					if e != nil {
						return cron, e
					}
					low = n
					high = n
				}
			}
			if low < bounds[i][0] || high > bounds[i][1] || low > high {
				return cron, fmt.Errorf("out of range %q", chunk)
			}
			for x := low; x <= high; x += step {
				v := x
				if i == 4 && v == 7 {
					v = 0
				}
				selected[v] = true
			}
		}
		cron.bits[i] = selected
	}
	return cron, nil
}
func (c Cron) Matches(t time.Time) bool {
	weekday := int(t.Weekday())
	if !c.bits[0][t.Minute()] || !c.bits[1][t.Hour()] || !c.bits[3][int(t.Month())] {
		return false
	}
	day, week := c.bits[2][t.Day()], c.bits[4][weekday]
	// Conventional crontab semantics: when both day-of-month and weekday are
	// restricted, either match satisfies the day condition.
	if !c.wildcardDay && !c.wildcardWeek {
		return day || week
	}
	return day && week
}
func (c Cron) Next(after time.Time, loc *time.Location) (time.Time, error) {
	t := after.UTC().Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 527040; i++ {
		if c.Matches(t.In(loc)) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("no cron match within one year")
}
