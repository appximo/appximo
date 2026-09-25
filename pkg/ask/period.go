package ask

import (
	"strings"
	"time"
)

// Window is a half-open [From, To) time range resolved by the ENGINE from a
// period token, in the app's timezone. Models get dates wrong; the token
// vocabulary is closed and the arithmetic is here.
type Window struct {
	From, To time.Time
	Words    string // the Spanish phrase for the reply ("hoy", "esta semana")
}

// Resolve turns a range token into a Window around now. Weeks start on
// Monday. "last_7_days" is the last 7 whole days including today.
func Resolve(rng string, now time.Time) (Window, bool) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	switch rng {
	case "today":
		return Window{day, day.AddDate(0, 0, 1), "hoy"}, true
	case "yesterday":
		return Window{day.AddDate(0, 0, -1), day, "ayer"}, true
	case "day_before_yesterday":
		return Window{day.AddDate(0, 0, -2), day.AddDate(0, 0, -1), "antier"}, true
	case "this_week":
		return Window{monday, monday.AddDate(0, 0, 7), "esta semana"}, true
	case "last_week":
		return Window{monday.AddDate(0, 0, -7), monday, "la semana pasada"}, true
	case "this_month":
		return Window{month, month.AddDate(0, 1, 0), "este mes"}, true
	case "last_month":
		return Window{month.AddDate(0, -1, 0), month, "el mes pasado"}, true
	case "last_7_days":
		return Window{day.AddDate(0, 0, -6), day.AddDate(0, 0, 1), "en los últimos 7 días"}, true
	case "last_30_days":
		return Window{day.AddDate(0, 0, -29), day.AddDate(0, 0, 1), "en los últimos 30 días"}, true
	case "this_year":
		y := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
		return Window{y, y.AddDate(1, 0, 0), "este año"}, true
	// The future (MOTOR-AGENDA-S1): an agenda is asked about what comes.
	case "tomorrow":
		d := day.AddDate(0, 0, 1)
		return Window{d, d.AddDate(0, 0, 1), "mañana (" + dateWords(d) + ")"}, true
	case "day_after_tomorrow":
		d := day.AddDate(0, 0, 2)
		return Window{d, d.AddDate(0, 0, 1), "pasado mañana (" + dateWords(d) + ")"}, true
	case "next_week":
		return Window{monday.AddDate(0, 0, 7), monday.AddDate(0, 0, 14), "la semana que viene"}, true
	}
	if wd, ok := weekdayTokens[strings.TrimPrefix(rng, "next_")]; ok && strings.HasPrefix(rng, "next_") {
		delta := (int(wd) - int(day.Weekday()) + 7) % 7
		if delta == 0 {
			delta = 7
		}
		d := day.AddDate(0, 0, delta)
		return Window{d, d.AddDate(0, 0, 1), "el " + weekdayES(wd) + " (" + dateWords(d) + ")"}, true
	}
	// The past occurrence, strictly before today — what «el martes» means
	// on a log of what happened (a note, the creation stamp).
	if wd, ok := weekdayTokens[strings.TrimPrefix(rng, "last_")]; ok && strings.HasPrefix(rng, "last_") {
		delta := (int(day.Weekday()) - int(wd) + 7) % 7
		if delta == 0 {
			delta = 7
		}
		d := day.AddDate(0, 0, -delta)
		return Window{d, d.AddDate(0, 0, 1), "el " + weekdayES(wd) + " pasado (" + dateWords(d) + ")"}, true
	}
	return resolveDateToken(rng, now)
}
