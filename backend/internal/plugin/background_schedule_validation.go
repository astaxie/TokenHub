package plugin

import (
	"fmt"
	"strings"
)

func validateBackgroundJobSchedule(schedule string) error {
	schedule = strings.TrimSpace(schedule)
	if schedule == "@startup" {
		return nil
	}
	if _, ok := backgroundJobScheduleInterval(schedule); !ok {
		return fmt.Errorf("unsupported background job schedule %q: use @startup, a positive duration, or */N * * * *", schedule)
	}
	return nil
}
