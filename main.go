package main

import (
	"log/slog"

	"github.com/Aone2233/nekomari/cmd"
	"github.com/Aone2233/nekomari/utils"
	logger "github.com/Aone2233/nekomari/utils/log"
)

func main() {
	if utils.VersionHash == "unknown" {
		logger.Setup(slog.LevelDebug)
	} else {
		logger.Setup(slog.LevelInfo)
	}

	logger.Infof("server", "Nekomari Monitor %s (hash: %s)", utils.CurrentVersion, utils.VersionHash)

	cmd.Execute()
}
