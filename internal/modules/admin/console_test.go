package admin

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestNextPackStatuses(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		imported bool
		want     []string
	}{
		{name: "draft without import can only retire", status: "draft", want: []string{"retired"}},
		{name: "draft with import", status: "draft", imported: true, want: []string{"beta", "retired"}},
		{name: "beta", status: "beta", imported: true, want: []string{"live", "draft", "retired"}},
		{name: "live", status: "live", imported: true, want: []string{"beta", "retired"}},
		{name: "retired is terminal", status: "retired", imported: true, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextPackStatuses(tt.status, tt.imported); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("nextPackStatuses(%q, %t) = %v, want %v", tt.status, tt.imported, got, tt.want)
			}
		})
	}
}

func TestNormalizeSpotSearch(t *testing.T) {
	id := uuid.MustParse("3f6569db-3d46-41c5-a1dd-11335502dcce")

	t.Run("uuid becomes an id filter", func(t *testing.T) {
		f, err := normalizeSpotSearch(SpotSearch{Query: " " + id.String() + " "})
		if err != nil || f.ID == nil || *f.ID != id || f.NamePattern != nil {
			t.Fatalf("got %+v, err %v", f, err)
		}
	})

	t.Run("name is escaped and wrapped", func(t *testing.T) {
		f, err := normalizeSpotSearch(SpotSearch{Query: `100%_park\`})
		if err != nil || f.NamePattern == nil {
			t.Fatalf("got %+v, err %v", f, err)
		}
		if want := `%100\%\_park\\%`; *f.NamePattern != want {
			t.Fatalf("pattern = %q, want %q", *f.NamePattern, want)
		}
	})

	t.Run("limit is clamped", func(t *testing.T) {
		f, err := normalizeSpotSearch(SpotSearch{Status: "hidden", Limit: 5000})
		if err != nil || f.Limit != spotSearchDefault {
			t.Fatalf("limit = %d, err %v", f.Limit, err)
		}
	})

	for name, in := range map[string]SpotSearch{
		"no filter at all": {},
		"one character":    {Query: "a"},
		"unknown status":   {Status: "deleted"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeSpotSearch(in); !errors.Is(err, ErrInvalidSpotQuery) {
				t.Fatalf("err = %v, want ErrInvalidSpotQuery", err)
			}
		})
	}
}

func TestSpotActionsStayInsideStateMachine(t *testing.T) {

	allowed := map[string][]string{
		"pending": {"active", "hidden"},
		"active":  {"closed", "hidden", "merged"},
		"closed":  {"active", "hidden", "merged"},
		"hidden":  {"active", "merged"},
	}
	for action, a := range spotActions {
		for _, from := range a.from {
			ok := false
			for _, to := range allowed[from] {
				ok = ok || to == a.to
			}
			if !ok {
				t.Errorf("action %q: %s -> %s is not a legal spot transition", action, from, a.to)
			}
		}
	}
}
