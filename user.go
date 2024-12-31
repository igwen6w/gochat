package main

import (
	"context"
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

const (
	inactivityTimeout     = 10 * time.Second
	bufferSize            = 1024
	activityCheckInterval = time.Second
)

type User struct {
	Name        string
	C           chan string
	Conn        net.Conn
	Server      *Server
	lastActive  atomic.Value // 使用原子操作替代指针
	ctx         context.Context
	cancel      context.CancelFunc
	offlineOnce sync.Once
}

func newUser(conn net.Conn, server *Server) *User {
	ctx, cancel := context.WithCancel(context.Background())
	user := &User{
		Name:   conn.RemoteAddr().String(),
		C:      make(chan string, bufferSize), // 添加缓冲区
		Conn:   conn,
		Server: server,
		ctx:    ctx,
		cancel: cancel,
	}

	user.lastActive.Store(time.Now())

	go user.handleMessages() // 合并消息处理
	go user.monitorActivity()

	log.Printf("新用户连接: %s", user.Name)
	return user
}

func (u *User) monitorActivity() {
	ticker := time.NewTicker(activityCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			lastActive := u.lastActive.Load().(time.Time)
			if time.Since(lastActive) > inactivityTimeout {
				log.Printf("用户 %s 因超时下线", u.Name)
				u.OffLine()
				return
			}
		case <-u.ctx.Done():
			return
		}
	}
}

func (u *User) handleMessages() {
	go u.readMessages() // 读取消息
	u.writeMessages()   // 写入消息
}

func (u *User) readMessages() {
	buf := make([]byte, bufferSize)
	for {
		select {
		case <-u.ctx.Done():
			return
		default:
			n, err := u.Conn.Read(buf)
			if err != nil || n == 0 {
				if err != nil && err != io.EOF {
					log.Printf("用户 %s 读取错误: %v", u.Name, err)
				}
				u.OffLine()
				return
			}

			u.lastActive.Store(time.Now())
			msg := strings.TrimSpace(string(buf[:n]))
			u.processMessage(msg)
		}
	}
}

func (u *User) writeMessages() {
	for {
		select {
		case message, ok := <-u.C:
			if !ok {
				return
			}
			if err := u.writeMessage(message); err != nil {
				log.Printf("用户 %s 发送消息失败: %v", u.Name, err)
				return
			}
		case <-u.ctx.Done():
			return
		}
	}
}

func (u *User) writeMessage(message string) error {
	_, err := u.Conn.Write([]byte(fmt.Sprintf("%s\n", message)))
	return err
}

func (u *User) processMessage(msg string) {
	switch {
	case msg == "who":
		u.listOnlineUsers()
	case msg == "Ping":
		u.C <- "Pong"
	case msg == "offline" || msg == "exit":
		u.OffLine()
	case strings.HasPrefix(msg, "rename|"):
		u.Rename(strings.TrimPrefix(msg, "rename|"))
	case strings.HasPrefix(msg, "to|"):
		u.handlePrivateMessage(msg)
	default:
		u.Server.PushMessage(u, msg)
	}
}

func (u *User) listOnlineUsers() {
	u.Server.mapLock.RLock() // 使用读锁而不是写锁
	defer u.Server.mapLock.RUnlock()

	for name := range u.Server.OnlineMap {
		u.C <- name + "_在线"
	}
}

func (u *User) handlePrivateMessage(msg string) {
	parts := strings.SplitN(msg, "|", 3)
	if len(parts) != 3 {
		return
	}

	u.Server.mapLock.RLock()
	targetUser, exists := u.Server.OnlineMap[parts[1]]
	u.Server.mapLock.RUnlock()

	if exists {
		targetUser.C <- fmt.Sprintf("%s:%s", u.Name, parts[2])
	}
}

func (u *User) OffLine() {
	u.offlineOnce.Do(func() {
		u.cancel()
		u.Server.DeleteOnlineMap(u)
		u.Server.PushMessage(u, "下线了")
		close(u.C)

		if err := u.Conn.Close(); err != nil {
			log.Printf("关闭用户 %s 连接失败: %v", u.Name, err)
		}

		runtime.Goexit()
	})
}

func (u *User) Rename(newName string) {
	oldName := u.Name
	if u.Server.UpdateOnlineMap(u, newName) {
		u.Server.PushMessage(u, fmt.Sprintf("从 %s 改为 %s", oldName, newName))
	}
}
