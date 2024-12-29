package main

import (
	"fmt"
	"net"
)

type Server struct {
	Ip   string
	Port int
}

func NewServer(ip string, port int) *Server {
	return &Server{Ip: ip, Port: port}
}

func (s *Server) Run() {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.Ip, s.Port))
	if err != nil {
		// net.Listen err
		fmt.Println("Error listening:", err)
		return
	}

	defer listener.Close()

	for {
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
	fmt.Println("服务器已创建连接")
}
