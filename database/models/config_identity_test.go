package models

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestConfigIdentityBackfillAndUniqueness(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "main.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&PingTask{}, &MaintenanceWindow{}); err != nil {
		t.Fatal(err)
	}
	// Raw inserts model rows written by an old binary before this migration.
	if err := db.Exec("INSERT INTO ping_tasks (name, target) VALUES (?, ?), (?, ?)", "one", "one.test", "two", "two.test").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Exec("INSERT INTO maintenance_windows (name, start, end) VALUES (?, ?, ?)", "window", now, now.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureConfigIdentities(db); err != nil {
		t.Fatal(err)
	}
	var tasks []PingTask
	var window MaintenanceWindow
	if err := db.Order("id").Find(&tasks).Error; err != nil || len(tasks) != 2 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	if err := db.First(&window).Error; err != nil {
		t.Fatal(err)
	}
	if !validConfigUID(tasks[0].ConfigUID) || !validConfigUID(tasks[1].ConfigUID) || tasks[0].ConfigUID == tasks[1].ConfigUID || !validConfigUID(window.ConfigUID) {
		t.Fatalf("invalid generated identities: %+v %+v", tasks, window)
	}
	before := tasks[0].ConfigUID
	if err := EnsureConfigIdentities(db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&tasks[0], tasks[0].Id).Error; err != nil || tasks[0].ConfigUID != before {
		t.Fatalf("identity changed on restart: %+v err=%v", tasks[0], err)
	}
	if err := db.Create(&PingTask{Name: "duplicate", Target: "duplicate.test", ConfigUID: before}).Error; err == nil {
		t.Fatal("duplicate task identity accepted")
	}
	if err := db.Create(&MaintenanceWindow{Name: "duplicate", Start: now, End: now.Add(time.Hour), ConfigUID: window.ConfigUID}).Error; err == nil {
		t.Fatal("duplicate window identity accepted")
	}
	if err := db.Create(&PingTask{Name: "invalid", Target: "invalid.test", ConfigUID: "invalid"}).Error; err == nil {
		t.Fatal("malformed task identity accepted")
	}
}

func TestConfigIdentityBackfillRejectsDuplicateWithoutPartialChanges(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "main.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&PingTask{}, &MaintenanceWindow{}); err != nil {
		t.Fatal(err)
	}
	uid, err := newConfigUID()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO ping_tasks (name, target, config_uid) VALUES (?, ?, ''), (?, ?, ?), (?, ?, ?)", "old", "a", "one", "b", uid, "two", "c", uid).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureConfigIdentities(db); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	var old PingTask
	if err := db.Where("name = ?", "old").Take(&old).Error; err != nil || old.ConfigUID != "" {
		t.Fatalf("failed backfill committed a partial identity: %+v err=%v", old, err)
	}
}

func TestConfigIdentityBackfillRejectsDeletedIdentityConflict(t *testing.T) {
	for _, mode := range []string{"live", "duplicate", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "main.db")), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })
			if err := db.AutoMigrate(&PingTask{}, &PingTaskDeletion{}, &MaintenanceWindow{}); err != nil {
				t.Fatal(err)
			}
			uid, err := newConfigUID()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "malformed" {
				uid = "bad-identity"
			}
			if err := db.Create(&PingTaskDeletion{TaskID: 12, ConfigUID: uid}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("INSERT INTO ping_tasks (name, target, config_uid) VALUES (?, ?, '')", "old", "old.test").Error; err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "live":
				if err := db.Create(&PingTask{Name: "reused", Target: "host", ConfigUID: uid}).Error; err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				if err := db.Create(&PingTaskDeletion{TaskID: 13, ConfigUID: uid}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := EnsureConfigIdentities(db); err == nil {
				t.Fatalf("%s identity conflict was accepted", mode)
			}
			var old PingTask
			if err := db.Where("name = ?", "old").Take(&old).Error; err != nil || old.ConfigUID != "" {
				t.Fatalf("failed migration partially committed: task=%+v err=%v", old, err)
			}
		})
	}
}
