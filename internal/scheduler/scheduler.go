package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"
	db "trano/internal/db/sqlc"
)

// Provide date in the proper IST timezone
func GenerateRunsForDate(ctx context.Context, queries *db.Queries, logger *log.Logger, date time.Time) {
	logger.Printf("generating runs for %s", date.Format(time.DateOnly))

	schedules, err := queries.ListActiveSchedules(ctx, int(date.Weekday()))
	if err != nil {
		logger.Printf("failed to fetch schedules for %s: %v", date.Format(time.DateOnly), err)
		return
	}

	targetDate := date.Format(time.DateOnly)

	for _, s := range schedules {
		runID := fmt.Sprintf("%d_%s", s.TrainNo, targetDate)

		err = queries.UpsertTrainRun(ctx, db.UpsertTrainRunParams{
			RunID:      runID,
			ScheduleID: s.ScheduleID,
			TrainNo:    s.TrainNo,
			RunDate:    targetDate,
		})
		if err != nil {
			logger.Printf("failed to upsert run for train %d on %s: %v", s.TrainNo, targetDate, err)
			continue
		}
	}
}
