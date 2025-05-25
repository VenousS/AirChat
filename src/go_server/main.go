package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Client struct {
	addr      net.Addr
	username  string
	inVoice   bool
	voiceAddr string // Добавляем адрес для голосового соединения
}

var (
	clients    = make(map[string]*Client)
	clientsMux sync.RWMutex
)

func cleanup(pc, voiceConn net.PacketConn) {
	log.Println("Завершение работы сервера...")

	// Отправляем всем клиентам сообщение о завершении работы
	clientsMux.RLock()
	for _, client := range clients {
		pc.WriteTo([]byte("Сервер завершает работу"), client.addr)
	}
	clientsMux.RUnlock()

	// Закрываем соединения
	if pc != nil {
		pc.Close()
	}
	if voiceConn != nil {
		voiceConn.Close()
	}
}

func handleVoiceData(voiceConn net.PacketConn) {
	buffer := make([]byte, 4096)
	var lastLogTime time.Time
	var bytesProcessed int
	var packetsProcessed int
	var lastClientListTime time.Time

	log.Println("Запущен обработчик голосовых данных")

	for {
		n, remoteAddr, err := voiceConn.ReadFrom(buffer)
		if err != nil {
			log.Printf("Ошибка чтения голосовых данных: %v", err)
			continue
		}

		senderIP := strings.Split(remoteAddr.String(), ":")[0]
		bytesProcessed += n
		packetsProcessed++

		// Логируем статистику каждые 5 секунд
		if time.Since(lastLogTime) > 5*time.Second {
			log.Printf("Статистика: получено %d пакетов, %d байт (%.2f КБ/с)",
				packetsProcessed, bytesProcessed, float64(bytesProcessed)/5.0/1024.0)
			lastLogTime = time.Now()
			bytesProcessed = 0
			packetsProcessed = 0
		}

		// Ищем отправителя по voiceAddr, если не найден — по IP
		var sender *Client
		var exists bool

		clientsMux.Lock() // LOCK для возможного обновления!
		for _, client := range clients {
			if client.voiceAddr == remoteAddr.String() {
				sender = client
				exists = true
				break
			}
		}
		// Если не найден по voiceAddr — ищем по IP и inVoice
		if !exists {
			for _, client := range clients {
				if client.inVoice && strings.Split(client.addr.String(), ":")[0] == senderIP {
					log.Printf("Обновление voiceAddr для %s: %s -> %s", client.username, client.voiceAddr, remoteAddr.String())
					client.voiceAddr = remoteAddr.String()
					sender = client
					exists = true
					break
				}
			}
		}

		if !exists || !sender.inVoice {
			clientsMux.Unlock()
			// Выводим список клиентов только раз в 10 секунд
			if time.Since(lastClientListTime) > 10*time.Second {
				log.Printf("❌ Неавторизованный клиент: %s (IP: %s)", remoteAddr.String(), senderIP)
				log.Println("Список активных клиентов:")
				for _, c := range clients {
					status := "🔇"
					if c.inVoice {
						status = "🔊"
					}
					log.Printf("%s %s (%s) -> %s", status, c.username,
						strings.Split(c.addr.String(), ":")[0], c.voiceAddr)
				}
				lastClientListTime = time.Now()
			}
			continue
		}

		// Отправляем голосовые данные всем клиентам в голосовом чате
		recipientCount := 0
		for _, client := range clients {
			if client.inVoice && client.voiceAddr != remoteAddr.String() {
				voiceAddr, err := net.ResolveUDPAddr("udp", client.voiceAddr)
				if err != nil {
					log.Printf("❌ Ошибка адреса %s: %v", client.username, err)
					continue
				}
				_, err = voiceConn.WriteTo(buffer[:n], voiceAddr)
				if err != nil {
					log.Printf("❌ Ошибка отправки %s: %v", client.username, err)
				} else {
					recipientCount++
				}
			}
		}

		// Логируем успешные передачи только при наличии получателей
		if recipientCount > 0 && time.Since(lastLogTime) > 5*time.Second {
			log.Printf("✅ %s -> %d клиентам (%d байт)", sender.username, recipientCount, n)
		}

		clientsMux.Unlock()
	}
}

func main() {
	// Создаем канал для обработки сигналов завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	pc, err := net.ListenPacket("udp", ":6000")
	if err != nil {
		log.Fatal("Ошибка запуска сервера:", err)
	}

	voiceConn, err := net.ListenPacket("udp", ":6001")
	if err != nil {
		pc.Close()
		log.Fatal("Ошибка запуска голосового сервера:", err)
	}

	// Отложенная очистка ресурсов
	defer cleanup(pc, voiceConn)

	log.Println("Сервер запущен на порту :6000")
	log.Println("Голосовой сервер запущен на порту :6001")

	// Запускаем обработку голосовых данных в отдельной горутине
	go handleVoiceData(voiceConn)

	// Горутина для обработки сигналов завершения
	go func() {
		<-sigChan
		cleanup(pc, voiceConn)
		os.Exit(0)
	}()

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
			clientIP := strings.Split(clientKey, ":")[0]

			clientsMux.Lock()
			clients[clientKey] = &Client{
				addr:      addr,
				username:  username,
				inVoice:   false,
				voiceAddr: clientIP + ":6001",
			}
			clientsMux.Unlock()
			log.Printf("✨ Новый клиент: %s (%s) -> %s", username, clientIP, clientIP+":6001")

			// Уведомляем всех о новом пользователе
			clientsMux.RLock()
			for _, client := range clients {
				if client.addr.String() != clientKey {
					pc.WriteTo([]byte(msg), client.addr)
				}
			}
			clientsMux.RUnlock()
			continue
		}

		// Обработка голосовых уведомлений
		if msg == "VOICE_CONNECT" {
			clientsMux.Lock()
			if client, ok := clients[clientKey]; ok {
				client.inVoice = true
				notification := client.username + " подключился к голосовому чату"
				log.Printf("🎤 %s (%s) вошёл в голосовой чат",
					client.username, strings.Split(clientKey, ":")[0])

				// Уведомляем всех о подключении к голосовому чату
				for _, c := range clients {
					pc.WriteTo([]byte(notification), c.addr)
				}
			} else {
				log.Printf("❌ Попытка подключения от неизвестного: %s", clientKey)
			}
			clientsMux.Unlock()
			continue
		}

		if msg == "VOICE_DISCONNECT" {
			clientsMux.Lock()
			if client, ok := clients[clientKey]; ok {
				client.inVoice = false
				notification := client.username + " отключился от голосового чата"
				log.Printf("🔇 %s (%s) вышел из голосового чата",
					client.username, strings.Split(clientKey, ":")[0])

				// Уведомляем всех об отключении от голосового чата
				for _, c := range clients {
					pc.WriteTo([]byte(notification), c.addr)
				}
			}
			clientsMux.Unlock()
			continue
		}

		// Рассылаем обычные сообщения всем клиентам
		log.Printf("Сообщение от %s: %s", clientKey, msg)
		clientsMux.RLock()
		for _, client := range clients {
			if client.addr.String() != clientKey {
				pc.WriteTo([]byte(msg), client.addr)
			}
		}
		clientsMux.RUnlock()
	}
}
