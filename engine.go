package playsound

import (
	"context"
	"io"
	"sync"

	"github.com/ebitengine/oto/v3"
)

// soundController представляет собой активную сессию проигрывания звука.
// Она хранит всё необходимое для динамического управления потоком.
type soundController struct {
	cancel     context.CancelFunc // Функция для немедленной остановки горутины мониторинга и очистки ресурсов.
	player     *oto.Player        // ЭПрямой доступ к аудио-плееру Oto для изменения громкости и паузы.
	params     PlayParams         // Настройки, переданные при старте (нужны для Loop и Fade эффектов).
	sampleRate int                // Частота дискретизации, используется для конвертации байтов в секунды.
	isPaused   bool               // Флаг состояния паузы. Если true, мониторинг игнорирует отсутствие воспроизведения.
	totalBytes int64              // Общий размер аудиоданных в байтах (для расчета длительности)
	tracker    *trackingStream    // Счётчик прогресса чтения, оборачивающий основной поток
}

// updateStatus безопасно обновляет флаг паузы в карте активных звуков.
func (sc *soundController) updateStatus(done chan struct{}, paused bool) {
	activeMu.Lock()
	defer activeMu.Unlock()
	sc.isPaused = paused
	activeSounds[done] = *sc
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

func (w *wavWrapper) SampleRate() int { return w.sampleRate }

func (w *wavWrapper) Length() int64 {
	currentPos, _ := w.ReadSeeker.Seek(0, io.SeekCurrent)
	totalSize, _ := w.ReadSeeker.Seek(0, io.SeekEnd)
	w.ReadSeeker.Seek(currentPos, io.SeekStart)

	return totalSize
}

// initEngine инициализирует аудио-движок Oto один раз за все время работы программы.
func initEngine(sampleRate int) error {
	var err error
	once.Do(func() {
		CleanUpTempFiles()
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

// trackingStream оборачивает поток аудиоданных и отслеживает текущую позицию чтения.
// Это необходимо, так как прямое обращение к плееру для получения позиции
// может вызвать заикание звука или конфликты доступа
type trackingStream struct {
	decodedStream
	currentPos int64
	mu         sync.Mutex
}

// Read считывает данные из декодера и обновляет счетчик прочитанных байт.
// Вызывается автоматически плеером oto в процессе воспроизведения.
func (ts *trackingStream) Read(p []byte) (n int, err error) {
	n, err = ts.decodedStream.Read(p)
	ts.mu.Lock()
	ts.currentPos += int64(n)
	ts.mu.Unlock()
	return n, err
}

// Seek изменяет позицию в декодере и синхронизирует внутренний счетчик.
// Используется для ручной перемотки трека пользователем.
func (ts *trackingStream) Seek(offset int64, whence int) (int64, error) {
	newPos, err := ts.decodedStream.Seek(offset, whence)
	if err == nil {
		ts.mu.Lock()
		ts.currentPos = newPos
		ts.mu.Unlock()
	}
	return newPos, err
}

// CurrentPos возвращает точное количество байт, прошедших через поток.
// Используется функцией GetPosition для отображения времени в UI.
func (ts *trackingStream) CurrentPos() int64 {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.currentPos
}