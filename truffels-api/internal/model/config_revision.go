package model

import "time"

type ConfigRevision struct {
	ID               int64     `json:"id"`
	ServiceID        string    `json:"service_id"`
	Timestamp        time.Time `json:"timestamp"`
	Actor            string    `json:"actor"`
	Diff             string    `json:"diff"`
	ConfigSnapshot   string    `json:"config_snapshot"`
	ValidationResult string    `json:"validation_result"`
}
