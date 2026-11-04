package kanban

import (
	"slices"
	"testing"
)

func TestMoveTo(t *testing.T) {
	tests := []struct {
		name  string
		ids   []string
		id    string
		index int
		want  []string
	}{
		{"forward within list", []string{"a", "b", "c"}, "a", 2, []string{"b", "c", "a"}},
		{"backward within list", []string{"a", "b", "c"}, "c", 0, []string{"c", "a", "b"}},
		{"same place", []string{"a", "b"}, "b", 1, []string{"a", "b"}},
		{"new item in the middle", []string{"a", "b"}, "x", 1, []string{"a", "x", "b"}},
		{"into empty list", nil, "x", 0, []string{"x"}},
		{"index past the end", []string{"a", "b"}, "x", 99, []string{"a", "b", "x"}},
		{"negative index", []string{"a", "b"}, "x", -1, []string{"x", "a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := moveTo(tt.ids, tt.id, tt.index); !slices.Equal(got, tt.want) {
				t.Errorf("moveTo() = %v, want %v", got, tt.want)
			}
		})
	}
}
