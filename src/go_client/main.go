package main

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gordonklaus/portaudio"
	"github.com/hraban/opus"
)

const (
	sampleRate       = 48000
	channels         = 1
	frameSize        = 960  // 20мс при 48кГц
	maxBytes         = 1275 // Максимальный размер пакета Opus
	jitterBufferSize = 20   // 400мс буфер для большей стабильности
	noiseThreshold   = 0.02 // Порог шумоподавления (возможно, стоит также пересмотреть)

	// Константы обработки аудио
	vadThreshold         = 0.002 // Порог определения голосовой активности (было 0.005)
	softGateFactor       = 0.3   // Коэффициент ослабления для мягкого гейта (было 0.1)
	gainFactor           = 1.2   // Небольшое усиление для слабых сигналов
	compressionThreshold = 0.8   // Порог для компрессии динамического диапазона
	vadHangoverTimeMs    = 250   // Время удержания VAD в миллисекундах (было 150)

	// Константы буферизации
	inputBufferMultiplier = 3 // Размер входного буфера относительно frameSize
	minBufferThreshold    = 7 // Минимальное количество фреймов для начала воспроизведения
)

var (
	paInitialized     bool = false
	vadHangoverFrames      = vadHangoverTimeMs / 20
)

type AudioState struct {
	inputStream  *portaudio.Stream
	outputStream *portaudio.Stream
	buffer       *AudioBuffer
	lastLogTime  time.Time
}

type AudioBuffer struct {
	InputBuffer   []float32
	OutputBuffer  []float32
	OpusInputBuf  []int16
	OpusOutputBuf []int16
	Encoder       *opus.Encoder
	Decoder       *opus.Decoder
	JitterBuffer  [][]float32
}

// JitterBuffer управляет временем пакетов аудио
type JitterBuffer struct {
	buffer    [][]float32
	maxSize   int
	frameSize int
	mutex     sync.RWMutex
}

func NewJitterBuffer(size int, frameSize int) *JitterBuffer {
	return &JitterBuffer{
		buffer:    make([][]float32, 0, size),
		maxSize:   size,
		frameSize: frameSize,
	}
}

func (jb *JitterBuffer) Add(data []float32) {
	jb.mutex.Lock()
	defer jb.mutex.Unlock()
	if len(jb.buffer) >= jb.maxSize {
		jb.buffer = jb.buffer[1:]
	}
	frame := make([]float32, len(data))
	copy(frame, data)
	jb.buffer = append(jb.buffer, frame)
}

func (jb *JitterBuffer) Get() []float32 {
	jb.mutex.Lock()
	defer jb.mutex.Unlock()
	if len(jb.buffer) == 0 {
		return make([]float32, jb.frameSize)
	}
	frame := jb.buffer[0]
	jb.buffer = jb.buffer[1:]
	return frame
}

func (jb *JitterBuffer) Available() int {
	jb.mutex.RLock()
	defer jb.mutex.RUnlock()
	return len(jb.buffer)
}

// Enhanced audio processing
type AudioProcessor struct {
	vadEnabled           bool
	lastVadState         bool
	energyThreshold      float32
	smoothingFactor      float32
	noiseFloor           float32
	framesSinceLastVoice int
	vadHangoverFrames    int
}

func NewAudioProcessor() *AudioProcessor {
	return &AudioProcessor{
		vadEnabled:           true,
		lastVadState:         false,
		energyThreshold:      vadThreshold,
		smoothingFactor:      0.95,
		noiseFloor:           0.001,
		framesSinceLastVoice: 0,
		vadHangoverFrames:    vadHangoverFrames,
	}
}

func (ap *AudioProcessor) ProcessInput(buffer []float32) []float32 {
	processed := make([]float32, len(buffer))
	copy(processed, buffer)
	energy := float32(0)
	for _, sample := range processed {
		energy += sample * sample
	}
	energy /= float32(len(processed))
	if energy > ap.energyThreshold {
		ap.framesSinceLastVoice = 0
	} else {
		ap.framesSinceLastVoice++
	}
	if ap.framesSinceLastVoice > ap.vadHangoverFrames {
		for i := range processed {
			processed[i] *= softGateFactor
		}
		return processed
	}
	applyHighPassFilter(processed)
	ap.applyCompression(processed)
	normalizeAmplitude(processed)
	return processed
}

func (ap *AudioProcessor) applyCompression(buffer []float32) {
	peak := float32(0)
	for _, sample := range buffer {
		if abs := float32(math.Abs(float64(sample))); abs > peak {
			peak = abs
		}
	}
	if peak > compressionThreshold {
		ratio := compressionThreshold / peak
		for i := range buffer {
			buffer[i] *= ratio
		}
	}
}

func float32ToInt16(float32Buf []float32) []int16 {
	int16Buf := make([]int16, len(float32Buf))
	for i, f := range float32Buf {
		s := f * 32767.0
		if s > 32767.0 {
			s = 32767.0
		} else if s < -32767.0 {
			s = -32767.0
		}
		int16Buf[i] = int16(s)
	}
	return int16Buf
}

func int16ToFloat32(int16Buf []int16) []float32 {
	float32Buf := make([]float32, len(int16Buf))
	for i, s := range int16Buf {
		float32Buf[i] = float32(s) / 32767.0
	}
	return float32Buf
}

func initPortAudio() error {
	if !paInitialized {
		if err := portaudio.Initialize(); err != nil {
			return fmt.Errorf("failed to initialize portaudio: %v", err)
		}
		paInitialized = true
	}
	return nil
}

func terminatePortAudio() {
	if paInitialized {
		portaudio.Terminate()
		paInitialized = false
	}
}

func applyHighPassFilter(buf []float32) {
	rc := 1.0 / (2 * math.Pi * 100.0)
	dt := 1.0 / float64(sampleRate)
	alpha := float32(rc / (rc + dt))
	prev := float32(0)
	for i := range buf {
		buf[i] = alpha * (prev + buf[i] - prev)
		prev = buf[i]
	}
}

func normalizeAmplitude(buf []float32) {
	max := float32(0)
	for _, s := range buf {
		if abs := float32(math.Abs(float64(s))); abs > max {
			max = abs
		}
	}
	targetMinPeak := float32(0.2)
	targetMaxPeak := float32(0.8)
	maxBoostFactor := float32(2.5)
	if max > 0.0001 {
		if max < targetMinPeak {
			scale := targetMinPeak / max
			if scale > maxBoostFactor {
				scale = maxBoostFactor
			}
			for i := range buf {
				buf[i] *= scale
			}
		} else if max > targetMaxPeak {
			scale := targetMaxPeak / max
			for i := range buf {
				buf[i] *= scale
			}
		}
	}
}

func initAudio() (*AudioBuffer, error) {
	encoder, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("failed to create encoder: %v", err)
	}
	encoder.SetBitrate(32000)
	encoder.SetComplexity(8)
	encoder.SetInBandFEC(true)
	encoder.SetPacketLossPerc(30)
	decoder, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		return nil, fmt.Errorf("failed to create decoder: %v", err)
	}
	return &AudioBuffer{
		InputBuffer:   make([]float32, frameSize),
		OutputBuffer:  make([]float32, frameSize),
		OpusInputBuf:  make([]int16, frameSize),
		OpusOutputBuf: make([]int16, frameSize),
		Encoder:       encoder,
		Decoder:       decoder,
		JitterBuffer:  make([][]float32, 0, jitterBufferSize),
	}, nil
}

func startAudioStream(conn *net.UDPConn, buffer *AudioBuffer, stopCh chan struct{}, wg *sync.WaitGroup) error {
	conn.SetWriteBuffer(32768)
	conn.SetReadBuffer(32768)
	audioState := &AudioState{
		buffer:      buffer,
		lastLogTime: time.Now(),
	}
	remoteAddr := conn.RemoteAddr().(*net.UDPAddr)
	fmt.Printf("Голосовое соединение установлено с %s\n", remoteAddr.String())
	fmt.Println("Инициализация аудиопотоков...")
	defaultOutputDevice, err := portaudio.DefaultOutputDevice()
	if err != nil {
		return fmt.Errorf("ошибка получения устройства вывода по умолчанию: %v", err)
	}
	fmt.Printf("Используется устройство вывода: %s\n", defaultOutputDevice.Name)
	defaultInputDevice, err := portaudio.DefaultInputDevice()
	if err != nil {
		return fmt.Errorf("ошибка получения устройства ввода по умолчанию: %v", err)
	}
	fmt.Printf("Используется устройство ввода: %s\n", defaultInputDevice.Name)
	inputStreamParams := portaudio.StreamParameters{
		Input: portaudio.StreamDeviceParameters{
			Device:   defaultInputDevice,
			Channels: channels,
			Latency:  defaultInputDevice.DefaultLowInputLatency,
		},
		Output: portaudio.StreamDeviceParameters{
			Channels: 0,
		},
		SampleRate:      float64(sampleRate),
		FramesPerBuffer: frameSize,
	}
	audioState.inputStream, err = portaudio.OpenStream(inputStreamParams, buffer.InputBuffer)
	if err != nil {
		return fmt.Errorf("failed to open input stream: %v", err)
	}
	outputStreamParams := portaudio.StreamParameters{
		Input: portaudio.StreamDeviceParameters{
			Channels: 0,
		},
		Output: portaudio.StreamDeviceParameters{
			Device:   defaultOutputDevice,
			Channels: channels,
			Latency:  defaultOutputDevice.DefaultLowOutputLatency,
		},
		SampleRate:      float64(sampleRate),
		FramesPerBuffer: frameSize,
	}
	audioState.outputStream, err = portaudio.OpenStream(outputStreamParams, buffer.OutputBuffer)
	if err != nil {
		audioState.inputStream.Close()
		return fmt.Errorf("failed to open output stream: %v", err)
	}
	if err := audioState.inputStream.Start(); err != nil {
		audioState.inputStream.Close()
		audioState.outputStream.Close()
		return fmt.Errorf("failed to start input stream: %v", err)
	}
	if err := audioState.outputStream.Start(); err != nil {
		audioState.inputStream.Stop()
		audioState.inputStream.Close()
		audioState.outputStream.Close()
		return fmt.Errorf("failed to start output stream: %v", err)
	}
	fmt.Println("✅ Аудиопотоки инициализированы")
	processor := NewAudioProcessor()
	jitterBuffer := NewJitterBuffer(jitterBufferSize, frameSize)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer audioState.inputStream.Stop()
		defer audioState.inputStream.Close()
		inputAccumulator := make([]float32, 0, frameSize*inputBufferMultiplier)
		encodedData := make([]byte, maxBytes)
		for {
			select {
			case <-stopCh:
				return
			default:
				err := audioState.inputStream.Read()
				if err != nil {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				inputAccumulator = append(inputAccumulator, buffer.InputBuffer...)
				for len(inputAccumulator) >= frameSize {
					copy(buffer.InputBuffer, inputAccumulator[:frameSize])
					inputAccumulator = append(inputAccumulator[:0], inputAccumulator[frameSize:]...)
					processed := processor.ProcessInput(buffer.InputBuffer)
					opusData := float32ToInt16(processed)
					n, err := buffer.Encoder.Encode(opusData, encodedData)
					if err == nil && n > 0 && n <= maxBytes {
						conn.Write(encodedData[:n])
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		heartbeat := []byte{0}
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				conn.Write(heartbeat)
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer audioState.outputStream.Stop()
		defer audioState.outputStream.Close()
		receiveBuf := make([]byte, maxBytes)
		for {
			select {
			case <-stopCh:
				return
			default:
				conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				n, _, err := conn.ReadFromUDP(receiveBuf)
				if err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue
					}
					continue
				}
				if n == 1 && receiveBuf[0] == 0 {
					continue
				}
				samplesRead, err := buffer.Decoder.Decode(receiveBuf[:n], buffer.OpusOutputBuf)
				if err != nil || samplesRead != frameSize {
					continue
				}
				audioFloat := int16ToFloat32(buffer.OpusOutputBuf)
				processed := processor.ProcessInput(audioFloat)
				jitterBuffer.Add(processed)
				if jitterBuffer.Available() >= minBufferThreshold {
					playbackData := jitterBuffer.Get()
					copy(buffer.OutputBuffer, playbackData)
					err = audioState.outputStream.Write()
					if err != nil {
						continue
					}
				}
			}
		}
	}()
	return nil
}

type AppController struct {
	serverIP        string
	username        string // Будет установлен после успешного LOGIN_SUCCESS
	password        string // Для отправки на сервер
	mainConn        *net.UDPConn
	voiceConn       *net.UDPConn
	stopAudio       chan struct{}
	audioWg         sync.WaitGroup
	isAuthenticated bool      // Флаг успешной аутентификации
	exitSignal      chan bool // Канал для сигнала о завершении из горутины чтения
}

func NewAppController(serverIP, username, password string, mainConn *net.UDPConn) *AppController {
	return &AppController{
		serverIP:        serverIP,
		username:        username, // Изначально используется для LOGIN
		password:        password,
		mainConn:        mainConn,
		isAuthenticated: false,
		exitSignal:      make(chan bool, 1),
	}
}

func (ac *AppController) Authenticate() bool {
	loginMessage := fmt.Sprintf("LOGIN::%s::%s", ac.username, ac.password)
	_, err := ac.mainConn.Write([]byte(loginMessage))
	if err != nil {
		fmt.Printf("Ошибка отправки LOGIN: %v\n", err)
		return false
	}
	// Ожидание ответа LOGIN_SUCCESS или LOGIN_FAILURE будет в основной горутине чтения
	// Здесь мы просто отправляем запрос. Флаг isAuthenticated установится в main.
	return true // Возвращаем true, если отправка успешна, а не аутентификация
}

func (ac *AppController) HandleVoiceCommand() {
	if !ac.isAuthenticated {
		fmt.Println("Необходимо сначала успешно войти в чат для использования голосовых команд.")
		return
	}
	if ac.voiceConn == nil {
		if !paInitialized {
			if err := initPortAudio(); err != nil {
				fmt.Printf("Ошибка инициализации PortAudio: %v\n", err)
				return
			}
		}
		voiceAddr, err := net.ResolveUDPAddr("udp", ac.serverIP+":6001")
		if err != nil {
			fmt.Println("Ошибка разрешения голосового адреса:", err)
			return
		}
		ac.voiceConn, err = net.DialUDP("udp", nil, voiceAddr)
		if err != nil {
			fmt.Println("Ошибка подключения к голосовому чату:", err)
			ac.voiceConn = nil
			return
		}
		audioBuffer, err := initAudio()
		if err != nil {
			fmt.Printf("Ошибка инициализации аудио: %v\n", err)
			ac.voiceConn.Close()
			ac.voiceConn = nil
			return
		}
		ac.stopAudio = make(chan struct{})
		err = startAudioStream(ac.voiceConn, audioBuffer, ac.stopAudio, &ac.audioWg)
		if err != nil {
			fmt.Printf("Ошибка запуска аудио потока: %v\n", err)
			ac.voiceConn.Close()
			ac.voiceConn = nil
			close(ac.stopAudio)
			return
		}
		ac.mainConn.Write([]byte("VOICE_CONNECT"))
		fmt.Println("Вы подключились к голосовому чату")
	} else {
		fmt.Println("Вы уже подключены к голосовому чату")
	}
}

func (ac *AppController) HandleLeaveCommand() {
	if !ac.isAuthenticated {
		fmt.Println("Необходимо сначала успешно войти в чат.")
		return
	}
	if ac.voiceConn != nil {
		close(ac.stopAudio)
		ac.audioWg.Wait()
		ac.mainConn.Write([]byte("VOICE_DISCONNECT"))
		ac.voiceConn.Close()
		ac.voiceConn = nil
		fmt.Println("Вы отключились от голосового чата")
	} else {
		fmt.Println("Вы не подключены к голосовому чату")
	}
}

func (ac *AppController) HandleExitCommand() bool {
	if ac.voiceConn != nil {
		close(ac.stopAudio)
		ac.audioWg.Wait()
		// VOICE_DISCONNECT отправится автоматически при выходе, если сервер обрабатывает закрытие соединения
		// ac.mainConn.Write([]byte("VOICE_DISCONNECT")) // Можно оставить для явности
		ac.voiceConn.Close()
		ac.voiceConn = nil
	}
	// Отправляем /exit только если мы аутентифицированы
	// Если не аутентифицированы, просто выходим из клиента
	if ac.isAuthenticated {
		_, err := ac.mainConn.Write([]byte("/exit"))
		if err != nil {
			// fmt.Printf("Ошибка отправки /exit: %v\n", err) // Можно логировать, но все равно выходим
		}
	}
	return true
}

func main() {
	if err := initPortAudio(); err != nil {
		fmt.Printf("Ошибка инициализации PortAudio: %v\n", err)
		return
	}
	defer terminatePortAudio()

	if len(os.Args) < 4 { // Ожидаем server_ip username password
		fmt.Println("Использование: client.exe <server_ip> <username> <password>")
		return
	}
	serverIP := os.Args[1]
	username := os.Args[2]
	password := os.Args[3] // Новый аргумент

	if serverIP == "" {
		fmt.Println("IP сервера не может быть пустым.")
		return
	}
	if username == "" {
		fmt.Println("Имя пользователя не может быть пустым.")
		return
	}
	if password == "" {
		fmt.Println("Пароль не может быть пустым.")
		return
	}

	fmt.Printf("Попытка подключения к серверу %s с именем %s...\n", serverIP, username)

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

	appController := NewAppController(serverIP, username, password, conn)

	// Горутина для чтения входящих сообщений от сервера
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, _, err := conn.ReadFromUDP(buffer)
			if err != nil {
				// Если ошибка чтения, скорее всего соединение разорвано или сервер недоступен
				if !appController.isAuthenticated {
					fmt.Println("Не удалось подключиться к серверу или сервер отклонил соединение.")
				} else {
					// fmt.Println("Ошибка чтения с сервера:", err) // Можно раскомментировать для отладки
				}
				appController.exitSignal <- true // Сигнал для завершения main
				return
			}
			serverMessage := string(buffer[:n])

			if strings.HasPrefix(serverMessage, "LOGIN_SUCCESS::") {
				parts := strings.SplitN(serverMessage, "::", 3)
				if len(parts) == 3 {
					// token := parts[1] // Токен пока не используется активно на клиенте, но можно сохранить
					serverUsername := parts[2]
					appController.username = serverUsername // Обновляем имя пользователя с тем, что прислал сервер
					appController.isAuthenticated = true
					fmt.Printf("Успешный вход как %s.\n", appController.username)
					// После успешного логина выводим команды, как и раньше
					fmt.Println("\nДоступные команды:")
					fmt.Println("/voice - подключиться к голосовому чату")
					fmt.Println("/leave - отключиться от голосового чата")
					fmt.Println("/exit - выйти из чата")
					fmt.Println("Любой другой текст будет отправлен как сообщение")
					fmt.Print("> ")
				}
			} else if strings.HasPrefix(serverMessage, "LOGIN_FAILURE::") {
				reason := strings.TrimPrefix(serverMessage, "LOGIN_FAILURE::")
				fmt.Printf("Ошибка входа: %s\n", reason)
				appController.exitSignal <- true // Сигнал для завершения main, так как вход не удался
				return                           // Завершаем горутину чтения
			} else if strings.HasPrefix(serverMessage, "ERROR::SESSION_INVALIDATED") {
				fmt.Println("Ваша сессия была завершена, так как выполнен вход с этим именем пользователя с другого места.")
				appController.exitSignal <- true // Сигнал для завершения main
				return
			} else if !appController.isAuthenticated {
				// Если не аутентифицированы, но пришло что-то кроме LOGIN_*, это странно.
				// Можно логировать или игнорировать.
				// fmt.Printf("Получено сообщение до аутентификации: %s\n", serverMessage)
				continue // Пропускаем, если не LOGIN_SUCCESS и не LOGIN_FAILURE
			} else {
				// Обработка сообщений после успешной аутентификации
				if strings.HasPrefix(serverMessage, "USER_LIST::") {
					jsonPart := strings.TrimPrefix(serverMessage, "USER_LIST::")
					fmt.Printf("USER_LIST::%s\n", jsonPart)
				} else if strings.HasPrefix(serverMessage, "STATUS_UPDATE::") {
					// Формат SERVER_USER_STATUS_UPDATE::user::status был заменен на STATUS_UPDATE::user::status
					// прямо из сервера, поэтому старый SERVER_USER_STATUS_UPDATE уже не нужен
					fmt.Printf("%s\n", serverMessage) // Просто передаем как есть
				} else if strings.HasPrefix(serverMessage, "SERVER_USER_JOINED::") { // Это сообщение больше не должно приходить, т.к. есть STATUS_UPDATE
					userNameJoined := strings.TrimPrefix(serverMessage, "SERVER_USER_JOINED::")
					fmt.Printf("STATUS_UPDATE::%s::online\n", userNameJoined)
					fmt.Printf("%s joined the chat\n", userNameJoined)
				} else if strings.HasPrefix(serverMessage, "SERVER_USER_LEFT::") { // Это сообщение больше не должно приходить
					userNameLeft := strings.TrimPrefix(serverMessage, "SERVER_USER_LEFT::")
					fmt.Printf("STATUS_UPDATE::%s::offline\n", userNameLeft)
					fmt.Printf("%s left the chat\n", userNameLeft)
				} else {
					fmt.Printf("%s\n", serverMessage)
				}
			}
		}
	}()

	// Попытка аутентификации
	if !appController.Authenticate() {
		// Если отправка LOGIN сообщения не удалась, выходим
		return
	}

	// Основной цикл для чтения ввода пользователя или ожидания сигнала на выход
	scanner := bufio.NewScanner(os.Stdin)
	for {
		// Используем select для неблокирующего чтения из Stdin и канала exitSignal
		// Этот подход сложнее для простого консольного ввода, лучше использовать блокирующий Scan
		// и прерывать его при получении сигнала.

		// Канал для чтения строки из Stdin
		inputLineChan := make(chan string)
		scanErrChan := make(chan error)

		go func() {
			if scanner.Scan() {
				inputLineChan <- scanner.Text()
			} else {
				scanErrChan <- scanner.Err()
			}
		}()

		select {
		case <-appController.exitSignal:
			// fmt.Println("Получен сигнал на выход из приложения.")
			return // Завершаем main

		case text := <-inputLineChan:
			if !appController.isAuthenticated {
				// Если еще не аутентифицированы, то не даем отправлять команды,
				// ждем ответа от сервера LOGIN_SUCCESS/FAILURE
				// Это состояние не должно длиться долго.
				// fmt.Print("> ") // Можно снова показать приглашение, если нужно
				continue
			}

			shouldExit := false
			switch text {
			case "/voice":
				appController.HandleVoiceCommand()
			case "/leave":
				appController.HandleLeaveCommand()
			case "/exit":
				shouldExit = appController.HandleExitCommand()
			default:
				// Отправляем обычное сообщение. Имя пользователя теперь берется из appController.username,
				// которое было установлено сервером.
				// Сервер сам добавит имя пользователя к сообщению, если это предусмотрено логикой сервера.
				// Клиент просто отправляет текст.
				// Для совместимости с текущим сервером, который ожидает "[username]: text" для обычных сообщений:
				// Это можно убрать, если сервер будет сам форматировать сообщение от аутентифицированного юзера.
				// Пока оставим, как было, но с ac.username
				_, err := appController.mainConn.Write([]byte("[" + appController.username + "]: " + text))
				if err != nil {
					fmt.Println("Ошибка отправки:", err)
					// shouldExit = true // Решаем, выходить ли при ошибке отправки
				}
			}
			if shouldExit {
				// fmt.Println("Завершение работы клиента по команде /exit...")
				return // Завершаем main
			}
			if appController.isAuthenticated { // Показываем приглашение только после успешного входа
				fmt.Print("> ")
			}

		case err := <-scanErrChan:
			if err != nil {
				// fmt.Printf("Ошибка сканирования ввода: %v\n", err)
			}
			// Если сканер завершился (например, EOF), то выходим
			return // Завершаем main
		}
	}
}
