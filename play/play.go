package play

import (
	"context"
	"fmt"
	"io"
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

// decodedStream объединяет возможности чтения и получения частоты дискретизации
type decodedStream interface {
	io.ReadSeeker
	SampleRate() int
}

// wavWrapper адаптирует библиотеку youpy/go-wav под наш интерфейс
type wavWrapper struct {
	io.Reader
	sampleRate int
}


func (w *wavWrapper) Seek(offset int64, whence int) (int64, error) {
	return 0, nil
}
func (w *wavWrapper) SampleRate() int { return w.sampleRate }

// getDecoder  логика выбора формата
func getDecoder(f *os.File, path string) (decodedStream, error) {
	// 1. Пытаемся декодировать как MP3 (самый гибкий декодер)
	mp3Stream, err := mp3.NewDecoder(f)
	if err == nil {
		return mp3Stream, nil
	}

	// Если не вышло, возвращаемся в начало файла для следующей попытки
	f.Seek(0, io.SeekStart)

	// 2. Пытаемся декодировать как WAV
	d := wav.NewReader(f)
	finfo, err := d.Format()
	if err == nil {
		// Если это реально WAV, сбрасываем указатель и возвращаем обертку
		f.Seek(0, io.SeekStart)
		return &wavWrapper{f, int(finfo.SampleRate)}, nil
	}

	// 3. Если ничего не помогло, смотрим на расширение для вывода ошибки
	ext := strings.ToLower(filepath.Ext(path))
	return nil, fmt.Errorf("file content doesn't match extension or format is unsupported: %s", ext)
}

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

func StopAll() {
	mu.Lock()
	defer mu.Unlock()
	if rootCancel != nil {
		rootCancel()
		rootCtx, rootCancel = context.WithCancel(context.Background())
	}
}

func PlaySound(filePath string) (chan struct{}, error) {
	done := make(chan struct{})

	f, err := os.Open(filePath)
	if err != nil {
		close(done)
		return done, err
	}

	stream, err := getDecoder(f, filePath)
	if err != nil {
		f.Close()
		close(done)
		return done, err
	}

	if err := initEngine(stream.SampleRate()); err != nil {
		f.Close()
		close(done)
		return done, fmt.Errorf("failed to init audio engine: %w", err)
	}

	mu.Lock()
	ctx := rootCtx
	mu.Unlock()

	player := otoCtx.NewPlayer(stream)
	player.Play()

	var closeOnce sync.Once
	safeClose := func() {
		closeOnce.Do(func() {close(done)})
	}

	go func ()  {
		defer f.Close()
		defer safeClose()
		PlaybackLoop:
		for player.IsPlaying() {
			select {
			case <-ctx.Done():
				player.Pause()
				break PlaybackLoop
			default:
				time.Sleep(50*time.Millisecond)
			}
		}
	}()

	return done, nil
}