package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

var (
	ErrServerClosed = errors.New("server closed")
	ErrNameExists   = errors.New("username already exists")
)

type ServerConfig struct {
	IP              string
	Port            int
	MessageBufSize  int
	ShutdownTimeout time.Duration
}

// DefaultConfig 返回默认配置
func DefaultConfig() ServerConfig {
	return ServerConfig{
		IP:              "0.0.0.0",
		Port:            8888,
		MessageBufSize:  1000,
		ShutdownTimeout: 30 * time.Second,
	}
}

type Server struct {
	config ServerConfig

	// 核心组件
	listener  net.Listener
	users     sync.Map // 替换 map，使用并发安全的 sync.Map
	broadcast chan Message

	// 上下文控制
	ctx    context.Context
	cancel context.CancelFunc

	// 关闭相关
	closeOnce sync.Once
	wg        sync.WaitGroup

	// 指标统计
	stats *ServerStats
}

type ServerStats struct {
	sync.RWMutex
	connectedUsers   int64
	messagesSent     int64
	messagesReceived int64
}

func NewServer(config ServerConfig) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	return &Server{
		config:    config,
		broadcast: make(chan Message, config.MessageBufSize),
		ctx:       ctx,
		cancel:    cancel,
		stats:     &ServerStats{},
	}
}

func (s *Server) Run() error {
	addr := fmt.Sprintf("%s:%d", s.config.IP, s.config.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	s.listener = listener

	// 启动广播处理
	s.wg.Add(1)
	go s.handleBroadcast()

	// 启动指标收集
	s.wg.Add(1)
	go s.collectMetrics()

	log.Printf("Server started on %s", addr)

	// 接受连接
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return ErrServerClosed
			default:
				log.Printf("Accept error: %v", err)
				continue
			}
		}

		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	// 创建用户
	user := NewUser(conn, s, WithBufferSize(100))
	if err := s.registerUser(user); err != nil {
		log.Printf("Failed to register user: %v", err)
		return
	}

	// 处理用户连接
	s.handleUser(user)
}

func (s *Server) registerUser(user *User) error {
	// 检查用户名是否已存在
	if _, exists := s.users.LoadOrStore(user.Name, user); exists {
		return ErrNameExists
	}

	s.stats.Lock()
	s.stats.connectedUsers++
	s.stats.Unlock()

	s.broadcastSystemMessage(fmt.Sprintf("%s 已上线", user.Name))
	return nil
}

func (s *Server) handleUser(user *User) {
	defer s.unregisterUser(user)

	// 等待用户退出或连接断开
	<-user.ctx.Done()
}

func (s *Server) unregisterUser(user *User) {
	s.users.Delete(user.Name)

	s.stats.Lock()
	s.stats.connectedUsers--
	s.stats.Unlock()

	s.broadcastSystemMessage(fmt.Sprintf("%s 已下线", user.Name))
}

func (s *Server) handleBroadcast() {
	defer s.wg.Done()

	for {
		select {
		case msg := <-s.broadcast:
			s.deliverMessage(msg)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Server) deliverMessage(msg Message) {
	s.stats.Lock()
	s.stats.messagesSent++
	s.stats.Unlock()

	s.users.Range(func(key, value interface{}) bool {
		if user, ok := value.(*User); ok {
			select {
			case user.messageChan <- msg:
			default:
				log.Printf("Failed to deliver message to user %s: channel full", user.Name)
			}
		}
		return true
	})
}

func (s *Server) broadcastSystemMessage(content string) {
	s.broadcast <- Message{
		Type:    SystemMessage,
		Content: content,
	}
}

func (s *Server) BroadcastMessage(msg Message) error {
	select {
	case s.broadcast <- msg:
		s.stats.Lock()
		s.stats.messagesReceived++
		s.stats.Unlock()
		return nil
	case <-s.ctx.Done():
		return ErrServerClosed
	}
}

func (s *Server) SendPrivateMessage(from *User, toName, content string) error {
	if value, ok := s.users.Load(toName); ok {
		if toUser, ok := value.(*User); ok {
			msg := Message{
				Type:    PrivateMessage,
				From:    from.Name,
				To:      toName,
				Content: content,
			}
			select {
			case toUser.messageChan <- msg:
				return nil
			default:
				return errors.New("message channel full")
			}
		}
	}
	return ErrUserNotFound
}

func (s *Server) RenameUser(user *User, newName string) error {
	if _, exists := s.users.Load(newName); exists {
		return ErrNameExists
	}

	// 原子性地更新用户名
	s.users.Delete(user.Name)
	if _, exists := s.users.LoadOrStore(newName, user); exists {
		// 恢复原来的映射
		s.users.Store(user.Name, user)
		return ErrNameExists
	}

	oldName := user.Name
	user.Name = newName

	s.broadcastSystemMessage(fmt.Sprintf("%s 改名为 %s", oldName, newName))
	return nil
}

func (s *Server) GetOnlineUsers() []string {
	var users []string
	s.users.Range(func(key, value interface{}) bool {
		if name, ok := key.(string); ok {
			users = append(users, name)
		}
		return true
	})
	return users
}

func (s *Server) Shutdown() error {
	var err error
	s.closeOnce.Do(func() {
		// 发送关闭信号
		s.cancel()

		// 关闭监听器
		if s.listener != nil {
			err = s.listener.Close()
		}

		// 等待所有 goroutine 完成
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()

		// 等待超时
		select {
		case <-done:
		case <-time.After(s.config.ShutdownTimeout):
			err = errors.New("shutdown timeout")
		}
	})
	return err
}

// 指标收集
func (s *Server) collectMetrics() {
	defer s.wg.Done()

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.stats.RLock()
			log.Printf("Server Stats - Connected Users: %d, Messages Sent: %d, Messages Received: %d",
				s.stats.connectedUsers, s.stats.messagesSent, s.stats.messagesReceived)
			s.stats.RUnlock()
		case <-s.ctx.Done():
			return
		}
	}
}
