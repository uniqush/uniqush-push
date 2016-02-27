// Package mocks implements a mock APNS server, for unit tests.
// Instead of a TCP socket, the mock connection uses a golang chan of bytes.
package mocks

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	_ "testing"
	"time"
)

type APNSNotificaton struct {
	command    uint8
	id         uint32
	expiry     uint32
	tokenLen   uint16
	devToken   []byte
	payloadLen uint16
	payload    []byte
}

func (self *APNSNotificaton) String() string {
	token := hex.EncodeToString(self.devToken)
	token = strings.ToLower(token)
	return fmt.Sprintf("command=%v; id=%v; expiry=%v; token=%v; payload=%v",
		self.command, self.id, self.expiry, token, string(self.payload))
}

type APNSResponse struct {
	id     uint32
	status uint8
}

type MockDirectionalConn struct {
	channel chan byte
}

var _ io.ReadWriter = &MockDirectionalConn{}

func newMockDirectionalConn() *MockDirectionalConn {
	return &MockDirectionalConn{
		channel: make(chan byte),
	}
}

func (self *MockDirectionalConn) Write(b []byte) (n int, err error) {
	for _, x := range b {
		self.channel <- x
	}
	return len(b), nil
}

func (self *MockDirectionalConn) Read(b []byte) (int, error) {
	n := len(b)
	for x := 0; x < n; x++ {
		result, ok := <-self.channel
		if !ok {
			if x != 0 {
				panic("Mock read unexpectedly cut off, shouldn't happen\n")
			}
			return x, errors.New("Mock connection was closed")
		}
		b[x] = result
	}
	return n, nil
}

func (self *MockDirectionalConn) CleanUp() {
	close(self.channel)
}

type MockNetConn struct {
	readConn       *MockDirectionalConn
	writeConn      *MockDirectionalConn
	isClientClosed bool
	rwmutex        sync.RWMutex
}

var _ net.Conn = &MockNetConn{}

func NewMockNetConn() *MockNetConn {
	return &MockNetConn{
		readConn:       newMockDirectionalConn(),
		writeConn:      newMockDirectionalConn(),
		isClientClosed: false,
	}
}

func (self *MockNetConn) CleanUp() {
	self.readConn.CleanUp()
	self.rwmutex.Lock()
	defer self.rwmutex.Unlock()
	// TODO: attempt read?
	if !self.isClientClosed {
		panic("Client code didn't clean up the channel")
	}
}

func (self *MockNetConn) Read(b []byte) (n int, err error) {
	self.rwmutex.RLock()
	defer self.rwmutex.RUnlock()
	return self.readConn.Read(b)
}

func (self *MockNetConn) Write(b []byte) (n int, err error) {
	self.rwmutex.RLock()
	defer self.rwmutex.RUnlock()
	return self.writeConn.Write(b)
}

func (self *MockNetConn) Close() error {
	// Allow isClientClosed to be called multiple times - from resultCollector and from worker pool
	// (e.g. if both of them notice connection issues)
	if !self.isClientClosed {
		self.writeConn.CleanUp()
		self.isClientClosed = true
	}
	return nil
}

func (self *MockNetConn) LocalAddr() net.Addr {
	return nil
}

func (self *MockNetConn) RemoteAddr() net.Addr {
	return nil
}

func (self *MockNetConn) SetDeadline(t time.Time) error {
	self.SetReadDeadline(t)
	return self.SetWriteDeadline(t)
}

func (self *MockNetConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (self *MockNetConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func (self *MockNetConn) ReadNotification() (notif *APNSNotificaton, err error) {
	notif = new(APNSNotificaton)
	// Read the bytes that the *tested* code sent
	var conn io.Reader = self.writeConn
	err = binary.Read(conn, binary.BigEndian, &(notif.command))
	if err != nil {
		notif = nil
		return
	}

	if notif.command == 1 {
		err = binary.Read(conn, binary.BigEndian, &(notif.id))
		if err != nil {
			notif = nil
			return
		}
		err = binary.Read(conn, binary.BigEndian, &(notif.expiry))
		if err != nil {
			notif = nil
			return
		}
	} else if notif.command != 0 {
		notif = nil
		err = fmt.Errorf("Unkown Command")
		return
	}
	err = binary.Read(conn, binary.BigEndian, &(notif.tokenLen))
	if err != nil {
		notif = nil
		return
	}

	// devtoken
	notif.devToken = make([]byte, notif.tokenLen)
	n, err := io.ReadFull(conn, notif.devToken)
	if err != nil {
		notif = nil
		return
	}
	if n != int(notif.tokenLen) {
		notif = nil
		err = fmt.Errorf("no enough data")
		return
	}

	// payload size
	err = binary.Read(conn, binary.BigEndian, &(notif.payloadLen))
	if err != nil {
		notif = nil
		return
	}

	// payload
	notif.payload = make([]byte, notif.payloadLen)
	n, err = io.ReadFull(conn, notif.payload)
	if err != nil {
		notif = nil
		return
	}
	if n != int(notif.payloadLen) {
		notif = nil
		err = fmt.Errorf("no enough data: payload")
		return
	}
	return
}

func (self *MockNetConn) Reply(status *APNSResponse) error {
	var command uint8
	command = 8
	// Write to the mock connection half that it's reading *from*
	var conn io.Writer = self.readConn
	err := binary.Write(conn, binary.BigEndian, command)
	if err != nil {
		return err
	}
	err = binary.Write(conn, binary.BigEndian, status.status)
	if err != nil {
		return err
	}
	err = binary.Write(conn, binary.BigEndian, status.id)
	if err != nil {
		return err
	}
	return nil
}

func SimulateStableAPNSServer(conn *MockNetConn, statusCode uint8) (int, error) {
	count := 0
	for {
		notif, err := conn.ReadNotification()
		if err != nil {
			return count, err
		}
		status := &APNSResponse{
			id:     notif.id,
			status: statusCode,
		}
		conn.Reply(status)
		count += 1
	}
}
