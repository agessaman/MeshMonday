package checkins

import "time"

// CountMeshMondayWeeksInclusive returns how many MeshMonday week buckets fall between
// the Monday week-start of trackFrom and the Monday week-start of through, inclusive,
// using tz for calendar boundaries (same as WeekStartMonday).
func CountMeshMondayWeeksInclusive(trackFrom, through time.Time, tz string) int {
	from := WeekStartMonday(trackFrom, tz)
	to := WeekStartMonday(through, tz)
	if to.Before(from) {
		return 0
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	fromDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	toDay := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc)
	days := int(toDay.Sub(fromDay).Hours() / 24)
	return days/7 + 1
}
