package playsound


import (
	"context"
	"io"
	"os"
	"testing"
	"time"
)

// go test -v -race ./play/...

func TestMain(m *testing.M) {
	// Инициализируем движок на стандартной частоте для тестов
	_ = initEngine(44100)

	// Запускаем все тесты
	code := m.Run()

	// Здесь можно добавить очистку, если нужно
	os.Exit(code)
}

// Тест перевода секунд в байты и обратно (utils.go)
func TestConversion(t *testing.T) {
	sampleRate := 44100
	seconds := 5.0

	// Ожидаем: 5 * 44100 * 4 = 882000 байт
	bytes := secondsToBytes(seconds, sampleRate)
	expectedBytes := int64(882000)

	if bytes != expectedBytes {
		t.Errorf("secondsToBytes() = %d; want %d", bytes, expectedBytes)
	}

	resSeconds := bytesToSeconds(bytes, sampleRate)
	if resSeconds != seconds {
		t.Errorf("bytesToSeconds() = %f; want %f", resSeconds, seconds)
	}
}

// Тест управления состояниями (имитация soundController)
func TestSoundControlState(t *testing.T) {
	done := make(chan struct{})
	sc := soundController{
		isPaused: false,
	}

	// Имитируем регистрацию в карте (engine.go)
	activeMu.Lock()
	activeSounds[done] = sc
	activeMu.Unlock()

	// Проверяем получение через хелпер getControl
	ctrl, ok := getControl(done)
	if !ok {
		t.Fatal("Sound should be registered in activeSounds")
	}

	// Проверяем смену статуса паузы
	ctrl.isPaused = true
	activeMu.Lock()
	activeSounds[done] = ctrl
	activeMu.Unlock()

	ctrlUpdated, _ := getControl(done)
	if !ctrlUpdated.isPaused {
		t.Error("isPaused should be true after update")
	}

	// Очистка
	activeMu.Lock()
	delete(activeSounds, done)
	activeMu.Unlock()
}

func TestValidateParams(t *testing.T) {
	tests := []struct {
		name     string
		input    PlayParams
		expected float64
	}{
		{"Default volume", PlayParams{Volume: 0}, 1.0},
		{"Keep volume", PlayParams{Volume: 0.5}, 0.5},
		{"Cap volume", PlayParams{Volume: 5.0}, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validateParams(tt.input)
			if p.Volume != tt.expected {
				t.Errorf("validateParams() volume = %v, want %v", p.Volume, tt.expected)
			}
		})
	}
}

// ===================================================================
// тест автоматического закрытия канала done после окончания трека

// mockStream имитирует аудио-поток, который мгновенно заканчивается.
type mockStream struct {
	io.ReadSeeker
}

func (m *mockStream) Read(p []byte) (n int, err error) {
	return 0, io.EOF // Имитируем конец файла
}

func (m *mockStream) SampleRate() int {
	return 44100
}

func (m *mockStream) Seek(offset int64, whence int) (int64, error) {
	return 0, nil
}

// mockCloser имитирует закрытие файла.
type mockCloser struct{}

func (m *mockCloser) Close() error { return nil }

func TestMonitorPlaybackClosing(t *testing.T) {
	// Инициализируем движок (нужно для создания плеера, если контекст oto уже есть)
	if otoCtx == nil {
        t.Skip("Пропуск: не удалось инициализировать аудио")
    }
	// В тестах на CI без аудиокарты otoCtx может быть nil, поэтому проверим это.
	if otoCtx == nil {
		t.Skip("Пропуск: аудио-движок не инициализирован (нет аудиоустройства)")
	}

	done := make(chan struct{})
	stream := &mockStream{}
	closer := &mockCloser{}
	player := otoCtx.NewPlayer(stream)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	params := PlayParams{Loop: false}

	// Регистрируем звук в системе
	activeMu.Lock()
	activeSounds[done] = soundController{
		cancel:     cancel,
		player:     player,
		params:     params,
		sampleRate: 44100,
	}
	activeMu.Unlock()

	// Запускаем мониторинг
	monitorPlayback(ctx, closer, stream, player, done, params)

	// Ждем закрытия канала done с таймаутом
	select {
	case <-done:
		// Успех: канал закрыт мониторингом
		activeMu.Lock()
		_, exists := activeSounds[done]
		activeMu.Unlock()
		if exists {
			t.Error("Звук должен быть удален из activeSounds после завершения")
		}
	case <-time.After(2 * time.Second):
		t.Error("Таймаут: мониторинг не закрыл канал done вовремя")
	}
}