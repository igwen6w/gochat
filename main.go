package main

func main() {
	server := NewServer(DefaultConfig())
	err := server.Run()
	if err != nil {
		return
	}
}
