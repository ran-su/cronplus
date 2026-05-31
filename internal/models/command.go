package models

import "time"

// CommandRecord logs an inbound command received from a channel.
type CommandRecord struct {
	ID             string    `json:"id"`
	ChannelType    string    `json:"channelType"`
	ChatID         string    `json:"chatID"`
	CommandText    string    `json:"commandText"`
	MatchedCommand string    `json:"matchedCommand,omitempty"`
	ReplyText      string    `json:"replyText,omitempty"`
	ReceivedAt     time.Time `json:"receivedAt"`
	Error          string    `json:"error,omitempty"`
}

// InboundMessage is a message received from a channel.
type InboundMessage struct {
	ChannelType string
	SenderID    string
	ChatID      string
	RawText     string
	ReceivedAt  time.Time
}

// OutboundReply is a reply to send back on the channel.
type OutboundReply struct {
	Text   string
	Format string // "plain" or "markdown"
}
