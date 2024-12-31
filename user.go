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
	"time"
)

type User struct {
	Name       string        // 用户名，也是地址
	C          chan string   // 消息通道
	Conn       net.Conn      // 网络连接
	Server     *Server       // 所属服务器
	LastActive *time.Time    // 最后活动时间指针
	ctx        context.Context // 带取消的上下文
	cancel     context.CancelFunc // 取消函数
	offlineOnce sync.Once    // 确保下线操作只执行一次
}

func newUser(conn net.Conn, server *Server) *User {
	userAddr := conn.RemoteAddr().String()
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	user := &User{
		Name:       userAddr,
		C:          make(chan string),
		Conn:       conn,
		Server:     server,
		LastActive: &now,
		ctx:        ctx,
		cancel:     cancel,
	}

	// 启动goroutines
	go user.ShowMessage()
	go user.ListenMessage()
	go user.CheckActivity()

	log.Printf("新用户连接: %s\n", userAddr)
	return user
}

func (u *User) CheckActivity() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Since(*u.LastActive) > 10*time.Second {
				log.Printf("用户 %s 因超时下线\n", u.Name)
				u.OffLine()
				return
			}
		case <-u.ctx.Done():
			return
		}
	}
}

func (u *User) ListenMessage() {
	buf := make([]byte, 1024)
	for {
		select {
		case <-u.ctx.Done():
			return
		default:
			n, err := u.Conn.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("用户 %s 读取错误: %v\n", u.Name, err)
				}
				u.OffLine()
				return
			}

			if n == 0 {
				log.Printf("用户 %s 断开连接\n", u.Name)
				u.OffLine()
				return
			}

			now := time.Now()
			u.LastActive = &now
			msg := strings.TrimSpace(string(buf[:n]))
			u.SendMessage(msg)
		}
	}
}

// SendMessage 处理并发送消息
func (u *User) SendMessage(msg string) {
	switch {
	case msg == "who":
		u.Server.mapLock.Lock()
		for name := range u.Server.OnlineMap {
			u.C <- name + "_在线"
		}
		u.Server.mapLock.Unlock()
	case msg == "Ping":
		u.C <- "Pong"
	case msg == "offline" || msg == "exit":
		u.OffLine()
	case strings.HasPrefix(msg, "rename|"):
		u.Rename(strings.TrimPrefix(msg, "rename|"))
	case strings.HasPrefix(msg, "to|"):
		parts := strings.SplitN(msg, "|", 3)
		if len(parts) == 3 {
			if user, ok := u.Server.OnlineMap[parts[1]]; ok {
				u.ToMessage(user, parts[2])
			}
		}
	default:
		u.Server.PushMessage(u, msg)
	}
}

// ShowMessage 显示消息
func (u *User) ShowMessage() {
	for {
		select {
		case message, ok := <-u.C:
			if !ok {
				return
			}
			_, err := u.Conn.Write([]byte(fmt.Sprintf("%s\n", message)))
			if err != nil {
				log.Printf("用户 %s 发送消息失败: %v\n", u.Name, err)
				return
			}
		case <-u.ctx.Done():
			return
		}
	}
}

// OffLine 用户下线
func (u *User) OffLine() {
	u.offlineOnce.Do(func() {
		u.cancel()

		u.Server.DeleteOnlineMap(u)

		u.Server.PushMessage(u, "下线了")

		close(u.C)

		if err := u.Conn.Close(); err != nil {
			log.Printf("关闭用户 %s 连接失败: %v\n", u.Name, err)
		}

		runtime.Goexit()
	})
}

func (u *User) Rename(newName string) {
	oldName := u.Name
	// 更新
	r := u.Server.UpdateOnlineMap(u, newName)
	if r {
		// 广播改名消息
		u.Server.PushMessage(u, fmt.Sprintf("从 %s 改为 %s", oldName, newName))
	}
}

func (u *User) ToMessage(user *User, msg string) {
	user.C <- fmt.Sprintf("%s:%s", u.Name, msg)
}
