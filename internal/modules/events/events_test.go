package events

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePerEventPropsLimit(t *testing.T) {
	ok := Input{Name: "spot_view", Props: map[string]any{"k": strings.Repeat("a", 100)}}
	if err := validate(ok); err != nil {
		t.Fatalf("small props rejected: %v", err)
	}
	big := Input{Name: "spot_view", Props: map[string]any{"k": strings.Repeat("a", MaxPropsBytes)}}
	if err := validate(big); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("oversize props accepted: %v", err)
	}
	if err := validate(Input{Name: "Bad Name"}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("bad name accepted: %v", err)
	}
}
