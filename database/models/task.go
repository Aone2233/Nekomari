package models

import "time"

type Task struct {
	TaskId       string       `json:"task_id" gorm:"type:varchar(36);primaryKey;unique;index:idx_tasks_history_order,priority:2"`
	Clients      StringArray  `json:"clients" gorm:"type:longtext"`
	Command      string       `json:"command" gorm:"type:text"`
	HistoryOrder *int64       `json:"-" gorm:"index:idx_tasks_history_order,priority:1"`
	Results      []TaskResult `gorm:"foreignKey:TaskId;references:TaskId;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}

type TaskResult struct {
	HistoryIdentity string     `json:"-" gorm:"type:text;default:null;uniqueIndex:idx_task_results_history_identity"`
	TaskId          string     `json:"task_id" gorm:"type:varchar(36);index;index:idx_task_results_history,priority:1"`
	Client          string     `json:"client" gorm:"type:varchar(36);index:idx_task_results_history,priority:2"`
	ClientInfo      Client     `json:"client_info" gorm:"foreignKey:Client;references:UUID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	Result          string     `json:"result" gorm:"type:longtext"`
	ExitCode        *int       `json:"exit_code" gorm:"type:int"`
	FinishedAt      *time.Time `json:"finished_at" gorm:"type:timestamp"`
	CreatedAt       time.Time  `json:"created_at" gorm:"type:timestamp"`
}
