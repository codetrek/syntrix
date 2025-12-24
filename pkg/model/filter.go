package model

type Filters []Filter

// Filter represents a query filter
type Filter struct {
	Field string      `json:"field"`
	Op    string      `json:"op"`
	Value interface{} `json:"value"`
}
