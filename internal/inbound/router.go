package inbound

import (
	"fmt"
	"strings"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

// CommandContext provides access to app state for command execution.
type CommandContext struct {
	GetTasks      func() []*models.Task
	GetRunHistory func(taskID string) []models.RunRecord
	TriggerRun    func(taskID, trigger string) error
	SetEnabled    func(taskID string, enabled bool) error
	NextRunTime   func(task *models.Task) *time.Time
}

// Router routes inbound messages to command handlers.
type Router struct {
	ctx CommandContext
}

// NewRouter creates a command router with the given context.
func NewRouter(ctx CommandContext) *Router {
	return &Router{ctx: ctx}
}

// Route processes an inbound message and returns a reply.
func (r *Router) Route(msg models.InboundMessage) *models.OutboundReply {
	text := strings.TrimSpace(msg.RawText)
	if text == "" || text[0] != '/' {
		return nil
	}

	parts := strings.Fields(text)
	command := strings.ToLower(parts[0])
	args := parts[1:]

	switch command {
	case "/status":
		return r.handleStatus()
	case "/list":
		return r.handleList()
	case "/help":
		return r.handleHelp()
	case "/run":
		if len(args) == 0 {
			return reply("Usage: /run <task-slug>")
		}
		return r.handleRun(args[0])
	case "/last":
		if len(args) == 0 {
			return reply("Usage: /last <task-slug>")
		}
		return r.handleLast(args[0])
	case "/enable":
		if len(args) == 0 {
			return reply("Usage: /enable <task-slug>")
		}
		return r.handleSetEnabled(args[0], true)
	case "/disable":
		if len(args) == 0 {
			return reply("Usage: /disable <task-slug>")
		}
		return r.handleSetEnabled(args[0], false)
	default:
		return reply(fmt.Sprintf("Unknown command: %s\nType /help for available commands.", command))
	}
}

func (r *Router) handleStatus() *models.OutboundReply {
	tasks := r.ctx.GetTasks()
	enabled := 0
	disabled := 0
	recentFailures := 0
	var nextRunTask string
	var nextRunTime *time.Time

	for _, t := range tasks {
		if t.Enabled {
			enabled++
		} else {
			disabled++
		}

		runs := r.ctx.GetRunHistory(t.ID)
		if len(runs) > 0 && models.RunStatusFromOutcome(runs[0].Outcome) == "failure" {
			if time.Since(runs[0].FinishedAt) < 24*time.Hour {
				recentFailures++
			}
		}

		if t.Enabled {
			if nr := r.ctx.NextRunTime(t); nr != nil {
				if nextRunTime == nil || nr.Before(*nextRunTime) {
					nextRunTime = nr
					nextRunTask = t.DisplayName
				}
			}
		}
	}

	msg := "CronPlus Status\n──────────\n"
	msg += fmt.Sprintf("Tasks: %d enabled, %d disabled\n", enabled, disabled)

	if nextRunTime != nil {
		dur := time.Until(*nextRunTime)
		msg += fmt.Sprintf("Next run: %s in %s\n", nextRunTask, formatDuration(dur))
	}

	msg += fmt.Sprintf("Recent failures: %d", recentFailures)
	return reply(msg)
}

func (r *Router) handleList() *models.OutboundReply {
	tasks := r.ctx.GetTasks()
	if len(tasks) == 0 {
		return reply("No tasks configured.")
	}

	msg := "Tasks\n──────────\n"
	for _, t := range tasks {
		icon := "✅"
		if !t.Enabled {
			icon = "⏸"
		}

		schedule := ""
		if t.Manifest != nil {
			schedule = t.Manifest.Schedule.Expression
		}

		nextStr := ""
		if t.Enabled {
			if nr := r.ctx.NextRunTime(t); nr != nil {
				nextStr = fmt.Sprintf(" — next: %s", nr.Format("15:04"))
			}
		} else {
			nextStr = " — disabled"
		}

		msg += fmt.Sprintf("%s %s — %s%s\n", icon, t.Slug(), schedule, nextStr)
	}
	return reply(strings.TrimRight(msg, "\n"))
}

func (r *Router) handleHelp() *models.OutboundReply {
	msg := `CronPlus Commands
──────────
/status — App health summary
/list — All tasks
/help — This message
/run <task> — Run a task now
/last <task> — Last run result
/enable <task> — Enable a task
/disable <task> — Disable a task`
	return reply(msg)
}

func (r *Router) handleRun(slug string) *models.OutboundReply {
	task := r.findTaskBySlug(slug)
	if task == nil {
		return reply(fmt.Sprintf("Task '%s' not found.\nUse /list to see available tasks.", slug))
	}

	if err := r.ctx.TriggerRun(task.ID, "command"); err != nil {
		return reply(fmt.Sprintf("❌ Failed to run %s: %s", task.DisplayName, err.Error()))
	}

	return reply(fmt.Sprintf("✅ Started %s.\nUse /last %s for the latest result.", task.DisplayName, task.Slug()))
}

func (r *Router) handleLast(slug string) *models.OutboundReply {
	task := r.findTaskBySlug(slug)
	if task == nil {
		return reply(fmt.Sprintf("Task '%s' not found.", slug))
	}

	runs := r.ctx.GetRunHistory(task.ID)
	if len(runs) == 0 {
		return reply(fmt.Sprintf("No runs recorded for %s.", task.DisplayName))
	}

	last := runs[0]
	status := models.RunStatusFromOutcome(last.Outcome)
	icon := "✅"
	if status == "failure" {
		icon = "❌"
	}

	msg := fmt.Sprintf("Last run: %s\n", task.DisplayName)
	msg += fmt.Sprintf("%s Status: %s\n", icon, status)
	msg += fmt.Sprintf("Trigger: %s\n", last.Trigger)
	msg += fmt.Sprintf("Time: %s\n", last.FinishedAt.Format("Jan 2, 15:04"))
	msg += fmt.Sprintf("Duration: %.1fs", float64(last.Outcome.DurationMs)/1000)

	if last.Outcome.ParsedResult != nil && last.Outcome.ParsedResult.Summary != "" {
		msg += "\n" + last.Outcome.ParsedResult.Summary
	}

	return reply(msg)
}

func (r *Router) handleSetEnabled(slug string, enabled bool) *models.OutboundReply {
	task := r.findTaskBySlug(slug)
	if task == nil {
		return reply(fmt.Sprintf("Task '%s' not found.", slug))
	}

	if err := r.ctx.SetEnabled(task.ID, enabled); err != nil {
		return reply(fmt.Sprintf("❌ Failed: %s", err.Error()))
	}

	action := "enabled"
	icon := "✅"
	if !enabled {
		action = "disabled"
		icon = "⏸"
	}
	return reply(fmt.Sprintf("%s %s %s.", icon, task.DisplayName, action))
}

func (r *Router) findTaskBySlug(slug string) *models.Task {
	tasks := r.ctx.GetTasks()
	slug = strings.ToLower(slug)
	for _, t := range tasks {
		if t.Slug() == slug {
			return t
		}
	}
	return nil
}

func reply(text string) *models.OutboundReply {
	return &models.OutboundReply{Text: text, Format: "plain"}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}
