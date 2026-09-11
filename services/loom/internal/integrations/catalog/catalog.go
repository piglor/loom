// Package catalog defines the presentation contract that integration plugins
// contribute to the operator console. It contains no provider policy.
package catalog

type PluginState string

const (
	NeedsConfiguration PluginState = "needs_configuration"
	ReadyToConnect     PluginState = "ready_to_connect"
	Connected          PluginState = "connected"
	NeedsAttention     PluginState = "needs_attention"
)

type CheckStatus string

const (
	CheckMissing CheckStatus = "missing"
	CheckReady   CheckStatus = "ready"
)

type Step struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type Check struct {
	ID       string      `json:"id"`
	Label    string      `json:"label"`
	Status   CheckStatus `json:"status"`
	Detail   string      `json:"detail"`
	Required bool        `json:"required"`
}

type Action struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Endpoint struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type Plugin struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Category        string      `json:"category"`
	Description     string      `json:"description"`
	State           PluginState `json:"state"`
	SetupTitle      string      `json:"setup_title"`
	SetupSummary    string      `json:"setup_summary"`
	EstimatedTime   string      `json:"estimated_time"`
	Steps           []Step      `json:"steps"`
	Checks          []Check     `json:"checks"`
	Action          *Action     `json:"action,omitempty"`
	Endpoints       []Endpoint  `json:"endpoints,omitempty"`
	Notice          string      `json:"notice"`
	ConnectionCount int         `json:"connection_count"`
	SecretBackend   string      `json:"secret_backend"`
}
