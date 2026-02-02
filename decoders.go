package playsound


import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hajimehoshi/go-mp3"
	"github.com/youpy/go-wav"
)

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

		// Создаем временный файл
		tempFile, err := os.CreateTemp("", "audio-track-*.tmp")
		if err != nil {
			return nil, nil, fmt.Errorf("ошибка создания temp-файла: %v", err)
		}

		// Копируем данные из сети в файл
		_, err = io.Copy(tempFile, resp.Body)
		if err != nil {
			tempFile.Close()
			os.Remove(tempFile.Name())
			return nil, nil, fmt.Errorf("ошибка загрузки трека: %v", err)
		}

		// Возвращаемся в начало файла для чтения декодером
		tempFile.Seek(0, io.SeekStart)

		// Возвращаем специальную обертку,
		// чтобы при закрытии файл удалялся автоматически
		return tempFile, &tempFileCloser{tempFile}, nil
	}

	// Для локального файла возвращаем сам дескриптор файла.
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return f, f, nil // os.File является и ReadSeeker, и Closer
}

// tempFileCloser нужен, чтобы удалить файл с диска после проигрывания
type tempFileCloser struct {
	f *os.File
}

func (t *tempFileCloser) Close() error {
	filePath := t.f.Name()
	t.f.Close()

	time.Sleep(10 * time.Millisecond)


	err := os.Remove(filePath)
    if err != nil {
        // Если всё равно не удалилось, выведем в консоль причину
        err = fmt.Errorf("[Debug] Не удалось удалить временный файл %s: %v", filePath, err)
		return err
    }
    return nil
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


// Удаляем временные файлы, ранее созданные нашей программой
func CleanUpTempFiles() {
	tempDir := os.TempDir()
	files, err := os.ReadDir(tempDir)
	if err != nil {
		return
	}

	for _, file := range files {
		// Ищем файлы, созданные нашей библиотекой
		if !file.IsDir() && strings.HasPrefix(file.Name(), "audio-track-") && strings.HasSuffix(file.Name(), ".tmp") {
			fullPath := filepath.Join(tempDir, file.Name())
			_ = os.Remove(fullPath)
		}
	}
}