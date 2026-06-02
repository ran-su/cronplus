package core

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

// Scheduler evaluates cron schedules on a regular tick and triggers task execution.
type Scheduler struct {
	engine        *Engine
	ticker        *time.Ticker
	mu            sync.Mutex
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

	// Treat the already-started current minute as seen. CronPlus schedules
	// future matching minutes and does not backfill the current partial minute.
	s.primeTasks(time.Now())

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

// PrimeTask prevents a newly visible task from firing in the current partial minute.
func (s *Scheduler) PrimeTask(task *models.Task, now time.Time) {
	minuteKey, ok := taskMinuteKeyIfDue(task, now)
	if !ok {
		return
	}
	s.mu.Lock()
	s.lastTriggered[task.ID] = minuteKey
	s.mu.Unlock()
}

func (s *Scheduler) primeTasks(now time.Time) {
	for _, task := range s.engine.Tasks() {
		s.PrimeTask(task, now)
	}
}

func (s *Scheduler) tick(now time.Time) {
	tasks := s.engine.Tasks()

	for _, task := range tasks {
		minuteKey, ok := taskMinuteKeyIfDue(task, now)
		if !ok {
			continue
		}

		// Skip if already triggered this minute
		s.mu.Lock()
		if s.lastTriggered[task.ID] == minuteKey {
			s.mu.Unlock()
			continue
		}
		s.lastTriggered[task.ID] = minuteKey
		s.mu.Unlock()

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

func taskMinuteKeyIfDue(task *models.Task, now time.Time) (string, bool) {
	if !task.Enabled || task.Manifest == nil {
		return "", false
	}

	expr, err := ParseCron(task.Manifest.Schedule.Expression)
	if err != nil {
		return "", false
	}

	tz := task.Manifest.Schedule.Timezone
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}

	nowLocal := now.In(loc)
	if !expr.Matches(nowLocal) {
		return "", false
	}
	return nowLocal.Format("2006-01-02T15:04"), true
}
