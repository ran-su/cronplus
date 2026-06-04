# CronPlus Channel Commands Design

## Overview

Delivery channels that support two-way messaging (Telegram) can receive commands from users and reply with structured responses.

## Built-In App Commands

| Command | Description |
|---|---|
| `/status` | App health: task counts, next run, failures |
| `/list` | All tasks with status and next run |
| `/help` | Available commands |

## Built-In Task Commands

| Command | Description |
|---|---|
| `/run <task-slug>` | Trigger manual run, reply with result |
| `/last <task-slug>` | Most recent run result |
| `/enable <task-slug>` | Enable task schedule |
| `/disable <task-slug>` | Disable task schedule |

## Task Slug

Derived from script name: lowercased, spaces → hyphens, non-alphanumeric stripped.
Example: `Price Watch` → `price-watch`

## Telegram Implementation

- Uses `getUpdates` long polling (not webhooks)
- Poll interval: 2 seconds
- Reuses delivery profile bot token
- Clears the Telegram slash-command menu and uses contextual inline buttons only
- Only processes messages from authorized chat IDs
- If `authorizedChatIDs` is empty, the profile's configured `chat_id` is the only authorized chat
- Tracks `update_id` offset to avoid double-processing
- Rate limited: 10 commands/minute/chat

## Security

| Control | Description |
|---|---|
| Opt-in per profile | Inbound commands off by default |
| Authorized chat IDs | Only whitelisted chats processed |
| Command allowlist | Only built-in commands recognized |
| Rate limiting | 10 commands/min/chat |
| Audit log | Authorized, non-rate-limited inbound messages are logged to CommandRecord |

## Reply Format

```
CronPlus Status
──────────
Tasks: 5 enabled, 1 disabled
Next run: Price Watch in 12 min
Recent failures: 1
```
