package playsound


import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

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
					// // Создаем новый плеер для "чистого" перезапуска.
					// currentPlayer = otoCtx.NewPlayer(stream)
					// currentPlayer.SetVolume(params.Volume)
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
			case <-time.After(100 * time.Millisecond):
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