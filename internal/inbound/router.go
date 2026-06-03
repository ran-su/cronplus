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
	command := normalizeCommand(parts[0])
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
			return r.reply("Usage: /run <task-slug>", r.listActionRows()...)
		}
		return r.handleRun(args[0])
	case "/last":
		if len(args) == 0 {
			return r.reply("Usage: /last <task-slug>", r.listActionRows()...)
		}
		return r.handleLast(args[0])
	case "/enable":
		if len(args) == 0 {
			return r.reply("Usage: /enable <task-slug>", r.listActionRows()...)
		}
		return r.handleSetEnabled(args[0], true)
	case "/disable":
		if len(args) == 0 {
			return r.reply("Usage: /disable <task-slug>", r.listActionRows()...)
		}
		return r.handleSetEnabled(args[0], false)
	default:
		return r.reply(fmt.Sprintf("Unknown command: %s\nType /help for available commands.", command), action("Help", "/help"))
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
	return r.reply(msg, r.generalActionRows()...)
}

func (r *Router) handleList() *models.OutboundReply {
	tasks := r.ctx.GetTasks()
	if len(tasks) == 0 {
		return r.reply("No tasks configured.", action("Help", "/help"))
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
	return r.reply(strings.TrimRight(msg, "\n"), r.listActionRows()...)
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
	return r.reply(msg, r.generalActionRows()...)
}

func (r *Router) handleRun(slug string) *models.OutboundReply {
	task := r.findTaskBySlug(slug)
	if task == nil {
		return r.reply(fmt.Sprintf("Task '%s' not found.\nUse /list to see available tasks.", slug), action("Show tasks", "/list"))
	}

	if err := r.ctx.TriggerRun(task.ID, "command"); err != nil {
		return r.reply(fmt.Sprintf("❌ Failed to run %s: %s", task.DisplayName, err.Error()), r.taskActionRows(task)...)
	}

	return r.reply(fmt.Sprintf("✅ Started %s.\nUse /last %s for the latest result.", task.DisplayName, task.Slug()), r.taskActionRows(task)...)
}

func (r *Router) handleLast(slug string) *models.OutboundReply {
	task := r.findTaskBySlug(slug)
	if task == nil {
		return r.reply(fmt.Sprintf("Task '%s' not found.", slug), action("Show tasks", "/list"))
	}

	runs := r.ctx.GetRunHistory(task.ID)
	if len(runs) == 0 {
		return r.reply(fmt.Sprintf("No runs recorded for %s.", task.DisplayName), r.taskActionRows(task)...)
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

	return r.reply(msg, r.taskActionRows(task)...)
}

func (r *Router) handleSetEnabled(slug string, enabled bool) *models.OutboundReply {
	task := r.findTaskBySlug(slug)
	if task == nil {
		return r.reply(fmt.Sprintf("Task '%s' not found.", slug), action("Show tasks", "/list"))
	}

	if err := r.ctx.SetEnabled(task.ID, enabled); err != nil {
		return r.reply(fmt.Sprintf("❌ Failed: %s", err.Error()), r.taskActionRows(task)...)
	}

	action := "enabled"
	icon := "✅"
	if !enabled {
		action = "disabled"
		icon = "⏸"
	}
	updatedTask := *task
	updatedTask.Enabled = enabled
	return r.reply(fmt.Sprintf("%s %s %s.", icon, task.DisplayName, action), r.taskActionRows(&updatedTask)...)
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

const maxInlineTaskRows = 6

func (r *Router) reply(text string, inlineActions ...[]models.ReplyAction) *models.OutboundReply {
	return &models.OutboundReply{
		Text:          text,
		Format:        "plain",
		InlineActions: compactActionRows(inlineActions),
	}
}

func action(label, command string) []models.ReplyAction {
	return []models.ReplyAction{{Label: label, Command: command}}
}

func (r *Router) generalActionRows() [][]models.ReplyAction {
	return [][]models.ReplyAction{
		{{Label: "Status", Command: "/status"}, {Label: "Tasks", Command: "/list"}},
		{{Label: "Help", Command: "/help"}},
	}
}

func (r *Router) taskActionRows(task *models.Task) [][]models.ReplyAction {
	if task == nil {
		return nil
	}
	slug := task.Slug()
	if slug == "" {
		return nil
	}
	rows := [][]models.ReplyAction{
		{{Label: "Run now", Command: "/run " + slug}, {Label: "Last result", Command: "/last " + slug}},
	}
	if task.Enabled {
		rows = append(rows, action("Disable", "/disable "+slug))
	} else {
		rows = append(rows, action("Enable", "/enable "+slug))
	}
	return rows
}

func (r *Router) listActionRows() [][]models.ReplyAction {
	rows := make([][]models.ReplyAction, 0)
	if r.ctx.GetTasks == nil {
		return rows
	}
	added := 0
	for _, task := range r.ctx.GetTasks() {
		if task == nil {
			continue
		}
		slug := task.Slug()
		if slug == "" {
			continue
		}
		rows = append(rows, []models.ReplyAction{
			{Label: "Run " + slug, Command: "/run " + slug},
			{Label: "Last", Command: "/last " + slug},
		})
		added++
		if added >= maxInlineTaskRows {
			break
		}
	}
	return rows
}

func compactActionRows(rows [][]models.ReplyAction) [][]models.ReplyAction {
	compact := make([][]models.ReplyAction, 0, len(rows))
	for _, row := range rows {
		next := make([]models.ReplyAction, 0, len(row))
		for _, item := range row {
			if strings.TrimSpace(item.Label) == "" || strings.TrimSpace(item.Command) == "" {
				continue
			}
			next = append(next, item)
		}
		if len(next) > 0 {
			compact = append(compact, next)
		}
	}
	return compact
}

func normalizeCommand(token string) string {
	token = strings.ToLower(strings.TrimSpace(token))
	if at := strings.IndexByte(token, '@'); at > 0 {
		token = token[:at]
	}
	return token
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
