package model

//CalendarRequest - expected body of calendar queue
type CalendarRequest struct {
	Path   string            `json:"path"`
	Method string            `json:"method"`
	Body   string            `json:"body"`
	Params map[string]string `json:"params"`
}
