package plugin

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/utils/messageSender"
	"github.com/gin-gonic/gin"
)

// Run in a network namespace: this test loads a real third-party package and
// delivers only to a temporary local SMTP server.
func TestKomariMarketEmailSMTP(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	certServer := httptest.NewTLSServer(nil)
	cert := certServer.TLS.Certificates[0]
	certServer.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", ca)
	for _, tc := range []struct{ upgrade, implicit, login bool }{{}, {upgrade: true}, {upgrade: true, login: true}, {implicit: true}} {
		t.Run(fmt.Sprintf("starttls=%v/implicit=%v/login=%v", tc.upgrade, tc.implicit, tc.login), func(t *testing.T) {
			address := "127.0.0.1:0"
			if tc.implicit {
				address = "127.0.0.1:465"
			}
			listener, err := net.Listen("tcp", address)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			result := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					result <- err
					return
				}
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				if tc.implicit {
					secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
					if err := secure.Handshake(); err != nil {
						result <- err
						return
					}
					conn = secure
				}
				write := func(s string) error { _, e := fmt.Fprint(conn, s); return e }
				if err = write("220 local test\r\n"); err != nil {
					result <- err
					return
				}
				reader := bufio.NewReader(conn)
				inData := false
				seenAuth := false
				seenBody := false
				loginStep := 0
				for {
					line, e := reader.ReadString('\n')
					if e != nil {
						result <- e
						return
					}
					line = strings.TrimSpace(line)
					if inData {
						if line == "." {
							inData = false
							e = write("250 accepted\r\n")
						} else if strings.Contains(line, "aGVsbG8=") {
							seenBody = true
						}
					} else {
						switch {
						case loginStep == 1:
							loginStep = 2
							e = write("334 UGFzc3dvcmQ6\r\n")
						case loginStep == 2:
							loginStep = 0
							seenAuth = true
							e = write("235 authenticated\r\n")
						case line == "AUTH LOGIN":
							loginStep = 1
							e = write("334 VXNlcm5hbWU6\r\n")
						case strings.HasPrefix(line, "EHLO "):
							e = write("250-local\r\n250 STARTTLS\r\n")
						case line == "STARTTLS":
							if !tc.upgrade {
								result <- fmt.Errorf("unexpected STARTTLS")
								return
							}
							if e = write("220 upgrade\r\n"); e == nil {
								secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
								e = secure.Handshake()
								conn = secure
								reader = bufio.NewReader(conn)
							}
						case strings.HasPrefix(line, "AUTH PLAIN "):
							seenAuth = true
							e = write("235 authenticated\r\n")
						case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
							e = write("250 ok\r\n")
						case line == "DATA":
							inData = true
							e = write("354 send\r\n")
						case line == "QUIT":
							e = write("221 bye\r\n")
							if e == nil && (!seenAuth || !seenBody) {
								e = fmt.Errorf("missing auth or body")
							}
							result <- e
							return
						default:
							result <- fmt.Errorf("unexpected SMTP command: %q", line)
							return
						}
					}
					if e != nil {
						result <- e
						return
					}
				}
			}()
			archive := verifiedMarketPluginArchive(t, dir, "email-notification-plugin")
			withTempDataDir(t)
			Init(gin.New())
			if err = dbcore.GetDBInstance().AutoMigrate(&models.MessageSenderProvider{}); err != nil {
				t.Fatal(err)
			}
			oldVersion := utils.CurrentVersion
			utils.CurrentVersion = "1.7.30"
			t.Cleanup(func() { utils.CurrentVersion = oldVersion })
			if _, err = InstallZip(archive); err != nil {
				t.Fatal(err)
			}
			if err = SetEnabled("email-notification-plugin", true, true); err != nil {
				t.Fatal(err)
			}
			defer SetEnabled("email-notification-plugin", false, false)
			id, ok := messageSender.ChannelSelectionID("email")
			if !ok {
				t.Fatal("missing email channel")
			}
			cfg := map[string]any{"host": "127.0.0.1", "port": listener.Addr().(*net.TCPAddr).Port, "username": "test", "password": "test", "sender": "sender@example.test", "receiver": "receiver@example.test", "use_ssl": tc.upgrade || tc.implicit, "use_login_auth": tc.login}
			raw, _ := json.Marshal(cfg)
			if err = database.SaveMessageSenderConfig(&models.MessageSenderProvider{Name: id, Addition: string(raw)}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			found, err := messageSender.CallNotificationChannel(ctx, id, messageSender.Notification{Title: "test", Message: "hello"})
			if !found || err != nil {
				t.Fatalf("send: %v %v", found, err)
			}
			if err = <-result; err != nil {
				t.Fatal(err)
			}
		})
	}
}
