package main

import (
	"fmt"
	"net"
	"sync"
)

type Server struct {
	Ip   string
	Port int
	// online map
	OnlineMap map[string]*User
	mapLock   sync.RWMutex

	// 广播消息渠道
	Message chan string
}

func NewServer(ip string, port int) *Server {
	return &Server{
		Ip:        ip,
		Port:      port,
		OnlineMap: make(map[string]*User),
		Message:   make(chan string),
	}
}

func (s *Server) Run() {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.Ip, s.Port))
	if err != nil {
		// net.Listen err
		fmt.Println("Error listening:", err)
		return
	}

	defer listener.Close()

	// 广播消息
	go s.BroadCastMessage()

	for {
		// 监听连接
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting connection")
			continue
		}

		go s.Handler(conn)
	}
}

func (s *Server) Handler(conn net.Conn) {
	// 业务逻辑
	fmt.Println("用户创建连接")

	// 用户上线
	s.UserOnline(conn)

	select {}
}

func (s *Server) UserOnline(conn net.Conn) {
	// 创建用户
	user := newUser(conn, s)

	// 加入在线用户列表
	s.AddOnlineMap(user)

	// 发送用户上线消息
	s.PushMessage(user, "上线了")
}

func (s *Server) AddOnlineMap(user *User) {
	s.mapLock.Lock()
	s.OnlineMap[user.Name] = user
	s.mapLock.Unlock()
}

// PushMessage 添加一条消息到消息渠道
func (s *Server) PushMessage(user *User, msg string) {
	s.Message <- fmt.Sprintf("[%s]:%s", user.Name, msg)
}

// BroadCastMessage 广播消息
func (s *Server) BroadCastMessage() {
	for {
		msg := <-s.Message
		// server log
		fmt.Println(msg)
		for _, user := range s.OnlineMap {
			user.C <- msg
		}
	}
}
