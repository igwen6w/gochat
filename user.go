package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrInvalidMessage = errors.New("invalid message format")
	ErrUserNotFound   = errors.New("user not found")
)

const (
	inactivityTimeout     = 60 * time.Second
	bufferSize            = 1024
	activityCheckInterval = time.Second
	channelBufferSize     = 100
)

// MessageType 定义消息类型
type MessageType int

const (
	BroadcastMessage MessageType = iota
	PrivateMessage
	SystemMessage
)

// Message 定义消息结构
type Message struct {
	Type    MessageType
	From    string
	To      string
	Content string
}

// User 定义用户结构
type User struct {
	Name        string
	messageChan chan Message
	conn        net.Conn
	server      *Server
	lastActive  atomic.Value
	ctx         context.Context
	cancel      context.CancelFunc
	offlineOnce sync.Once
	errChan     chan error
}

// UserOption 定义用户选项函数类型
type UserOption func(*User)

// WithBufferSize 设置消息缓冲区大小的选项
func WithBufferSize(size int) UserOption {
	return func(u *User) {
		u.messageChan = make(chan Message, size)
	}
}

// NewUser 创建新用户
func NewUser(conn net.Conn, server *Server, opts ...UserOption) *User {
	ctx, cancel := context.WithCancel(context.Background())
	user := &User{
		Name:        conn.RemoteAddr().String(),
		messageChan: make(chan Message, channelBufferSize),
		conn:        conn,
		server:      server,
		ctx:         ctx,
		cancel:      cancel,
		errChan:     make(chan error, 1),
	}

	// 应用选项
	for _, opt := range opts {
		opt(user)
	}

	user.lastActive.Store(time.Now())
	user.start()

	log.Printf("新用户连接: %s", user.Name)
	return user
}

// start 启动用户的所有goroutine
func (u *User) start() {
	go u.monitorActivity()
	go u.monitorErrors()
	go u.handleMessages()
}

// monitorErrors 监控错误
func (u *User) monitorErrors() {
	for {
		select {
		case err := <-u.errChan:
			log.Printf("用户 %s 错误: %v", u.Name, err)
			u.OffLine()
			return
		case <-u.ctx.Done():
			return
		}
	}
}

func (u *User) monitorActivity() {
	ticker := time.NewTicker(activityCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Since(u.lastActive.Load().(time.Time)) > inactivityTimeout {
				u.errChan <- fmt.Errorf("用户 %s 超时", u.Name)
				return
			}
		case <-u.ctx.Done():
			return
		}
	}
}

func (u *User) handleMessages() {
	var wg sync.WaitGroup
	wg.Add(2)

	// 读取消息
	go func() {
		defer wg.Done()
		u.readMessages()
	}()

	// 写入消息
	go func() {
		defer wg.Done()
		u.writeMessages()
	}()

	wg.Wait()
}

func (u *User) readMessages() {
	reader := NewBufferedReader(u.conn, bufferSize)
	for {
		select {
		case <-u.ctx.Done():
			return
		default:
			msg, err := reader.ReadMessage()
			if err != nil {
				log.Printf("读取消息错误: %v", err)
				return
			}

			u.lastActive.Store(time.Now())
			if err := u.processMessage(msg); err != nil {
				log.Printf("处理消息错误: %v", err)
			}
		}
	}
}

func (u *User) writeMessages() {
	writer := NewBufferedWriter(u.conn)
	for {
		select {
		case <-u.ctx.Done():
			return
		case msg, ok := <-u.messageChan:
			if !ok {
				return
			}
			if err := writer.WriteMessage(msg); err != nil {
				log.Printf("写入消息错误: %v", err)
				return
			}
		}
	}
}

func (u *User) processMessage(content string) error {
	content = strings.TrimSpace(content)

	switch {
	case content == "who":
		return u.listOnlineUsers()
	case content == "Ping":
		return u.sendSystemMessage("Pong")
	case content == "offline" || content == "exit":
		u.errChan <- fmt.Errorf("用户 %s 退出", u.Name)
		return nil
	case strings.HasPrefix(content, "rename|"):
		return u.handleRename(content)
	case strings.HasPrefix(content, "to|"):
		return u.handlePrivateMessage(content)
	default:
		return u.broadcastMessage(content)
	}
}

func (u *User) sendSystemMessage(content string) error {
	u.messageChan <- Message{
		Type:    SystemMessage,
		Content: content,
	}
	return nil
}

func (u *User) listOnlineUsers() error {
	users := u.server.GetOnlineUsers()
	for _, name := range users {
		if err := u.sendSystemMessage(name + "_在线"); err != nil {
			return err
		}
	}
	return nil
}

func (u *User) handleRename(msg string) error {
	newName := strings.TrimPrefix(msg, "rename|")
	if err := u.server.RenameUser(u, newName); err != nil {
		return err
	}
	return nil
}

func (u *User) handlePrivateMessage(msg string) error {
	parts := strings.SplitN(msg, "|", 3)
	if len(parts) != 3 {
		return ErrInvalidMessage
	}

	return u.server.SendPrivateMessage(u, parts[1], parts[2])
}

func (u *User) broadcastMessage(content string) error {
	return u.server.BroadcastMessage(Message{
		Type:    BroadcastMessage,
		From:    u.Name,
		Content: content,
	})
}

func (u *User) OffLine() {
	u.offlineOnce.Do(func() {
		defer u.cancel()

		close(u.messageChan)
		close(u.errChan)

		if err := u.conn.Close(); err != nil {
			log.Printf("关闭用户 %s 连接失败: %v", u.Name, err)
		}

		log.Printf("%s 已离线", u.Name)

		runtime.Goexit()
	})
}

// BufferedReader 缓冲读取器
type BufferedReader struct {
	reader *bufio.Reader
}

func NewBufferedReader(r io.Reader, size int) *BufferedReader {
	return &BufferedReader{
		reader: bufio.NewReaderSize(r, size),
	}
}

func (br *BufferedReader) ReadMessage() (string, error) {
	line, err := br.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// BufferedWriter 缓冲写入器
type BufferedWriter struct {
	writer *bufio.Writer
	mu     sync.Mutex
}

func NewBufferedWriter(w io.Writer) *BufferedWriter {
	return &BufferedWriter{
		writer: bufio.NewWriter(w),
	}
}

func (bw *BufferedWriter) WriteMessage(msg Message) error {
	bw.mu.Lock()
	defer bw.mu.Unlock()

	var content string
	switch msg.Type {
	case PrivateMessage:
		content = fmt.Sprintf("%s:%s\n", msg.From, msg.Content)
	case SystemMessage:
		content = fmt.Sprintf("%s\n", msg.Content)
	default:
		content = fmt.Sprintf("%s:%s\n", msg.From, msg.Content)
	}

	if _, err := bw.writer.WriteString(content); err != nil {
		return err
	}
	return bw.writer.Flush()
}
