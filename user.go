package main

import (
	"fmt"
	"io"
	"net"
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

	// 发送消息和指令
	go user.SendMessage()

	return user
}

func (u *User) SendMessage() {
	buf := make([]byte, 1024)
	for {
		n, err := u.Conn.Read(buf)
		if n == 0 {
			u.Server.PushMessage(u, "下线了")
			return
		}

		if err != nil && err != io.EOF {
			fmt.Println("conn.read err:", err)
			return
		}

		msg := string(buf[:n-1])
		u.Server.PushMessage(u, msg)
	}
}

func (u *User) ShowMessage() {
	for {
		message := <-u.C

		u.Conn.Write([]byte(fmt.Sprintf("%s\n", message)))
	}
}
