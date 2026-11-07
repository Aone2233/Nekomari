package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/pingguard"
)

func TestPinnedTaskGenerationIsInternalAndUnique(t *testing.T) {
	seen := make(map[string]bool)
	for range 128 {
		generation, err := pingguard.NewGeneration()
		if err != nil || !validPinnedWSID(generation) || seen[generation] {
			t.Fatalf("invalid or repeated incarnation: %q, %v", generation, err)
		}
		seen[generation] = true
		task := models.PingTask{PinnedGeneration: generation}
		if pinnedTaskGeneration(task) != generation {
			t.Fatal("persisted incarnation changed")
		}
		data, err := json.Marshal(task)
		if err != nil || strings.Contains(string(data), generation) {
			t.Fatalf("internal incarnation exposed in JSON: %s, %v", data, err)
		}
	}
}
