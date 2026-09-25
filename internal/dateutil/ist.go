package dateutil

import "time"

const dateLayout = "2006-01-02"

var istLocation = time.FixedZone("IST", 5*60*60+30*60)

// ISTDayRange converts inclusive YYYY-MM-DD business dates into the UTC
// instants that bound those calendar days in India.
func ISTDayRange(startDate, endDate string) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation(dateLayout, startDate, istLocation)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := time.ParseInLocation(dateLayout, endDate, istLocation)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start.UTC(), end.AddDate(0, 0, 1).Add(-time.Nanosecond).UTC(), nil
}
