package telegram

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramBoundedResponseAndExistingEnvelope(t *testing.T) {
	response := `{"ok":true}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.PostForm.Get("chat_id") != "synthetic" || r.PostForm.Get("parse_mode") != "HTML" {
			t.Error("Telegram form contract changed")
		}
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()
	sender := &TelegramSender{Addition: Addition{Endpoint: server.URL + "/", BotToken: "test-only", ChatID: "synthetic"}}
	if err := sender.SendTextMessage("test", "title"); err != nil {
		t.Fatal(err)
	}
	response = `{"ok":false,"description":"` + strings.Repeat("x", 65<<10) + `"}`
	if err := sender.SendTextMessage("test", "title"); err == nil {
		t.Fatal("oversized response must fail within the bounded reader")
	}
}
