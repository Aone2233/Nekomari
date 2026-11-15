package jsruntime

import (
	"bufio"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNodeTLSDirectAndStartTLS(t *testing.T) {
	certServer := httptest.NewTLSServer(nil)
	defer certServer.Close()
	cert := certServer.TLS.Certificates[0]
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}))
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgrade=%v", upgrade), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverResult := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					serverResult <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				if upgrade {
					if _, err = io.WriteString(conn, "READY\n"); err != nil {
						serverResult <- err
						return
					}
					line, e := bufio.NewReader(conn).ReadString('\n')
					if e != nil || line != "STARTTLS\n" {
						serverResult <- fmt.Errorf("upgrade command %q: %v", line, e)
						return
					}
					if _, err = io.WriteString(conn, "GO\n"); err != nil {
						serverResult <- err
						return
					}
				}
				secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
				if err = secure.Handshake(); err != nil {
					serverResult <- err
					return
				}
				line, err := bufio.NewReader(secure).ReadString('\n')
				if err == nil && line != "hello\n" {
					err = fmt.Errorf("unexpected TLS data %q", line)
				}
				if err == nil {
					_, err = io.WriteString(secure, "world\n")
				}
				serverResult <- err
			}()
			port := listener.Addr().(*net.TCPAddr).Port
			script := `async function verify(port, ca, upgrade) {
   const tls=require('tls'), net=require('net'); let socket;
   const wait=(s,event)=>new Promise((resolve,reject)=>{s.once(event,resolve);s.once('error',reject)});
   if(upgrade){ const raw=net.connect({host:'127.0.0.1',port});raw.setEncoding('utf8');await wait(raw,'connect');await wait(raw,'data');raw.write('STARTTLS\n');await wait(raw,'data');socket=tls.connect({socket:raw,servername:'example.com',ca}); }
   else socket=tls.connect({host:'127.0.0.1',port,servername:'example.com',ca});
   await wait(socket,'secureConnect');socket.setEncoding('utf8');socket.write('hello\n');const answer=await wait(socket,'data');socket.destroy();return answer==='world\n';
  }`
			runtime, err := New(script, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			if err := runtime.Call("verify", port, ca, upgrade); err != nil {
				t.Fatal(err)
			}
			if err := <-serverResult; err != nil && !strings.Contains(err.Error(), "closed") {
				t.Fatal(err)
			}
		})
	}
}

func TestNodeTLSRejectsUntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	runtime, err := New(`async function verify(port) {
  const socket = require('tls').connect({host:'127.0.0.1', port, servername:'example.com'});
  return await new Promise((resolve) => { socket.once('secureConnect', () => resolve(false)); socket.once('error', () => resolve(true)); });
 }`, Options{NodeJS: true, BaseDir: t.TempDir(), Console: io.Discard, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify", port); err != nil {
		t.Fatal(err)
	}
}
