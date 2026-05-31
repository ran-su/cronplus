package models

// DeliveryProfile is a configured delivery destination.
type DeliveryProfile struct {
	ID                     string            `json:"id"`
	Name                   string            `json:"name"`
	DriverType             string            `json:"driverType"`
	Enabled                bool              `json:"enabled"`
	Config                 map[string]string `json:"config"`
	InboundCommandsEnabled bool              `json:"inboundCommandsEnabled"`
	AuthorizedChatIDs      []string          `json:"authorizedChatIDs,omitempty"`
}

// DeliveryRequest is a request to send a message through a delivery profile.
type DeliveryRequest struct {
	Profile         DeliveryProfile
	TaskName        string
	RunRecord       RunRecord
	MessageTemplate string
}
