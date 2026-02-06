package playsound


import (
	"context"
	"io"
)

// PlayParams содержит настройки воспроизведения.
type PlayParams struct {
	Volume   float64 // Громкость NB! Тишина это -1, не 0!
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
	params = validateParams(params)

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
	tracker := &trackingStream{decodedStream: stream}
	player := otoCtx.NewPlayer(tracker)

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

	var tBytes int64
    if l, ok := stream.(interface{ Length() int64 }); ok {
        tBytes = l.Length()
    } else if l, ok := stream.(interface{ Length() int }); ok {
        tBytes = int64(l.Length())
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
		tracker:    tracker,
		totalBytes: tBytes,
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
