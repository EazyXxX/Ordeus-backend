package kanban

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"ordeus/backend/internal/auth"
	"ordeus/backend/internal/httpjson"
)

// maxReportTasks keeps a report readable and its row small.
const maxReportTasks = 200

// ReportStats is a week of finished tasks. Counts by day run Monday to Sunday.
type ReportStats struct {
	Completed         int          `json:"completed"`
	PreviousCompleted int          `json:"previousCompleted"`
	Created           int          `json:"created"`
	OnTime            int          `json:"onTime"`
	Late              int          `json:"late"`
	OverdueOpen       int          `json:"overdueOpen"`
	AverageHours      *float64     `json:"averageHours"`
	ByDay             []int        `json:"byDay"`
	ByCategory        []NamedCount `json:"byCategory"`
	ByPriority        []NamedCount `json:"byPriority"`
	Tasks             []ReportTask `json:"tasks"`
}

type NamedCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type ReportTask struct {
	Title       string    `json:"title"`
	Category    string    `json:"category"`
	Priority    string    `json:"priority"`
	CompletedAt time.Time `json:"completedAt"`
	Late        bool      `json:"late"`
}

type Report struct {
	Week      string      `json:"week"`
	Stats     ReportStats `json:"stats"`
	CreatedAt time.Time   `json:"createdAt"`
}

// finishedTask is a card that reached the done column within the week.
type finishedTask struct {
	title, category, priority string
	completedAt, createdAt    time.Time
	dueDate                   *time.Time
}

func (h *handler) listReports(w http.ResponseWriter, r *http.Request) {
	reports, err := h.store.reports(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusOK, reports)
}

func (h *handler) createReport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Week string `json:"week"`
		// TimeZone is the author's, so that a week runs from their own Monday.
		TimeZone string `json:"timeZone"`
	}
	if !httpjson.Bind(w, r, &input) {
		return
	}
	location, err := time.LoadLocation(input.TimeZone)
	if input.TimeZone == "" || err != nil {
		httpjson.Error(w, http.StatusBadRequest, "unknown time zone")
		return
	}
	week, err := time.ParseInLocation(time.DateOnly, input.Week, location)
	if err != nil || week.Weekday() != time.Monday {
		httpjson.Error(w, http.StatusBadRequest, "week must be a monday")
		return
	}
	if week.After(time.Now()) {
		httpjson.Error(w, http.StatusBadRequest, "week has not started")
		return
	}

	report, err := h.store.createReport(r.Context(), auth.UserID(r.Context()), week)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusOK, report)
}

func (h *handler) deleteReport(w http.ResponseWriter, r *http.Request) {
	week := r.PathValue("week")
	if _, err := time.Parse(time.DateOnly, week); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid week")
		return
	}
	tag, err := h.store.pool.Exec(r.Context(),
		`DELETE FROM task_reports WHERE user_id = $1 AND week_start = $2::date`, auth.UserID(r.Context()), week)
	finish(w, r, requireRow(tag.RowsAffected(), err))
}

func (s *store) reports(ctx context.Context, userID string) ([]Report, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT to_char(week_start, 'YYYY-MM-DD'), stats, created_at
		FROM task_reports WHERE user_id = $1 ORDER BY week_start DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Report, error) {
		var report Report
		var stats []byte
		if err := row.Scan(&report.Week, &stats, &report.CreatedAt); err != nil {
			return Report{}, err
		}
		return report, json.Unmarshal(stats, &report.Stats)
	})
}

// createReport counts the week starting at week, which is midnight of a Monday
// in the author's time zone, and replaces any earlier report of it. A card is
// done when it sits in the done column: a card keeps its time after the author
// picks another done column, so that switching back loses nothing.
func (s *store) createReport(ctx context.Context, userID string, week time.Time) (Report, error) {
	end := week.AddDate(0, 0, 7)
	report := Report{Week: week.Format(time.DateOnly)}

	var counts struct{ previous, created, overdueOpen int }
	err := s.pool.QueryRow(ctx, `
		WITH cards AS (
			SELECT created_at, due_date,
				CASE WHEN column_id = kanban_done_column($1) THEN completed_at END AS done_at
			FROM kanban_cards WHERE user_id = $1
		)
		SELECT
			count(*) FILTER (WHERE done_at >= $4 AND done_at < $2),
			count(*) FILTER (WHERE created_at >= $2 AND created_at < $3),
			count(*) FILTER (WHERE due_date < $5::date AND created_at < $3 AND (done_at IS NULL OR done_at >= $3))
		FROM cards`,
		userID, week, end, week.AddDate(0, 0, -7), end.Format(time.DateOnly),
	).Scan(&counts.previous, &counts.created, &counts.overdueOpen)
	if err != nil {
		return Report{}, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT title, category, priority, completed_at, created_at, due_date
		FROM kanban_cards
		WHERE user_id = $1 AND column_id = kanban_done_column($1) AND completed_at >= $2 AND completed_at < $3
		ORDER BY completed_at, id`, userID, week, end)
	if err != nil {
		return Report{}, err
	}
	tasks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (finishedTask, error) {
		var task finishedTask
		err := row.Scan(&task.title, &task.category, &task.priority, &task.completedAt, &task.createdAt, &task.dueDate)
		return task, err
	})
	if err != nil {
		return Report{}, err
	}

	report.Stats = summarizeWeek(tasks, week.Location())
	report.Stats.PreviousCompleted = counts.previous
	report.Stats.Created = counts.created
	report.Stats.OverdueOpen = counts.overdueOpen

	stats, err := json.Marshal(report.Stats)
	if err != nil {
		return Report{}, err
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO task_reports (user_id, week_start, stats) VALUES ($1, $2::date, $3)
		ON CONFLICT (user_id, week_start) DO UPDATE SET stats = EXCLUDED.stats, created_at = now()
		RETURNING created_at`, userID, report.Week, stats).Scan(&report.CreatedAt)
	return report, err
}

// summarizeWeek counts the finished tasks. A task is late when it was finished
// on a later day than its due date, as days go where the author lives.
func summarizeWeek(tasks []finishedTask, location *time.Location) ReportStats {
	stats := ReportStats{
		Completed:  len(tasks),
		ByDay:      make([]int, 7),
		ByCategory: []NamedCount{},
		ByPriority: []NamedCount{},
		Tasks:      []ReportTask{},
	}
	categories := map[string]int{}
	byPriority := map[string]int{}
	var hours float64

	for _, task := range tasks {
		local := task.completedAt.In(location)
		stats.ByDay[(int(local.Weekday())+6)%7]++
		categories[task.category]++
		byPriority[task.priority]++
		hours += task.completedAt.Sub(task.createdAt).Hours()

		late := false
		if task.dueDate != nil {
			late = local.Format(time.DateOnly) > task.dueDate.Format(time.DateOnly)
			if late {
				stats.Late++
			} else {
				stats.OnTime++
			}
		}
		if len(stats.Tasks) < maxReportTasks {
			stats.Tasks = append(stats.Tasks, ReportTask{
				Title: task.title, Category: task.category, Priority: task.priority,
				CompletedAt: task.completedAt, Late: late,
			})
		}
	}

	if len(tasks) > 0 {
		average := hours / float64(len(tasks))
		stats.AverageHours = &average
	}
	for name, count := range categories {
		stats.ByCategory = append(stats.ByCategory, NamedCount{Name: name, Count: count})
	}
	// The most frequent first. Tasks without a category go last.
	sort.Slice(stats.ByCategory, func(i, j int) bool {
		a, b := stats.ByCategory[i], stats.ByCategory[j]
		if (a.Name == "") != (b.Name == "") {
			return b.Name == ""
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Name < b.Name
	})
	// From the most urgent, the way priorities are listed.
	for i := len(priorities) - 1; i >= 0; i-- {
		if count := byPriority[priorities[i]]; count > 0 {
			stats.ByPriority = append(stats.ByPriority, NamedCount{Name: priorities[i], Count: count})
		}
	}
	return stats
}
