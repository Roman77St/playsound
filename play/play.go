package play

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/go-mp3"
	"github.com/youpy/go-wav"
)

var (
	otoCtx *oto.Context
	once sync.Once
	mu         sync.Mutex
	rootCtx    context.Context
	rootCancel context.CancelFunc
)

// decodedStream объединяет возможности чтения и получения частоты дискретизации.
// Используется для возврата универсального потока из декодеров.
type decodedStream interface {
	io.ReadSeeker
	SampleRate() int
}

// readSeekerAt нужен специально для WAV-декодера, который требует метод ReadAt.
type readSeekerAt interface {
    io.ReadSeeker
    io.ReaderAt
}

// wavWrapper адаптирует результат работы библиотеки youpy/go-wav под наш интерфейс.
type wavWrapper struct {
	io.ReadSeeker
	sampleRate int
}

func (w *wavWrapper) SampleRate() int { return w.sampleRate }

// PlayParams содержит настройки воспроизведения: громкость и флаг зацикливания.
type PlayParams struct {
	Volume float64
	Loop   bool
}

// getReadSeeker определяет источник аудио: локальный путь или URL.
// Если передан URL, файл скачивается в память целиком для обеспечения возможности Seek.
func getReadSeeker(path string) (io.ReadSeeker, io.Closer, error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		resp, err := http.Get(path)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("http error: %s", resp.Status)
		}

		// Читаем все данные в оперативную память.
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, nil, err
		}

		// bytes.NewReader реализует ReadSeeker и ReaderAt.
		// Использование io.NopCloser(nil) безопасно, так как буфер в памяти не требует закрытия.
		return bytes.NewReader(data), io.NopCloser(nil), nil
	}

	// Для локального файла возвращаем сам дескриптор файла.
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return f, f, nil // os.File является и ReadSeeker, и Closer
}


// getDecoder выбирает подходящий декодер (MP3 или WAV) на основе содержимого потока.
func getDecoder(rs io.ReadSeeker, path string) (decodedStream, error) {
	// 1. Пробуем декодировать как MP3.
	mp3Stream, err := mp3.NewDecoder(rs)
	if err == nil {
		return mp3Stream, nil
	}

	// Сбрасываем указатель после неудачной попытки.
	rs.Seek(0, io.SeekStart)

	// 2. Пробуем декодировать как WAV.
	// Проверяем, поддерживает ли поток метод ReadAt (необходим для WAV).
	if rsa, ok := rs.(readSeekerAt); ok {
        d := wav.NewReader(rsa) // Теперь здесь не будет ошибки компиляции
        finfo, err := d.Format()
        if err == nil {
            rsa.Seek(0, io.SeekStart)
            return &wavWrapper{rsa, int(finfo.SampleRate)}, nil
        }
    }

	// 3. Если ничего не помогло, смотрим на расширение для вывода ошибки
	ext := strings.ToLower(filepath.Ext(path))
	return nil, fmt.Errorf("file content doesn't match extension or format is unsupported: %s", ext)
}

// initEngine инициализирует аудио-движок Oto один раз за все время работы программы.
func initEngine(sampleRate int) error {
	var err error
	once.Do(func() {
		rootCtx, rootCancel = context.WithCancel(context.Background())
		op := &oto.NewContextOptions{
			SampleRate: sampleRate,
			ChannelCount: 2,
			Format: oto.FormatSignedInt16LE,
		}
		var readyChan chan struct {}
		otoCtx, readyChan, err = oto.NewContext(op)
		if err == nil {
			<-readyChan
		}
	})
	return err
}

// StopAll мгновенно останавливает все проигрываемые в данный момент звуки.
func StopAll() {
	mu.Lock()
	defer mu.Unlock()
	if rootCancel != nil {
		rootCancel()
		rootCtx, rootCancel = context.WithCancel(context.Background())
	}
}

// PlaySound — упрощенная функция для разового проигрывания на полной громкости.
func PlaySound(filePath string) (chan struct{}, error) {
	return PlaySoundWithParams(filePath, PlayParams{
		Volume: 1,
		Loop: false,
	})
}

// PlaySoundWithParams основная функция для запуска аудио с параметрами.
func PlaySoundWithParams(filePath string, params PlayParams) (chan struct{}, error) {
	done := make(chan struct{})

	// Шаг 1: Получаем доступ к данным (файл или сеть).
	rs, closer, err := getReadSeeker(filePath)
	if err != nil {
		return nil, err
	}

	// Шаг 2: Инициализируем нужный декодер.
	stream, err := getDecoder(rs, filePath)
	if err != nil {
		closer.Close()
		return nil, err
	}

	// Шаг 3: Подготавливаем аудио-движок.
	if err := initEngine(stream.SampleRate()); err != nil {
		closer.Close()
		return nil, err
	}

	// Шаг 4: Создаем и запускаем плеер.
	player := otoCtx.NewPlayer(stream)
	player.SetVolume(params.Volume)
	player.Play()

	mu.Lock()
	ctx := rootCtx
	mu.Unlock()

	// Шаг 5: Запускаем фоновый мониторинг состояния плеера.
	monitorPlayback(ctx, closer, stream, player, done, params)
	return done, nil
}

// monitorPlayback следит за окончанием трека и реализует логику Loop.
func monitorPlayback(ctx context.Context, closer io.Closer, stream decodedStream, player *oto.Player, done chan struct{}, params PlayParams) {
	var closeOnce sync.Once
	safeClose := func() {
		closeOnce.Do(func() { close(done) })
	}

	go func() {
		// Гарантируем закрытие файлов и каналов при выходе из функции.
		defer closer.Close()
		defer safeClose()

		currentPlayer := player

		for {
			// Если музыка перестала играть (дошла до конца).
			if !currentPlayer.IsPlaying() {
				if params.Loop {
					// Перематываем поток в начало.
					_, err := stream.Seek(0, io.SeekStart)
					if err != nil {
						return
					}
					// Создаем новый плеер для "чистого" перезапуска.
					currentPlayer = otoCtx.NewPlayer(stream)
					currentPlayer.SetVolume(params.Volume)
					currentPlayer.Play()
					// Защита от слишком частого перезапуска.
					time.Sleep(100 * time.Millisecond)
				} else {
					return
				}
			}
			select {
			case <-ctx.Done():                // Остановка по сигналу StopAll.
				currentPlayer.Pause()
				return
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()
}