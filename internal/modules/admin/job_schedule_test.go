package admin

import (
	"errors"
	"testing"
)

func TestValidateJobInterval(t *testing.T) {
	tests := []struct {
		name    string
		seconds int64
		limits  jobScheduleLimit
		wantErr bool
	}{
		{name: "minimum", seconds: 900, limits: jobScheduleLimits["weather.refresh"]},
		{name: "maximum", seconds: 3600, limits: jobScheduleLimits["weather.refresh"]},
		{name: "below minimum", seconds: 899, limits: jobScheduleLimits["weather.refresh"], wantErr: true},
		{name: "above maximum", seconds: 3601, limits: jobScheduleLimits["weather.refresh"], wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJobInterval(tt.seconds, tt.limits)
			if errors.Is(err, ErrInvalidJobSchedule) != tt.wantErr {
				t.Fatalf("validateJobInterval() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}
