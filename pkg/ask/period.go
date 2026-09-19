package ask

import (
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
	}
	return Window{}, false
}
