package cloudbilling

import (
	"testing"
	"time"
)

func TestMonthlyPriceValidation(t *testing.T) {
	for _, price := range []string{"-1", "NaN", "1e2", "0", "0.0000001", "1000001", "", "01", "1.2.3"} {
		if validatePlan(Plan{Enabled: true, MonthlyPrice: price}) == nil {
			t.Errorf("accepted %q", price)
		}
	}
	for _, price := range []string{"0.000001", "1", "19.99", "1000000"} {
		if err := validatePlan(Plan{Enabled: true, MonthlyPrice: price}); err != nil {
			t.Errorf("rejected %q: %v", price, err)
		}
	}
}
func TestCalendarMonthClampsMonthEnd(t *testing.T) {
	for _, pair := range [][2]string{{"2026-01-31T12:00:00Z", "2026-02-28T12:00:00Z"}, {"2028-01-31T12:00:00Z", "2028-02-29T12:00:00Z"}, {"2026-12-15T09:30:00Z", "2027-01-15T09:30:00Z"}} {
		at, _ := time.Parse(time.RFC3339, pair[0])
		if got := nextMonth(at).Format(time.RFC3339); got != pair[1] {
			t.Errorf("%s => %s", pair[0], got)
		}
	}
}
