package main

import (
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
)

type User struct {
	Name   string
	Addr   string
	C      chan string
	Conn   net.Conn
	Server *Server
}

func newUser(conn net.Conn, server *Server) *User {
	userAddr := conn.RemoteAddr().String()
	user := &User{
		Name:   userAddr,
		Addr:   userAddr,
		C:      make(chan string),
		Conn:   conn,
		Server: server,
	}

	// 接收显示消息
	go user.ShowMessage()

	// 监听消息和指令
	go user.ListenMessage()

	return user
}

func (u *User) ListenMessage() {
	buf := make([]byte, 1024)
	for {
		n, err := u.Conn.Read(buf)
		if err != nil && err != io.EOF {
			fmt.Println("conn.read err:", err)
			return
		}

		if n == 0 {
			u.OffLine()
		}

		u.SendMessage(string(buf[:n-1]))
	}
}

// SendMessage 发送消息
func (u *User) SendMessage(msg string) {
	if msg == "who" {
		u.Server.mapLock.Lock()
		for name, _ := range u.Server.OnlineMap {
			u.C <- name + "_在线"
		}
		u.Server.mapLock.Unlock()
	} else if msg == "Ping" {
		u.C <- "Pong"
	} else if msg == "offline" || msg == "exit" {
		u.OffLine()
	} else if len(msg) >= 8 && msg[0:7] == "rename|" {
		// 用户重命名，格式：rename|string
		u.Rename(msg[7:])
	} else if len(msg) > 4 && msg[:3] == "to|" {
		// 格式：to|name|message
		a := strings.Split(msg, "|")
		if name := a[1]; len(name) > 0 {
			if user, ok := u.Server.OnlineMap[name]; ok {
				// 给用户发送消息
				u.ToMessage(user, a[2])
			}
		}
	} else {
		u.Server.PushMessage(u, msg)
	}
}

// ShowMessage 显示消息
func (u *User) ShowMessage() {
	for {
		message := <-u.C

		u.Conn.Write([]byte(fmt.Sprintf("%s\n", message)))
	}
}

// OffLine 用户下线
func (u *User) OffLine() {
	u.Conn.Close()

	u.Server.DeleteOnlineMap(u)

	u.Server.PushMessage(u, "下线了")

	runtime.Goexit()
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
