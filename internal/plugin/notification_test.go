package plugin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/utils/messageSender"
	"github.com/gin-gonic/gin"
)

func TestNotificationChannelLifecycleAndAsyncDelivery(t *testing.T) {
	withTempDataDir(t)
	Init(gin.New())
	if err := dbcore.GetDBInstance().AutoMigrate(&models.MessageSenderProvider{}); err != nil {
		t.Fatal(err)
	}
	path := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Channel","short":"test-channel","permissions":{"node":true,"timeout":1}}`,
		"script.js":          `const server=require("server"); function load(){ server.registerNotificationChannel("test-channel-id",{type:"managed",data:[{key:"address",type:"string",default:"default-address"}]},async (notice,cfg)=> { await new Promise(r=>setTimeout(r,10)); if(notice.title!=="hello" || notice.event.message!=="body" || cfg.address!=="saved-address") throw new Error("unexpected payload"); }); }`,
	})
	if _, err := InstallZip(path); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("test-channel", true, true); err != nil {
		t.Fatal(err)
	}
	id, ok := messageSender.ChannelSelectionID("test-channel-id")
	if !ok || id != "plugin:test-channel:test-channel-id" {
		t.Fatalf("selection %q %v", id, ok)
	}
	_, values, ok, err := messageSender.GetNotificationChannelConfiguration("test-channel-id")
	if err != nil || !ok || values["address"] != "default-address" {
		t.Fatalf("defaults: %v %v %v", values, ok, err)
	}
	if err := database.SaveMessageSenderConfig(&models.MessageSenderProvider{Name: id, Addition: `{"address":"saved-address"}`}); err != nil {
		t.Fatal(err)
	}
	found, err := messageSender.CallNotificationChannel(context.Background(), id, messageSender.Notification{Title: "hello", Event: models.EventMessage{Message: "body"}})
	if !found || err != nil {
		t.Fatalf("send: %v %v", found, err)
	}
	if err := SetEnabled("test-channel", false, false); err != nil {
		t.Fatal(err)
	}
	if messageSender.NotificationChannelRegistered("test-channel-id") {
		t.Fatal("channel survived unload")
	}
	if found, _ := messageSender.CallNotificationChannel(context.Background(), id, messageSender.Notification{}); found {
		t.Fatal("unloaded channel callable")
	}
	if err := SetEnabled("test-channel", true, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	found, err = messageSender.CallNotificationChannel(ctx, id, messageSender.Notification{Title: "hello", Event: models.EventMessage{Message: "body"}})
	if !found || err != nil {
		t.Fatalf("reload send: %v %v", found, err)
	}
}

func TestNotificationUnloadDuringPendingSend(t *testing.T) {
	withTempDataDir(t)
	Init(gin.New())
	if err := dbcore.GetDBInstance().AutoMigrate(&models.MessageSenderProvider{}); err != nil {
		t.Fatal(err)
	}
	path := writePluginZip(t, map[string]string{"komari-plugin.json": `{"name":"Pending","short":"pending-channel","permissions":{"timeout":2}}`, "script.js": `const server=require('server'); function load(){server.registerNotificationChannel('pending',{type:'managed',data:[]},()=>new Promise(()=>{}));}`})
	if _, err := InstallZip(path); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("pending-channel", true, true); err != nil {
		t.Fatal(err)
	}
	id, _ := messageSender.ChannelSelectionID("pending")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := messageSender.CallNotificationChannel(ctx, id, messageSender.Notification{})
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	unloaded := make(chan error, 1)
	go func() { unloaded <- SetEnabled("pending-channel", false, false) }()
	select {
	case err := <-unloaded:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("unload blocked by pending send")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("pending send succeeded after unload")
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("send did not respect cancellation")
	}
}

func TestNotificationErrorDoesNotExposeConfiguration(t *testing.T) {
	withTempDataDir(t)
	Init(gin.New())
	if err := dbcore.GetDBInstance().AutoMigrate(&models.MessageSenderProvider{}); err != nil {
		t.Fatal(err)
	}
	path := writePluginZip(t, map[string]string{"komari-plugin.json": `{"name":"Fail","short":"failing-channel","permissions":{"timeout":1}}`, "script.js": `const server=require('server'); function load(){server.registerNotificationChannel('fail',{type:'managed',data:[]},()=>{throw new Error('test-sensitive-value')})}`})
	if _, err := InstallZip(path); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("failing-channel", true, true); err != nil {
		t.Fatal(err)
	}
	id, _ := messageSender.ChannelSelectionID("fail")
	found, err := messageSender.CallNotificationChannel(context.Background(), id, messageSender.Notification{})
	if !found || err == nil || strings.Contains(err.Error(), "test-sensitive-value") {
		t.Fatalf("unsafe failure: %v %v", found, err)
	}
}
