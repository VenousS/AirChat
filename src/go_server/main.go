package main

import (
	"log"
	"net"
	"strings"
)

func main() {
	pc, err := net.ListenPacket("udp", ":6000")
	if err != nil {
		log.Fatal("Ошибка запуска сервера:", err)
	}
	defer pc.Close()

	log.Println("Сервер запущен на порту :6000")
	clients := make(map[string]net.Addr)

	for {
		buffer := make([]byte, 4096)
		n, addr, err := pc.ReadFrom(buffer)
		if err != nil {
			log.Printf("Ошибка чтения: %v", err)
			continue
		}

		msg := string(buffer[:n])
		clientKey := addr.String()

		// Обработка нового подключения
		if strings.Contains(msg, " joined the chat") {
			username := strings.Split(msg, " joined the chat")[0]
			clients[clientKey] = addr
			log.Printf("Новый клиент: %s (%s)", username, clientKey)

			// Уведомляем всех о новом пользователе
			for _, clientAddr := range clients {
				if clientAddr.String() != clientKey {
					pc.WriteTo([]byte(msg), clientAddr)
				}
			}
			continue
		}

		// Рассылаем обычные сообщения всем клиентам
		log.Printf("Сообщение от %s: %s", clientKey, msg)
		for _, clientAddr := range clients {
			if clientAddr.String() != clientKey {
				pc.WriteTo([]byte(msg), clientAddr)
			}
		}
	}
}
