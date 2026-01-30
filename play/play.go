package play

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

// PlayParams содержит настройки воспроизведения.
type PlayParams struct {
	Volume   float64 // Громкость
	Loop     bool    // Зацикливание трека
	FadeOut  bool    // Постепенное затухание звука
	FadeIn   bool    // Постепенное увеличение громкости
	Position int     // С какой секунды начать
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

			if !exists {
				return
			}
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
		if player.Volume() > v+step {
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
