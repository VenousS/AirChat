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
	sampleRate = 48000
	channels   = 1
	frameSize  = 960   // 20ms at 48kHz
	maxBytes   = 12500 // Увеличиваем размер буфера для лучшего качества
)

var (
	voiceConn     *net.UDPConn
	stopAudio     chan struct{}
	audioWg       sync.WaitGroup
	paInitialized bool = false
	debugMode     bool = true // Включаем режим отладки
)

type AudioState struct {
	inputStream     *portaudio.Stream
	outputStream    *portaudio.Stream
	buffer          *AudioBuffer
	lastLogTime     time.Time
	packetsReceived int
	bytesReceived   int64
	samplesDecoded  int
	samplesPlayed   int
}

type AudioBuffer struct {
	InputBuffer   []float32
	OutputBuffer  []float32
	OpusInputBuf  []int16
	OpusOutputBuf []int16
	Encoder       *opus.Encoder
	Decoder       *opus.Decoder
}

func float32ToInt16(float32Buf []float32) []int16 {
	int16Buf := make([]int16, len(float32Buf))
	for i, f := range float32Buf {
		// Convert float32 [-1.0,1.0] to int16
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
		// Convert int16 to float32 [-1.0,1.0]
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

func initAudio() (*AudioBuffer, error) {
	encoder, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("failed to create encoder: %v", err)
	}

	// Настраиваем параметры кодека для лучшего качества
	encoder.SetBitrate(96000)     // 64 kbps для лучшего качества голоса
	encoder.SetComplexity(10)     // Максимальное качество кодирования
	encoder.SetInBandFEC(true)    // Включаем коррекцию ошибок
	encoder.SetPacketLossPerc(10) // Ожидаем 10% потерь пакетов

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
	}, nil
}

func startAudioStream(conn *net.UDPConn, buffer *AudioBuffer) error {
	audioState := &AudioState{
		buffer:      buffer,
		lastLogTime: time.Now(),
	}

	// Проверяем UDP соединение
	remoteAddr := conn.RemoteAddr().(*net.UDPAddr)
	fmt.Printf("Установлено голосовое соединение с %s\n", remoteAddr.String())

	fmt.Println("Инициализация аудио потоков...")

	// Получаем информацию о доступных устройствах
	devices, err := portaudio.Devices()
	if err != nil {
		return fmt.Errorf("не удалось получить список аудио устройств: %v", err)
	}

	// Выводим информацию об устройствах
	fmt.Println("\nДоступные аудио устройства:")
	for _, dev := range devices {
		if dev.MaxOutputChannels > 0 {
			fmt.Printf("Выход: %s (задержка: %v)\n", dev.Name, dev.DefaultLowOutputLatency)
		}
		if dev.MaxInputChannels > 0 {
			fmt.Printf("Вход: %s (задержка: %v)\n", dev.Name, dev.DefaultLowInputLatency)
		}
	}

	defaultOutputDevice, err := portaudio.DefaultOutputDevice()
	if err != nil {
		return fmt.Errorf("не удалось получить устройство вывода по умолчанию: %v", err)
	}
	fmt.Printf("\nИспользуется устройство вывода: %s\n", defaultOutputDevice.Name)

	defaultInputDevice, err := portaudio.DefaultInputDevice()
	if err != nil {
		return fmt.Errorf("не удалось получить устройство ввода по умолчанию: %v", err)
	}
	fmt.Printf("Используется устройство ввода: %s\n", defaultInputDevice.Name)

	// Открываем входной поток (микрофон)
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

	// Открываем выходной поток (динамики)
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

	// Проверяем поддержку формата
	err = portaudio.IsFormatSupported(outputStreamParams, buffer.OutputBuffer)
	if err != nil {
		fmt.Printf("❌ Формат аудио не поддерживается: %v\n", err)
		return fmt.Errorf("unsupported audio format: %v", err)
	}
	fmt.Println("✅ Формат аудио поддерживается")

	audioState.outputStream, err = portaudio.OpenStream(outputStreamParams, buffer.OutputBuffer)
	if err != nil {
		audioState.inputStream.Close()
		return fmt.Errorf("failed to open output stream: %v", err)
	}
	fmt.Printf("✅ Выходной поток успешно открыт (устройство: %s)\n", defaultOutputDevice.Name)

	// Проверяем информацию о потоке
	streamInfo := audioState.outputStream.Info()
	fmt.Printf("ℹ️ Информация о потоке:\n")
	fmt.Printf("   Выходная задержка: %v\n", streamInfo.OutputLatency)
	fmt.Printf("   Частота дискретизации: %.0f Гц\n", streamInfo.SampleRate)

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

	fmt.Println("✅ Выходной поток успешно запущен")

	// Проверяем загрузку CPU
	time.Sleep(100 * time.Millisecond) // Даем потоку время инициализироваться
	cpuLoad := audioState.outputStream.CpuLoad()
	fmt.Printf("ℹ️ Загрузка CPU потоком: %.1f%%\n", cpuLoad*100)

	// Воспроизводим тестовый звук с нарастающей громкостью
	fmt.Println("Воспроизведение тестового звука...")
	for i := range buffer.OutputBuffer {
		t := float64(i) / float64(sampleRate)
		amplitude := float32(0.5 * (1.0 - math.Exp(-t*5.0))) // Плавное нарастание громкости
		buffer.OutputBuffer[i] = amplitude * float32(math.Sin(2.0*math.Pi*440.0*t))
	}

	// Проверяем содержимое буфера перед воспроизведением
	maxAmplitude := float32(0)
	for _, sample := range buffer.OutputBuffer {
		amplitude := float32(math.Abs(float64(sample)))
		if amplitude > maxAmplitude {
			maxAmplitude = amplitude
		}
	}
	fmt.Printf("Максимальная амплитуда тестового сигнала: %.4f\n", maxAmplitude)

	err = audioState.outputStream.Write()
	if err != nil {
		fmt.Printf("Ошибка воспроизведения тестового звука: %v\n", err)
	} else {
		fmt.Println("Тестовый звук отправлен на воспроизведение")
	}

	// Даем время на воспроизведение тестового звука
	time.Sleep(500 * time.Millisecond)

	// Буфер для закодированных данных
	encodedData := make([]byte, maxBytes)

	// Запускаем горутину для записи звука
	audioWg.Add(1)
	go func() {
		defer audioWg.Done()
		defer audioState.inputStream.Stop()
		defer audioState.inputStream.Close()

		fmt.Println("Запущена горутина записи звука")
		var lastPrintTime time.Time
		sampleCount := 0
		bytesSent := 0

		for {
			select {
			case <-stopAudio:
				fmt.Println("Остановка записи звука")
				return
			default:
				// Читаем звук с микрофона
				err := audioState.inputStream.Read()
				if err != nil {
					fmt.Printf("Error reading from input stream: %v\n", err)
					continue
				}

				// Проверяем, есть ли звук в буфере
				hasSound := false
				maxInputAmplitude := float32(0)
				for _, sample := range buffer.InputBuffer {
					amplitude := float32(math.Abs(float64(sample)))
					if amplitude > maxInputAmplitude {
						maxInputAmplitude = amplitude
					}
					if amplitude > 0.01 {
						hasSound = true
					}
				}

				// Усиливаем входной сигнал если он слишком тихий
				if maxInputAmplitude > 0 && maxInputAmplitude < 0.1 {
					gain := 0.3 / maxInputAmplitude
					if gain > 10.0 {
						gain = 10.0
					}
					for i := range buffer.InputBuffer {
						buffer.InputBuffer[i] *= gain
					}
				}

				if hasSound {
					sampleCount++
					// Конвертируем float32 в int16 для Opus
					buffer.OpusInputBuf = float32ToInt16(buffer.InputBuffer)

					// Кодируем звук
					n, err := buffer.Encoder.Encode(buffer.OpusInputBuf, encodedData)
					if err != nil {
						fmt.Printf("Error encoding audio: %v\n", err)
						continue
					}

					// Отправляем закодированные данные
					bytesWritten, err := conn.Write(encodedData[:n])
					if err != nil {
						fmt.Printf("Error sending audio data: %v\n", err)
						continue
					}
					bytesSent += bytesWritten

					if time.Since(lastPrintTime) > time.Second {
						fmt.Printf("Записано %d сэмплов с звуком (макс. амплитуда: %.4f), отправлено %d байт за последнюю секунду\n",
							sampleCount, maxInputAmplitude, bytesSent)
						sampleCount = 0
						bytesSent = 0
						lastPrintTime = time.Now()
					}
				}
			}
		}
	}()

	// Запускаем горутину для воспроизведения звука
	audioWg.Add(1)
	go func() {
		defer audioWg.Done()
		defer audioState.outputStream.Stop()
		defer audioState.outputStream.Close()

		fmt.Println("Запущена горутина воспроизведения звука")

		receiveBuf := make([]byte, maxBytes)
		for {
			select {
			case <-stopAudio:
				fmt.Println("Остановка воспроизведения звука")
				return
			default:
				// Устанавливаем таймаут чтения
				conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

				// Получаем звуковые данные
				n, _, err := conn.ReadFromUDP(receiveBuf)
				if err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue
					}
					fmt.Printf("Error receiving audio data: %v\n", err)
					continue
				}

				fmt.Printf("Получен UDP-пакет размером %d байт\n", n)

				audioState.packetsReceived++
				audioState.bytesReceived += int64(n)

				// Декодируем полученные данные
				samplesRead, err := buffer.Decoder.Decode(receiveBuf[:n], buffer.OpusOutputBuf)
				if err != nil {
					fmt.Printf("❌ Ошибка декодирования: %v\n", err)
					continue
				}

				audioState.samplesDecoded += samplesRead

				if samplesRead == 0 {
					continue
				}

				// Проверяем размер буфера
				if samplesRead > len(buffer.OutputBuffer) {
					fmt.Printf("⚠️ Количество декодированных сэмплов (%d) больше размера буфера (%d)\n",
						samplesRead, len(buffer.OutputBuffer))
					samplesRead = len(buffer.OutputBuffer)
				}

				// Конвертируем int16 в float32 для PortAudio
				outputFloat32 := int16ToFloat32(buffer.OpusOutputBuf[:samplesRead])
				// Копируем данные в выходной буфер с проверкой размера
				copy(buffer.OutputBuffer, outputFloat32)
				// Очищаем оставшуюся часть буфера
				for i := samplesRead; i < len(buffer.OutputBuffer); i++ {
					buffer.OutputBuffer[i] = 0
				}

				// Проверяем наличие звука в буфере
				hasSound := false
				maxAmplitude := float32(0)
				sumAmplitude := float32(0)
				for _, sample := range buffer.OutputBuffer[:samplesRead] {
					amplitude := float32(math.Abs(float64(sample)))
					sumAmplitude += amplitude
					if amplitude > maxAmplitude {
						maxAmplitude = amplitude
					}
					if amplitude > 0.01 {
						hasSound = true
					}
				}

				// Более плавная обработка сигнала
				if hasSound && maxAmplitude > 0 {
					// Адаптивное усиление с плавным переходом
					targetGain := float64(1.0)
					if maxAmplitude < 0.3 {
						targetGain = math.Min(float64(0.3/maxAmplitude), 2.0)
					}

					// Применяем усиление с плавным переходом
					for i := range buffer.OutputBuffer[:samplesRead] {
						// Нормализуем сигнал более мягко
						sample := float64(buffer.OutputBuffer[i])
						if math.Abs(sample) > 0.001 { // Игнорируем очень тихие сигналы
							sample *= targetGain
							// Мягкое ограничение амплитуды
							if sample > 0.95 {
								sample = 0.95 + math.Tanh(sample-0.95)*0.05
							} else if sample < -0.95 {
								sample = -0.95 + math.Tanh(sample+0.95)*0.05
							}
						}
						buffer.OutputBuffer[i] = float32(sample)
					}

					if debugMode {
						fmt.Printf("Применено усиление %.2fx (макс. амплитуда: %.4f)\n", targetGain, maxAmplitude)
					}
				}

				// Проверяем доступность буфера для записи
				available, err := audioState.outputStream.AvailableToWrite()
				if err != nil {
					fmt.Printf("❌ Ошибка проверки доступности буфера: %v\n", err)
					continue
				}

				if available < len(buffer.OutputBuffer) {
					fmt.Printf("⚠️ Буфер заполнен (доступно %d из %d)\n", available, len(buffer.OutputBuffer))
					time.Sleep(10 * time.Millisecond) // Даем время на освобождение буфера
					continue
				}

				// Проверяем состояние потока перед записью
				streamInfo := audioState.outputStream.Info()
				if streamInfo.OutputLatency > 200*time.Millisecond {
					fmt.Printf("⚠️ Высокая задержка вывода: %v\n", streamInfo.OutputLatency)
				}

				// Воспроизводим звук
				err = audioState.outputStream.Write()
				if err != nil {
					fmt.Printf("❌ Ошибка записи в выходной поток: %v\n", err)
					continue
				}

				if hasSound {
					audioState.samplesPlayed++
					fmt.Printf("🔊 Воспроизведение: макс. амплитуда=%.4f, средняя=%.4f, задержка=%v\n",
						maxAmplitude, sumAmplitude/float32(len(buffer.OutputBuffer)), streamInfo.OutputLatency)
				}

				// Логируем статистику каждые 5 секунд
				if time.Since(audioState.lastLogTime) > 5*time.Second {
					kbps := float64(audioState.bytesReceived) * 8 / 1024 / 5 // КБит/с за 5 секунд
					fmt.Printf("\n📊 Статистика за 5 секунд:\n")
					fmt.Printf("   Получено пакетов: %d (%.1f пак/с)\n",
						audioState.packetsReceived, float64(audioState.packetsReceived)/5)
					fmt.Printf("   Скорость приема: %.1f КБит/с\n", kbps)
					fmt.Printf("   Декодировано сэмплов: %d\n", audioState.samplesDecoded)
					fmt.Printf("   Воспроизведено сэмплов: %d\n", audioState.samplesPlayed)
					fmt.Printf("   Максимальная амплитуда: %.4f\n", maxAmplitude)

					if cpuLoad := audioState.outputStream.CpuLoad(); cpuLoad > 0.1 {
						fmt.Printf("   Загрузка CPU: %.1f%%\n", cpuLoad*100)
					}

					// Проверяем состояние потока
					fmt.Printf("   Состояние потока:\n")
					fmt.Printf("      Выходная задержка: %v\n", streamInfo.OutputLatency)
					fmt.Printf("      Частота дискретизации: %.0f Гц\n", streamInfo.SampleRate)
					fmt.Printf("      Доступно для записи: %d сэмплов\n", available)

					audioState.packetsReceived = 0
					audioState.bytesReceived = 0
					audioState.samplesDecoded = 0
					audioState.samplesPlayed = 0
					audioState.lastLogTime = time.Now()
				}
			}
		}
	}()

	return nil
}

func main() {
	// Инициализируем PortAudio в начале программы
	if err := initPortAudio(); err != nil {
		fmt.Printf("Ошибка инициализации PortAudio: %v\n", err)
		return
	}
	// Гарантируем завершение работы PortAudio при выходе
	defer terminatePortAudio()

	reader := bufio.NewReader(os.Stdin)

	// Запрашиваем IP сервера
	fmt.Print("Введите IP сервера (или нажмите Enter для localhost): ")
	serverIP, _ := reader.ReadString('\n')
	serverIP = strings.TrimSpace(serverIP)
	if serverIP == "" {
		serverIP = "127.0.0.1"
	}

	// Запрашиваем имя пользователя
	fmt.Print("Введите ваше имя: ")
	username, _ := reader.ReadString('\n')
	username = strings.TrimSpace(username)
	for username == "" {
		fmt.Print("Имя не может быть пустым. Введите ваше имя: ")
		username, _ = reader.ReadString('\n')
		username = strings.TrimSpace(username)
	}

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

	fmt.Println("\nДоступные команды:")
	fmt.Println("/voice - подключиться к голосовому чату")
	fmt.Println("/leave - отключиться от голосового чата")
	fmt.Println("/exit - выйти из чата")
	fmt.Println("Любой другой текст будет отправлен как сообщение")

	// Чтение ввода пользователя
	fmt.Print("> ")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := scanner.Text()

		switch text {
		case "/voice":
			if voiceConn == nil {
				// Проверяем, что PortAudio инициализирован
				if !paInitialized {
					if err := initPortAudio(); err != nil {
						fmt.Printf("Ошибка инициализации PortAudio: %v\n", err)
						continue
					}
				}

				// Подключаемся к голосовому чату
				voiceAddr, err := net.ResolveUDPAddr("udp", serverIP+":6001")
				if err != nil {
					fmt.Println("Ошибка разрешения голосового адреса:", err)
					continue
				}
				voiceConn, err = net.DialUDP("udp", nil, voiceAddr)
				if err != nil {
					fmt.Println("Ошибка подключения к голосовому чату:", err)
					continue
				}

				// Инициализируем аудио
				audioBuffer, err := initAudio()
				if err != nil {
					fmt.Printf("Ошибка инициализации аудио: %v\n", err)
					voiceConn.Close()
					voiceConn = nil
					continue
				}

				// Создаем канал для остановки аудио
				stopAudio = make(chan struct{})

				// Запускаем аудио потоки
				err = startAudioStream(voiceConn, audioBuffer)
				if err != nil {
					fmt.Printf("Ошибка запуска аудио потока: %v\n", err)
					voiceConn.Close()
					voiceConn = nil
					continue
				}

				// Отправляем уведомление о подключении к голосовому чату
				conn.Write([]byte("VOICE_CONNECT"))
				fmt.Println("Вы подключились к голосовому чату")
			} else {
				fmt.Println("Вы уже подключены к голосовому чату")
			}

		case "/leave":
			if voiceConn != nil {
				// Останавливаем аудио потоки
				close(stopAudio)
				audioWg.Wait()

				// Отправляем уведомление об отключении от голосового чата
				conn.Write([]byte("VOICE_DISCONNECT"))
				voiceConn.Close()
				voiceConn = nil
				fmt.Println("Вы отключились от голосового чата")
			} else {
				fmt.Println("Вы не подключены к голосовому чату")
			}

		case "/exit":
			if voiceConn != nil {
				// Останавливаем аудио потоки
				close(stopAudio)
				audioWg.Wait()

				conn.Write([]byte("VOICE_DISCONNECT"))
				voiceConn.Close()
			}
			return

		default:
			// Отправляем обычное сообщение
			_, err := conn.Write([]byte("[" + username + "]: " + text))
			if err != nil {
				fmt.Println("Ошибка отправки:", err)
				return
			}
		}
		fmt.Print("> ")
	}
}
