package tasks

import (
	"context"
	"errors"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"gorm.io/gorm"
)

func CreateTask(taskId string, clients []string, command string) error {
	db := dbcore.GetDBInstance()
	// Create a new task in the database with clients as JSON array
	task := models.Task{
		TaskId:  taskId,
		Clients: models.StringArray(clients),
		Command: command,
	}
	var taskResults []models.TaskResult
	for _, client := range clients {
		taskResults = append(taskResults, models.TaskResult{
			TaskId:     taskId,
			Client:     client,
			Result:     "",
			ExitCode:   nil,
			FinishedAt: nil,
			CreatedAt:  time.Now().UTC(),
		})
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		ordered := tx.Exec(`UPDATE tasks SET history_order = max(coalesce((SELECT max(history_order) FROM tasks), 0), 0) + 1 WHERE task_id = ? AND coalesce((SELECT max(history_order) FROM tasks), 0) < 9223372036854775807`, taskId)
		if ordered.Error != nil {
			return ordered.Error
		}
		if ordered.RowsAffected != 1 {
			return errors.New("task history order exhausted")
		}
		if len(taskResults) > 0 {
			return tx.Create(&taskResults).Error
		}
		return nil
	})
}
func GetTaskByTaskId(taskId string) (*models.Task, error) {
	var task models.Task
	if err := dbcore.GetDBInstance().Where("task_id = ?", taskId).First(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}
func GetTasksByClientId(clientId string) ([]models.Task, error) {
	var tasks []models.Task
	if err := dbcore.GetDBInstance().Where("clients LIKE ?", "%"+clientId+"%").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func GetSpecificTaskResult(taskId, clientId string) (*models.TaskResult, error) {
	var result models.TaskResult
	if err := dbcore.GetDBInstance().Where("task_id = ? AND client = ?", taskId, clientId).First(&result).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

func GetAllTasks() ([]models.Task, error) {
	var tasks []models.Task
	if err := dbcore.GetDBInstance().Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func GetTaskResultsByTaskId(taskId string) ([]models.TaskResult, error) {
	var results []models.TaskResult
	if err := dbcore.GetDBInstance().Where("task_id = ?", taskId).Find(&results).Error; err != nil {
		return nil, err
	}
	return results, nil
}
func SaveTaskResult(taskId, clientId, result string, exitCode int, timestamp time.Time) error {
	return SaveTaskResultContext(context.Background(), taskId, clientId, result, exitCode, timestamp)
}

func SaveTaskResultContext(ctx context.Context, taskId, clientId, result string, exitCode int, timestamp time.Time) error {
	return dbcore.GetDBInstance().WithContext(ctx).
		Model(&models.TaskResult{}).
		Where("task_id = ? AND client = ?", taskId, clientId).
		Updates(map[string]interface{}{
			"result":      result,
			"exit_code":   exitCode,
			"finished_at": timestamp.UTC(),
		}).Error
}

func ClearTaskResultsByTimeBefore(before time.Time) error {
	return dbcore.GetDBInstance().Where("created_at < ?", before.UTC()).Delete(&models.TaskResult{}).Error
}
