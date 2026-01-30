package play

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
