package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Использование: client <server-ip> <username>")
		return
	}

	serverIP := os.Args[1]
	username := os.Args[2]

	serverAddr, err := net.ResolveUDPAddr("udp", serverIP+":6000")
	if err != nil {
		fmt.Println("Ошибка разрешения адреса:", err)
		return
	}

	conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		fmt.Println("Ошибка подключения:", err)
		return
	}
	defer conn.Close()

	// Отправляем сообщение о подключении
	_, err = conn.Write([]byte(username + " joined the chat"))
	if err != nil {
		fmt.Println("Ошибка отправки:", err)
		return
	}

	// Горутина для чтения входящих сообщений
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, _, err := conn.ReadFromUDP(buffer)
			if err != nil {
				fmt.Println("Ошибка чтения:", err)
				return
			}
			fmt.Printf("\r%s\n> ", string(buffer[:n]))
		}
	}()

	// Чтение ввода пользователя
	fmt.Print("> ")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := scanner.Text()
		if text == "exit" {
			break
		}

		_, err := conn.Write([]byte("[" + username + "]: " + text))
		if err != nil {
			fmt.Println("Ошибка отправки:", err)
			return
		}
		fmt.Print("> ")
	}
}
