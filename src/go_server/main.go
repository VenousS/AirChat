package main

import (
	"crypto/rand" // Для генерации токенов
	"encoding/hex"
	"encoding/json"
	"log"
	"math"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hraban/opus"
)

const (
	sampleRate    = 48000
	channels      = 1
	frameSize     = 960  // 20ms at 48kHz
	maxPacketSize = 1275 // Максимальный размер пакета Opus

	// Таймауты и интервалы
	clientTimeout     = 30 * time.Second // Таймаут для неактивности в голосовом чате
	heartbeatInterval = 5 * time.Second  // Интервал heartbeat для голосового чата
	maxBufferAge      = 500 * time.Millisecond

	// Константы статусов
	StatusOnline  = "online"
	StatusInVoice = "in-voice"
	StatusOffline = "offline"

	TokenLength = 16 // Длина токена в байтах (даст 32 символа в hex)
)

var usersCredentials = make(map[string]string) // Теперь это make, чтобы можно было добавлять
var usersCredentialsMux sync.RWMutex           // Мьютекс для usersCredentials

type AuthInfo struct { // Информация об аутентифицированном пользователе по токену
	Username  string
	ClientKey string // ip:port
	Token     string
	LoginTime time.Time
}

var activeUserSessions = make(map[string]*AuthInfo) // Ключ - username
var activeTokenToUser = make(map[string]string)     // Ключ - token, значение - username
var authMux sync.RWMutex                            // Мьютекс для доступа к картам аутентификации

type Client struct {
	addr         net.Addr
	username     string // Устанавливается после успешной аутентификации
	token        string // Токен текущей сессии
	inVoice      bool
	voiceAddr    string
	decoder      *opus.Decoder
	encoder      *opus.Encoder
	lastActivity time.Time // Для голосового чата
	// LastMainActivity time.Time // Удалено
	active bool
	Status string
}

// AudioBuffer больше не используется глобально, AudioProcessor управляет этим
// type AudioBuffer struct {
// 	data      []float32
// 	timestamp time.Time
// }

var (
	clients     = make(map[string]*Client)
	clientsMux  sync.RWMutex
	mixInterval = 20 * time.Millisecond
	// audioProcessor будет инициализирован в handleVoiceData
)

// <<< НОВАЯ СТРУКТУРА ДЛЯ JSON Списка Пользователей >>>
type UserStatusInfo struct {
	Username string `json:"username"`
	Status   string `json:"status"`
}

func generateSecureToken(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// <<< НОВАЯ ФУНКЦИЯ: Сборка JSON списка пользователей >>>
func buildUserListJSON() []byte {
	clientsMux.RLock()
	defer clientsMux.RUnlock()
	var userList []UserStatusInfo
	for _, client := range clients {
		if client.Status != StatusOffline && client.username != "" { // Убедимся, что юзернейм не пустой
			userList = append(userList, UserStatusInfo{Username: client.username, Status: client.Status})
		}
	}
	jsonData, err := json.Marshal(userList)
	if err != nil {
		log.Printf("Ошибка кодирования списка пользователей в JSON: %v", err)
		return []byte("[]")
	}
	return jsonData
}

// <<< НОВАЯ ФУНКЦИЯ: Рассылка всем клиентам >>>
func broadcastToAllClients(message []byte, pc net.PacketConn) {
	clientsMux.RLock()
	var recipients []net.Addr
	for _, client := range clients {
		if client.Status != StatusOffline && client.username != "" {
			recipients = append(recipients, client.addr)
		}
	}
	clientsMux.RUnlock()

	for _, rAddr := range recipients {
		_, err := pc.WriteTo(message, rAddr)
		if err != nil {
			log.Printf("Ошибка отправки сообщения (broadcastToAllClients) клиенту %s: %v", rAddr, err)
		}
	}
}

// <<< НОВАЯ ФУНКЦИЯ: Рассылка всем, КРОМЕ отправителя >>>
func broadcastToOthers(message []byte, senderAddr net.Addr, pc net.PacketConn) {
	clientsMux.RLock()
	var recipients []net.Addr
	for _, client := range clients {
		if client.addr.String() != senderAddr.String() && client.Status != StatusOffline && client.username != "" {
			recipients = append(recipients, client.addr)
		}
	}
	clientsMux.RUnlock()

	for _, rAddr := range recipients {
		_, err := pc.WriteTo(message, rAddr)
		if err != nil {
			log.Printf("Ошибка отправки сообщения (broadcastToOthers) клиенту %s: %v", rAddr, err)
		}
	}
}

// Улучшенная функция микширования аудио с улучшенной обработкой буферов
func mixAudio(buffers [][]float32) []float32 {
	if len(buffers) == 0 {
		return nil
	}

	// Проверяем размеры буферов
	frameLen := len(buffers[0])
	for i, buf := range buffers {
		if len(buf) != frameLen {
			log.Printf("Неверный размер буфера %d: %d (ожидалось %d)", i, len(buf), frameLen)
			return nil
		}
	}

	// Создаем выходной буфер
	mixed := make([]float32, frameLen)

	// Вычисляем коэффициент масштабирования для микширования
	scale := float32(1.0) / float32(len(buffers))

	// Микшируем все буферы с масштабированием
	for _, buf := range buffers {
		for i := range buf {
			mixed[i] += buf[i] * scale
		}
	}

	// Применяем компрессию динамического диапазона
	maxAmplitude := float32(0)
	for _, sample := range mixed {
		if abs := float32(math.Abs(float64(sample))); abs > maxAmplitude {
			maxAmplitude = abs
		}
	}

	// Мягкое ограничение и нормализация
	if maxAmplitude > 1.0 {
		// Применяем кривую мягкого ограничения
		for i := range mixed {
			mixed[i] = float32(math.Tanh(float64(mixed[i])))
		}
	}

	return mixed
}

// AudioProcessor обрабатывает аудиопотоки
type AudioProcessor struct {
	sampleRate int
	channels   int
	frameSize  int
	buffers    map[string][]float32
	mutex      sync.RWMutex
}

func NewAudioProcessor() *AudioProcessor {
	return &AudioProcessor{
		sampleRate: sampleRate,
		channels:   channels,
		frameSize:  frameSize,
		buffers:    make(map[string][]float32),
	}
}

func (ap *AudioProcessor) AddBuffer(clientID string, buffer []float32) {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	if len(buffer) != ap.frameSize {
		return
	}
	ap.buffers[clientID] = buffer
}

func (ap *AudioProcessor) RemoveClient(clientID string) {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()
	delete(ap.buffers, clientID)
}

func (ap *AudioProcessor) GetMixedAudioForClient(excludeClientID string) []float32 {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()

	var buffers [][]float32
	for clientID, buffer := range ap.buffers {
		if clientID != excludeClientID {
			buffers = append(buffers, buffer)
		}
	}

	return mixAudio(buffers)
}

func cleanup(pc, voiceConn net.PacketConn) {
	log.Println("Завершение работы сервера...")

	clientsMux.RLock()
	for _, client := range clients {
		if client.username != "" { // Только аутентифицированным
			pc.WriteTo([]byte("SERVER_SHUTDOWN::Сервер завершает работу"), client.addr)
		}
	}
	clientsMux.RUnlock()

	if pc != nil {
		pc.Close()
	}
	if voiceConn != nil {
		voiceConn.Close()
	}
}

// New function to clean up inactive clients
func cleanupInactiveClients(ap *AudioProcessor, pc net.PacketConn) {
	ticker := time.NewTicker(clientTimeout / 2)
	defer ticker.Stop()

	for {
		<-ticker.C
		now := time.Now()

		clientsMux.Lock()
		for _, client := range clients {
			if client.Status == StatusOffline || client.username == "" { // Пропускаем неаутентифицированных или уже оффлайн
				continue
			}
			if client.inVoice {
				timeSinceLastVoiceActivity := now.Sub(client.lastActivity)
				if timeSinceLastVoiceActivity > clientTimeout {
					log.Printf("Отключаем неактивного клиента %s из войса (не было активности %.1f секунд)",
						client.username, timeSinceLastVoiceActivity.Seconds())

					client.inVoice = false
					client.Status = StatusOnline
					ap.RemoveClient(client.username)

					statusUpdateMsg := []byte("STATUS_UPDATE::" + client.username + "::" + StatusOnline)
					go func(msg []byte, targetPc net.PacketConn) {
						broadcastToAllClients(msg, targetPc) // Рассылаем всем, так как это публичное изменение статуса
					}(statusUpdateMsg, pc)
				}
			}
		}
		clientsMux.Unlock()
	}
}

// New function to send heartbeats
func sendHeartbeats(voiceConn net.PacketConn) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	heartbeat := []byte{0} // Single byte heartbeat packet

	for {
		<-ticker.C
		clientsMux.RLock()
		for _, client := range clients {
			if client.inVoice && client.username != "" && client.Status != StatusOffline {
				voiceAddr, err := net.ResolveUDPAddr("udp", client.voiceAddr)
				if err == nil {
					voiceConn.WriteTo(heartbeat, voiceAddr)
				}
			}
		}
		clientsMux.RUnlock()
	}
}

func handleVoiceData(voiceConn net.PacketConn, pc net.PacketConn) {
	buffer := make([]byte, maxPacketSize)
	audioProcessor := NewAudioProcessor()

	log.Println("Обработчик голосовых данных запущен")

	go cleanupInactiveClients(audioProcessor, pc)
	go sendHeartbeats(voiceConn)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Восстановление после паники микшера: %v", r)
				go handleVoiceData(voiceConn, pc)
			}
		}()

		ticker := time.NewTicker(mixInterval)
		defer ticker.Stop()

		for {
			<-ticker.C
			clientsMux.RLock()
			for _, client := range clients {
				if !client.inVoice || client.encoder == nil || client.Status == StatusOffline || client.username == "" {
					continue
				}
				mixed := audioProcessor.GetMixedAudioForClient(client.username)
				if mixed == nil {
					continue
				}
				pcm := make([]int16, len(mixed))
				for i, sample := range mixed {
					pcm[i] = int16(sample * 32767.0)
				}
				encoded := make([]byte, maxPacketSize)
				n, err := client.encoder.Encode(pcm, encoded)
				if err != nil {
					continue
				}
				if n > 0 {
					voiceAddrUDP, errResolve := net.ResolveUDPAddr("udp", client.voiceAddr)
					if errResolve == nil {
						voiceConn.WriteTo(encoded[:n], voiceAddrUDP)
					}
				}
			}
			clientsMux.RUnlock()
		}
	}()

	for {
		n, remoteAddr, err := voiceConn.ReadFrom(buffer)
		if err != nil {
			log.Printf("Error reading voice data: %v", err)
			continue
		}
		clientsMux.Lock()
		var sender *Client
		for _, client := range clients {
			if client.username == "" || client.Status == StatusOffline {
				continue
			}
			if client.voiceAddr == remoteAddr.String() {
				sender = client
				break
			}
			if client.inVoice && strings.Split(client.addr.String(), ":")[0] == strings.Split(remoteAddr.String(), ":")[0] {
				client.voiceAddr = remoteAddr.String()
				sender = client
			}
		}
		if sender == nil || !sender.inVoice || sender.decoder == nil {
			clientsMux.Unlock()
			continue
		}
		sender.lastActivity = time.Now()
		if n == 1 && buffer[0] == 0 {
			clientsMux.Unlock()
			continue
		}
		if n > maxPacketSize {
			clientsMux.Unlock()
			continue
		}
		pcm := make([]int16, frameSize)
		samplesDecoded, err := sender.decoder.Decode(buffer[:n], pcm)
		if err != nil || samplesDecoded != frameSize {
			clientsMux.Unlock()
			continue
		}
		floatPCM := make([]float32, frameSize)
		for i, sample := range pcm {
			floatPCM[i] = float32(sample) / 32767.0
		}
		audioProcessor.AddBuffer(sender.username, floatPCM)
		clientsMux.Unlock()
	}
}

func mainLoop(pc net.PacketConn, voiceConn net.PacketConn, audioProcessor *AudioProcessor) {
	for {
		buffer := make([]byte, 4096)
		n, addr, err := pc.ReadFrom(buffer)
		if err != nil {
			log.Printf("Критическая ошибка чтения из основного сокета: %v. Цикл продолжается.", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		msg := string(buffer[:n])
		clientKey := addr.String()

		parts := strings.SplitN(msg, "::", 3) // Разбираем сообщение по разделителю '::'

		if len(parts) > 0 && strings.TrimSpace(parts[0]) == "LOGIN" {
			if len(parts) == 3 {
				loginUsername := strings.TrimSpace(parts[1])
				loginPassword := strings.TrimSpace(parts[2])

				var proceedWithLogin bool = false
				var isNewUser bool = false

				usersCredentialsMux.Lock() // Блокируем доступ к usersCredentials
				expectedPassword, userExistsInCredentials := usersCredentials[loginUsername]

				if !userExistsInCredentials {
					// Пользователя нет в списке - это новый пользователь (регистрация)
					usersCredentials[loginUsername] = loginPassword
					log.Printf("Новый пользователь '%s' зарегистрирован с адреса %s.", loginUsername, clientKey)
					proceedWithLogin = true
					isNewUser = true
				} else {
					// Пользователь существует, проверяем пароль
					if expectedPassword == loginPassword {
						proceedWithLogin = true
					} else {
						log.Printf("Попытка логина от %s для пользователя %s - ОТКАЗ (неверный пароль)", clientKey, loginUsername)
						pc.WriteTo([]byte("LOGIN_FAILURE::INVALID_CREDENTIALS"), addr)
						proceedWithLogin = false
					}
				}
				usersCredentialsMux.Unlock() // Разблокируем usersCredentialsMux

				if proceedWithLogin {
					authMux.Lock() // Блокируем authMux для работы с сессиями
					if existingSession, loggedIn := activeUserSessions[loginUsername]; loggedIn {
						log.Printf("Пользователь %s уже был залогинен с токеном %s (адрес %s). Инвалидация старой сессии.", loginUsername, existingSession.Token, existingSession.ClientKey)
						delete(activeTokenToUser, existingSession.Token)
						// Уведомление старого клиента и его удаление должно происходить вне authMux Lock,
						// но перед созданием новой сессии для того же юзера. Переместим.
						// Запоминаем старый clientKey, чтобы уведомить после разблокировки authMux.
						oldClientKeyToInvalidate := existingSession.ClientKey
						delete(activeUserSessions, loginUsername) // Удаляем старую сессию немедленно

						clientsMux.Lock()
						if oldClient, ok := clients[oldClientKeyToInvalidate]; ok {
							pc.WriteTo([]byte("ERROR::SESSION_INVALIDATED"), oldClient.addr)
							delete(clients, oldClientKeyToInvalidate)
							log.Printf("Старый клиент %s (%s) удален из активных.", loginUsername, oldClientKeyToInvalidate)
						}
						clientsMux.Unlock()
					}

					token, errToken := generateSecureToken(TokenLength)
					if errToken != nil {
						log.Printf("Ошибка генерации токена для %s: %v", loginUsername, errToken)
						pc.WriteTo([]byte("LOGIN_FAILURE::TOKEN_GENERATION_ERROR"), addr)
						authMux.Unlock()
						continue
					}

					activeUserSessions[loginUsername] = &AuthInfo{Username: loginUsername, ClientKey: clientKey, Token: token, LoginTime: time.Now()}
					activeTokenToUser[token] = loginUsername
					authMux.Unlock()

					decoder, _ := opus.NewDecoder(sampleRate, channels)
					encoder, _ := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
					if encoder != nil {
						encoder.SetBitrate(96000)
						encoder.SetComplexity(10)
						encoder.SetPacketLossPerc(10)
						encoder.SetInBandFEC(true)
					}

					newClient := &Client{
						addr:         addr,
						username:     loginUsername,
						token:        token,
						inVoice:      false,
						voiceAddr:    strings.Split(clientKey, ":")[0] + ":6001",
						decoder:      decoder,
						encoder:      encoder,
						lastActivity: time.Now(),
						active:       true,
						Status:       StatusOnline,
					}
					clientsMux.Lock()
					clients[clientKey] = newClient
					clientsMux.Unlock()

					pc.WriteTo([]byte("LOGIN_SUCCESS::"+token+"::"+loginUsername), addr)
					if isNewUser {
						log.Printf("✨ Новый пользователь %s (%s) успешно аутентифицирован и зарегистрирован. Статус: %s", loginUsername, clientKey, newClient.Status)
					} else {
						log.Printf("✨ Клиент %s (%s) успешно аутентифицирован. Статус: %s", loginUsername, clientKey, newClient.Status)
					}

					userListJSON := buildUserListJSON()
					userListMsgContent := append([]byte("USER_LIST::"), userListJSON...)
					pc.WriteTo(userListMsgContent, newClient.addr)

					selfStatusUpdateMsg := []byte("STATUS_UPDATE::" + newClient.username + "::" + newClient.Status)
					pc.WriteTo(selfStatusUpdateMsg, newClient.addr)

					clientsMux.RLock()
					for _, existingClient := range clients {
						if existingClient.addr.String() != newClient.addr.String() && existingClient.Status != StatusOffline && existingClient.username != "" {
							individualStatusUpdateMsg := []byte("STATUS_UPDATE::" + existingClient.username + "::" + existingClient.Status)
							pc.WriteTo(individualStatusUpdateMsg, newClient.addr)
							statusUpdateForOthersMsg := []byte("STATUS_UPDATE::" + newClient.username + "::" + newClient.Status)
							pc.WriteTo(statusUpdateForOthersMsg, existingClient.addr)
							joinMsgForOthers := []byte(newClient.username + " joined the chat")
							pc.WriteTo(joinMsgForOthers, existingClient.addr)
						}
					}
					clientsMux.RUnlock()
				}
			} else {
				log.Printf("Некорректный формат LOGIN сообщения от %s: %s", clientKey, msg)
				pc.WriteTo([]byte("LOGIN_FAILURE::INVALID_FORMAT"), addr)
			}
			continue
		}

		clientsMux.RLock()
		client, clientAuthenticatedAndExists := clients[clientKey]
		clientsMux.RUnlock()

		if !clientAuthenticatedAndExists || client.username == "" || client.Status == StatusOffline {
			continue
		}

		if strings.TrimSpace(msg) == "/exit" {
			log.Printf("🚪 Клиент %s (%s) отправил /exit. Отключаю.", client.username, client.addr)
			clientsMux.Lock()
			client.Status = StatusOffline
			if client.inVoice {
				client.inVoice = false
				audioProcessor.RemoveClient(client.username)
			}
			delete(clients, clientKey)
			clientsMux.Unlock()

			authMux.Lock()
			delete(activeTokenToUser, client.token)
			delete(activeUserSessions, client.username)
			authMux.Unlock()
			log.Printf("Токен %s для пользователя %s инвалидирован.", client.token, client.username)

			statusUpdateMsg := []byte("STATUS_UPDATE::" + client.username + "::" + StatusOffline)
			go broadcastToAllClients(statusUpdateMsg, pc)
			continue
		}

		clientsMux.Lock()
		client, clientStillExists := clients[clientKey]
		if !clientStillExists || client.username == "" || client.Status == StatusOffline {
			clientsMux.Unlock()
			continue
		}

		if msg == "VOICE_CONNECT" {
			client.inVoice = true
			client.lastActivity = time.Now()
			client.Status = StatusInVoice
			log.Printf("🎤 %s (%s) вошёл в голосовой чат. Статус: %s", client.username, clientKey, client.Status)
			clientsMux.Unlock()
			statusUpdateMsg := []byte("STATUS_UPDATE::" + client.username + "::" + client.Status)
			go broadcastToAllClients(statusUpdateMsg, pc)
			chatNotification := []byte(client.username + " подключился к голосовому чату")
			go broadcastToAllClients(chatNotification, pc)
			continue
		}

		if msg == "VOICE_DISCONNECT" {
			client.inVoice = false
			client.Status = StatusOnline
			audioProcessor.RemoveClient(client.username)
			log.Printf("🔇 %s (%s) вышел из голосового чата. Статус: %s", client.username, clientKey, client.Status)
			clientsMux.Unlock()
			statusUpdateMsg := []byte("STATUS_UPDATE::" + client.username + "::" + client.Status)
			go broadcastToAllClients(statusUpdateMsg, pc)
			chatNotification := []byte(client.username + " отключился от голосового чата")
			go broadcastToAllClients(chatNotification, pc)
			continue
		}

		chatMessage := []byte("[" + client.username + "]: " + msg)
		clientsMux.Unlock()
		go broadcastToOthers(chatMessage, addr, pc)
	}
}

func main() {
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
	defer cleanup(pc, voiceConn)

	log.Println("Сервер запущен на порту :6000")
	log.Println("Голосовой сервер запущен на порту :6001")
	audioProcessor := NewAudioProcessor()
	go handleVoiceData(voiceConn, pc)
	go func() {
		<-sigChan
		log.Println("Получен сигнал завершения, очистка...")
		shutdownMsg := []byte("SERVER_SHUTDOWN::Сервер выключается")
		broadcastToAllClients(shutdownMsg, pc)
		time.Sleep(200 * time.Millisecond)
		cleanup(pc, voiceConn)
		os.Exit(0)
	}()
	mainLoop(pc, voiceConn, audioProcessor)
}
