//go:build !windows

package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A tiny WebSocket client: what Claude Code does when it connects.
type wsClient struct {
	c net.Conn
	r *bufio.Reader
}

func wsDial(t *testing.T, port int, header string) (*wsClient, int) {
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 16)
	rand.Read(key)
	fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n%s\r\n",
		base64.StdEncoding.EncodeToString(key), header)
	r := bufio.NewReader(c)
	res, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &wsClient{c, r}, res.StatusCode
}

func (w *wsClient) send(v any) {
	b, _ := json.Marshal(v)
	hdr := []byte{0x81}
	if len(b) < 126 {
		hdr = append(hdr, 0x80|byte(len(b)))
	} else {
		hdr = append(hdr, 0x80|126, byte(len(b)>>8), byte(len(b)))
	}
	mask := []byte{1, 2, 3, 4}
	hdr = append(hdr, mask...)
	for i := range b {
		b[i] ^= mask[i%4]
	}
	w.c.Write(append(hdr, b...))
}

func (w *wsClient) recv(t *testing.T) map[string]any {
	w.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var h [2]byte
	if _, err := io.ReadFull(w.r, h[:]); err != nil {
		t.Fatal(err)
	}
	n := uint64(h[1] & 0x7f)
	if n == 126 {
		var b [2]byte
		io.ReadFull(w.r, b[:])
		n = uint64(binary.BigEndian.Uint16(b[:]))
	} else if n == 127 {
		var b [8]byte
		io.ReadFull(w.r, b[:])
		n = binary.BigEndian.Uint64(b[:])
	}
	p := make([]byte, n)
	io.ReadFull(w.r, p)
	var m map[string]any
	json.Unmarshal(p, &m)
	return m
}

func TestPxiIDEBridge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s, root := newTestServer(t)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi\n"), 0o644)
	ide, err := s.pxiIDEStart()
	if err != nil {
		t.Fatal(err)
	}
	defer pxiIDEStop()
	lock, err := os.ReadFile(filepath.Join(home, ".claude", "ide", strconv.Itoa(ide.port)+".lock"))
	if err != nil || !strings.Contains(string(lock), `"ideName":"pxi"`) {
		t.Fatalf("lock file: %s %v", lock, err)
	}

	if _, code := wsDial(t, ide.port, "x-claude-code-ide-authorization: wrong\r\n"); code != 403 {
		t.Errorf("wrong token: want 403, got %d", code)
	}
	if _, code := wsDial(t, ide.port, "x-claude-code-ide-authorization: "+ide.token+"\r\nOrigin: http://evil.example\r\n"); code != 403 {
		t.Errorf("web page: want 403, got %d", code)
	}
	ws, code := wsDial(t, ide.port, "x-claude-code-ide-authorization: "+ide.token+"\r\n")
	if code != 101 {
		t.Fatalf("handshake: %d", code)
	}
	ws.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25", "clientInfo": map[string]any{"name": "claude-code", "version": "test"}}})
	if m := ws.recv(t); m["result"].(map[string]any)["protocolVersion"] != "2025-11-25" {
		t.Fatalf("initialize: %v", m)
	}
	ws.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "openDiff", "arguments": map[string]any{
		"old_file_path": filepath.Join(root, "a.txt"), "new_file_path": filepath.Join(root, "a.txt"), "new_file_contents": "hello\n", "tab_name": "tab-1"}}})

	// The page answers the card.
	srv := httptest.NewServer(hostGuard(s))
	defer srv.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		req, _ := http.NewRequest("POST", srv.URL+"/api/pxi/claude/answer", strings.NewReader(`{"session":"ide-1","req":"tab-1","allow":true}`))
		req.Header.Set("Origin", srv.URL)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("answer: %d", res.StatusCode)
		}
		time.Sleep(50 * time.Millisecond)
	}
	m := ws.recv(t)
	content := m["result"].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["text"] != "FILE_SAVED" || content[1].(map[string]any)["text"] != "hello\n" {
		t.Fatalf("openDiff result: %v", m)
	}
}
