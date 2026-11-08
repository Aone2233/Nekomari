package agent

import "github.com/Aone2233/nekomari/database/models"

// Empty or malformed incarnations fail validation when registering dispatches.
// Every relevant mutation must persist a new value with the change itself.
func pinnedTaskGeneration(task models.PingTask) string {
	return task.PinnedGeneration
}
