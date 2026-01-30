package play

import (
	"context"
	"io"
	"sync"

	"github.com/ebitengine/oto/v3"
)

type soundController struct {
	cancel     context.CancelFunc // Функция для принудительной остановки через контекст
	player     *oto.Player        // Экземпляр плеера из библиотеки oto
	params     PlayParams         // Копия параметров, с которыми был запущен звук
	sampleRate int                // Частота дискретизации (нужна для расчета позиций/секунд)
	isPaused   bool               // Флаг, предотвращающий закрытие потока мониторингом во время паузы
}

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

func (w *wavWrapper) SampleRate() int { return w.sampleRate }

var (
	otoCtx       *oto.Context
	once         sync.Once
	mu           sync.Mutex
	rootCtx      context.Context
	rootCancel   context.CancelFunc
	activeSounds = make(map[chan struct{}]soundController)
	activeMu     sync.Mutex
)

// getControl — хелпер для безопасного получения контроллера из карты.
func getControl(done chan struct{}) (soundController, bool) {
	activeMu.Lock()
	defer activeMu.Unlock()
	ctrl, ok := activeSounds[done]
	return ctrl, ok
}
