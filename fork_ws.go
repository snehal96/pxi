package main

// A minimal WebSocket server (RFC 6455) for the Claude Code IDE bridge
// (fork_ide.go): text messages, ping/pong and close. No extensions. Kept here
// rather than adding a dependency.

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

type pxiWS struct {
	conn net.Conn
	rw   *bufio.ReadWriter
	wmu  sync.Mutex
}

const pxiWSMaxMessage = 32 << 20 // file contents travel in openDiff

func pxiWSUpgrade(w http.ResponseWriter, r *http.Request) (*pxiWS, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		return nil, errors.New("not a websocket request")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, errors.New("bad websocket handshake")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("hijack unsupported")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(sum[:]) + "\r\n"
	if p := r.Header.Get("Sec-WebSocket-Protocol"); p != "" {
		resp += "Sec-WebSocket-Protocol: " + strings.TrimSpace(strings.Split(p, ",")[0]) + "\r\n"
	}
	if _, err := rw.WriteString(resp + "\r\n"); err != nil {
		conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	return &pxiWS{conn: conn, rw: rw}, nil
}

func (c *pxiWS) writeFrame(op byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	hdr := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n <= 0xffff:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	if _, err := c.rw.Write(hdr); err != nil {
		return err
	}
	if _, err := c.rw.Write(payload); err != nil {
		return err
	}
	return c.rw.Flush()
}

func (c *pxiWS) WriteText(b []byte) error { return c.writeFrame(0x1, b) }

// ReadMessage returns the next text or binary message, answering pings.
func (c *pxiWS) ReadMessage() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(c.rw, h[:]); err != nil {
			return nil, err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		masked, n := h[1]&0x80 != 0, uint64(h[1]&0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(c.rw, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(c.rw, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if !masked {
			return nil, errors.New("client frames must be masked")
		}
		if n > pxiWSMaxMessage || uint64(len(msg))+n > pxiWSMaxMessage {
			return nil, errors.New("message too large")
		}
		var mask [4]byte
		if _, err := io.ReadFull(c.rw, mask[:]); err != nil {
			return nil, err
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.rw, payload); err != nil {
			return nil, err
		}
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
		switch op {
		case 0x8: // close
			c.writeFrame(0x8, nil)
			return nil, io.EOF
		case 0x9: // ping
			if err := c.writeFrame(0xA, payload); err != nil {
				return nil, err
			}
			continue
		case 0xA: // pong
			continue
		case 0x0, 0x1, 0x2:
			msg = append(msg, payload...)
			if fin {
				return msg, nil
			}
		default:
			return nil, errors.New("unknown websocket opcode")
		}
	}
}

func (c *pxiWS) Close() error { return c.conn.Close() }
