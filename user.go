package main

import (
	"fmt"
	"net"
)

type User struct {
	Name string
	Addr string
	C    chan string
	Conn net.Conn
}

func newUser(conn net.Conn) *User {
	userAddr := conn.RemoteAddr().String()
	user := &User{
		Name: userAddr,
		Addr: userAddr,
		C:    make(chan string),
		Conn: conn,
	}

	go user.ShowMessage()

	return user
}

func (u *User) SendMessage() {
}

func (u *User) ShowMessage() {
	for {
		message := <-u.C

		u.Conn.Write([]byte(fmt.Sprintf("%s\n", message)))
	}
}
