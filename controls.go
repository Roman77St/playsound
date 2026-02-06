package playsound

import (
	"context"
	"fmt"
	"io"
)

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
	control, ok := getControl(done)
	if ok {
		control.cancel()
	}
}

// SetVolume динамически меняет громкость уже играющего звука.
// Возвращает ошибку, если звук не найден (уже завершился).
func SetVolume(done chan struct{}, volume float64) error {
	control, ok := getControl(done)

	if !ok {
		return fmt.Errorf("sound already finished or not found")
	}

	control.player.SetVolume(volume)
	return nil
}

// GetVolume возвращает текущую громкость звука.
func GetVolume(done chan struct{}) (float64, error) {
	control, ok := getControl(done)

	if !ok {
		return 0, fmt.Errorf("sound already finished or not found")
	}

	return control.player.Volume(), nil
}

// Возвращает текущую позицию трека в секундах
func GetPosition(done chan struct{}) (float64, error) {
	control, ok := getControl(done)

	if !ok {
		return 0, fmt.Errorf("sound not found")
	}

	pos := control.tracker.CurrentPos()

	return bytesToSeconds(pos, control.sampleRate), nil
}

// Перемотка запущенного трека.
func Seek(done chan struct{}, seconds float64) error {
	control, ok := getControl(done)

	if !ok {
		return fmt.Errorf("звук не найден")
	}

	offset := secondsToBytes(seconds, control.sampleRate)

	_, err := control.tracker.Seek(offset, io.SeekStart)
	return err
}

// Pause приостанавливает воспроизведение
func Pause(done chan struct{}) error {
	control, ok := getControl(done)

	if !ok {
		return fmt.Errorf("sound not found")
	}

	if control.params.FadeOut {
		fadeOut(control.player)
	}

	control.player.Pause()
	control.updateStatus(done, true)
	return nil
}

// Resume возобновляет приостановленное воспроизведение
func PlayOn(done chan struct{}) error {
	control, ok := getControl(done)

	if !ok {
		return fmt.Errorf("sound not found")
	}

	// Подготовка громкости перед запуском
	targetVol := control.params.Volume
	if control.params.FadeIn {
		control.player.SetVolume(0)
	} else {
		control.player.SetVolume(targetVol)
	}

	control.player.Play()

	// Снимаем флаг паузы до запуска горутины, чтобы мониторинг не закрыл трек
	control.updateStatus(done, false)

	if control.params.FadeIn {
		go fadeIn(control.player, control.params.Volume)
	}
	return nil
}
