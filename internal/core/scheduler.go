package core

import (
	"context"
	"log"
	"time"
)

// Scheduler evaluates cron schedules on a regular tick and triggers task execution.
type Scheduler struct {
	engine        *Engine
	ticker        *time.Ticker
	lastTriggered map[string]string // taskID → "2006-01-02T15:04" key to prevent double-fire
}

// NewScheduler creates a scheduler for the given engine.
func NewScheduler(engine *Engine) *Scheduler {
	return &Scheduler{
		engine:        engine,
		lastTriggered: make(map[string]string),
	}
}

// Start begins the scheduler loop. It blocks until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	s.ticker = time.NewTicker(30 * time.Second)
	defer s.ticker.Stop()

	log.Println("[CronPlus] Scheduler started (30s tick).")

	// Do an immediate tick on start
	s.tick(time.Now())

	for {
		select {
		case t := <-s.ticker.C:
			s.tick(t)
		case <-ctx.Done():
			log.Println("[CronPlus] Scheduler stopped.")
			return
		}
	}
}

func (s *Scheduler) tick(now time.Time) {
	tasks := s.engine.Tasks()

	for _, task := range tasks {
		if !task.Enabled || task.Manifest == nil {
			continue
		}

		expr, err := ParseCron(task.Manifest.Schedule.Expression)
		if err != nil {
			continue
		}

		tz := task.Manifest.Schedule.Timezone
		loc, err := time.LoadLocation(tz)
		if err != nil {
			loc = time.UTC
		}

		nowLocal := now.In(loc)
		minuteKey := nowLocal.Format("2006-01-02T15:04")

		// Skip if already triggered this minute
		if s.lastTriggered[task.ID] == minuteKey {
			continue
		}

		if expr.Matches(nowLocal) {
			s.lastTriggered[task.ID] = minuteKey

			if s.engine.IsRunning(task.ID) {
				log.Printf("[CronPlus] Skipping scheduled run for '%s' — already running.", task.DisplayName)
				continue
			}

			log.Printf("[CronPlus] Scheduled run: %s", task.DisplayName)
			taskID := task.ID
			go func() {
				if _, err := s.engine.RunTask(taskID, "schedule"); err != nil {
					log.Printf("[CronPlus] Scheduled run failed for '%s': %v", task.DisplayName, err)
				}
			}()
		}
	}
}
