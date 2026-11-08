package kanban

import (
	"testing"
	"time"
)

func TestSummarizeWeekCountsInTheAuthorsTimeZone(t *testing.T) {
	moscow := time.FixedZone("MSK", 3*60*60)
	monday := time.Date(2026, 11, 2, 0, 0, 0, 0, moscow)
	due := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	tasks := []finishedTask{
		// 22:30 UTC on Monday is already Tuesday in Moscow, still on time.
		{title: "a", category: "Дом", priority: "high", createdAt: monday, dueDate: &due,
			completedAt: time.Date(2026, 11, 3, 22, 30, 0, 0, time.UTC).Add(-24 * time.Hour)},
		{title: "b", category: "Дом", priority: "none", createdAt: monday, dueDate: &due,
			completedAt: monday.AddDate(0, 0, 3)},
		{title: "c", category: "", priority: "urgent", createdAt: monday,
			completedAt: monday.AddDate(0, 0, 6).Add(12 * time.Hour)},
	}

	stats := summarizeWeek(tasks, moscow)

	if stats.Completed != 3 || stats.OnTime != 1 || stats.Late != 1 {
		t.Errorf("completed %d, on time %d, late %d", stats.Completed, stats.OnTime, stats.Late)
	}
	if want := []int{0, 1, 0, 1, 0, 0, 1}; !equalInts(stats.ByDay, want) {
		t.Errorf("by day = %v, want %v", stats.ByDay, want)
	}
	if stats.ByCategory[0] != (NamedCount{"Дом", 2}) || stats.ByCategory[1] != (NamedCount{"", 1}) {
		t.Errorf("by category = %v", stats.ByCategory)
	}
	if stats.ByPriority[0].Name != "urgent" || stats.ByPriority[2].Name != "none" {
		t.Errorf("by priority = %v", stats.ByPriority)
	}
	if !stats.Tasks[1].Late || stats.Tasks[0].Late {
		t.Errorf("late flags = %v, %v", stats.Tasks[0].Late, stats.Tasks[1].Late)
	}
}

func TestSummarizeWeekWithoutTasks(t *testing.T) {
	stats := summarizeWeek(nil, time.UTC)
	if stats.AverageHours != nil || len(stats.ByDay) != 7 || stats.Tasks == nil || stats.ByCategory == nil {
		t.Errorf("stats = %+v", stats)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
