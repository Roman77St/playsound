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

type soundController struct {
	cancel     context.CancelFunc
	player     *oto.Player
	params     PlayParams
	sampleRate int
	isPaused bool
}

// PlayParams содержит настройки воспроизведения.
type PlayParams struct {
	Volume   float64 // Громкость
	Loop     bool    // Зацикливание трека
	FadeOut  bool    // Постепенное затухание звука
	FadeIn   bool    // Постепенное увеличение громкости
	Position int     // С какой секунды начать
}

var (
	otoCtx       *oto.Context
	once         sync.Once
	mu           sync.Mutex
	rootCtx      context.Context
	rootCancel   context.CancelFunc
	activeSounds = make(map[chan struct{}]soundController)
	activeMu     sync.Mutex
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
			SampleRate:   sampleRate,
			ChannelCount: 2,
			Format:       oto.FormatSignedInt16LE,
		}
		var readyChan chan struct{}
		otoCtx, readyChan, err = oto.NewContext(op)
		if err == nil {
			<-readyChan
		}
	})
	return err
}

// PlaySound — упрощенная функция для разового проигрывания на полной громкости.
func PlaySound(filePath string) (chan struct{}, error) {
	return PlaySoundWithParams(filePath, PlayParams{
		Volume:  1,
		Loop:    false,
		FadeOut: true,
	})
}

// PlaySoundWithParams основная функция для запуска аудио с параметрами.
func PlaySoundWithParams(filePath string, params PlayParams) (chan struct{}, error) {
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


	// Если включен FadeIn, начинаем с нуля, иначе ставим целевую громкость сразу
    startVol := params.Volume
    if params.FadeIn {
        startVol = 0
    }
    player.SetVolume(startVol)

	// Если указана стартовая позиция — перематываем плеер
	if params.Position > 0 {
		offset := secondsToBytes(params.Position, stream.SampleRate())
		_, err = player.Seek(offset, io.SeekStart)
		if err != nil {
			return nil, err
		}
	}

	mu.Lock()
	soundCtx, soundCancel := context.WithCancel(rootCtx)
	mu.Unlock()

	done := make(chan struct{})
	activeMu.Lock()
	activeSounds[done] = soundController{
		cancel:     soundCancel,
		player:     player,
		params:     params,
		sampleRate: stream.SampleRate(),
	}
	activeMu.Unlock()

	player.Play()

	if params.FadeIn {
        go fadeIn(player, params.Volume)
    }

	// Шаг 5: Запускаем фоновый мониторинг состояния плеера.
	monitorPlayback(soundCtx, closer, stream, player, done, params)
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
		defer func() {
			activeMu.Lock()
			delete(activeSounds, done)
			activeMu.Unlock()
			closer.Close()
			safeClose()
		}()

		currentPlayer := player

		for {
			activeMu.Lock()
            currentSound, exists := activeSounds[done]
            activeMu.Unlock()

            if !exists { return }
			// Если музыка перестала играть (дошла до конца).
			if !currentPlayer.IsPlaying() && !currentSound.isPaused {
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
					time.Sleep(200 * time.Millisecond)
				} else {
					return
				}
			}
			select {
			case <-ctx.Done(): // Остановка по сигналу StopAll.
				if params.FadeOut {
					fadeOut(currentPlayer)
				}
				currentPlayer.Pause()
				return
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()
}

// fadeIn постепенно поднимает громкость плеера до целевого значения
func fadeIn(player *oto.Player, targetVolume float64) {
    step := 0.02
    for v := 0.0; v <= targetVolume; v += step {
		if player.Volume() > v + step {
            return
        }
        player.SetVolume(v)
        time.Sleep(30 * time.Millisecond)
    }
    player.SetVolume(targetVolume)
}

// fadeOut постепенно снижает громкость плеера до нуля
func fadeOut(player *oto.Player) {
    currentVol := player.Volume()
    if currentVol <= 0 {
        return
    }

    // Рассчитываем шаг так, чтобы всегда было 20 итераций.
    // Если громкость 0.1, шаг будет 0.005. Если 1.0, шаг будет 0.05.
    step := currentVol / 20.0

    for range 20 {
        currentVol -= step
        if currentVol < 0 {
            currentVol = 0
        }
        player.SetVolume(currentVol)
        time.Sleep(50 * time.Millisecond)

        if currentVol <= 0 {
            break
        }
    }
    // На всякий случай фиксируем чистый ноль в конце
    player.SetVolume(0)
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

// Stop останавливает конкретный звук по его каналу done
func Stop(done chan struct{}) {
	activeMu.Lock()
	control, ok := activeSounds[done]
	activeMu.Unlock()
	if ok {
		control.cancel()
	}
}

// SetVolume динамически меняет громкость уже играющего звука.
// Возвращает ошибку, если звук не найден (уже завершился).
func SetVolume(done chan struct{}, volume float64) error {
	activeMu.Lock()
	control, ok := activeSounds[done]
	activeMu.Unlock()

	if !ok {
		return fmt.Errorf("sound already finished or not found")
	}

	control.player.SetVolume(volume)
	return nil
}

// GetVolume возвращает текущую громкость звука.
func GetVolume(done chan struct{}) (float64, error) {
	activeMu.Lock()
	control, ok := activeSounds[done]
	activeMu.Unlock()

	if !ok {
		return 0, fmt.Errorf("sound already finished or not found")
	}

	return control.player.Volume(), nil
}

func secondsToBytes(seconds, sampleRate int) int64 {
	// 4 байта = 2 канала * 2 байта на семпл (int16)
	return int64(seconds) * int64(sampleRate) * 4
}

func bytesToSeconds(b int64, sampleRate int) int {
	if sampleRate <= 0 {
		return 0
	}
	return int(b / int64(sampleRate*4))
}

func GetPosition(done chan struct{}) (int, error) {
	activeMu.Lock()
	control, ok := activeSounds[done]
	activeMu.Unlock()

	if !ok {
		return 0, fmt.Errorf("sound not found")
	}

	// Seek(0, SeekCurrent) возвращает текущую позицию в байтах
	pos, err := control.player.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}

	return bytesToSeconds(pos, control.sampleRate), nil
}

// Перемотка запущенного трека.
func Seek(done chan struct{}, seconds int) error {
	activeMu.Lock()
	ctrl, ok := activeSounds[done]
	activeMu.Unlock()

	if !ok {
		return fmt.Errorf("звук не найден")
	}

	_, err := ctrl.player.Seek(secondsToBytes(seconds, ctrl.sampleRate), io.SeekStart)
	return err
}

// Pause приостанавливает воспроизведение
func Pause(done chan struct{}) error {
	activeMu.Lock()
	control, ok := activeSounds[done]
	activeMu.Unlock()

	if !ok {
		return fmt.Errorf("sound not found")
	}

	if control.params.FadeOut {
		fadeOut(control.player)
	}

	control.player.Pause()
	activeMu.Lock()
	control.isPaused = true
	activeSounds[done] = control
	activeMu.Unlock()
	return nil
}

// Resume возобновляет приостановленное воспроизведение
func PlayOn(done chan struct{}) error {
	activeMu.Lock()
	control, ok := activeSounds[done]
	activeMu.Unlock()

	if !ok {
		return fmt.Errorf("sound not found")
	}

	targetVol := control.params.Volume
	if control.params.FadeIn {
			control.player.SetVolume(0)
		} else {
			control.player.SetVolume(targetVol)
	}

	control.player.Play()

	// Снимаем флаг паузы ДО запуска горутины, чтобы мониторинг не закрыл трек
    activeMu.Lock()
    control.isPaused = false
    activeSounds[done] = control
    activeMu.Unlock()

	if control.params.FadeIn {
		go fadeIn(control.player, control.params.Volume)
	}

	control.isPaused = false
    activeSounds[done] = control
	return nil
}