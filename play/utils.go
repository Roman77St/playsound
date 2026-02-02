package play


// secondsToBytes рассчитывает размер аудио-данных в байтах на основе длительности.
// Формула: секунды * частота дискретизации * 4 (2 канала по 2 байта на семпл).
func secondsToBytes(seconds, sampleRate int) int64 {
	// 4 байта = 2 канала * 2 байта на семпл (int16)
	return int64(seconds) * int64(sampleRate) * 4
}

// bytesToSeconds переводит объем данных в байтах в секунды.
func bytesToSeconds(b int64, sampleRate int) int {
	if sampleRate <= 0 {
		return 0
	}
	return int(b / int64(sampleRate*4))
}

// validateParams проверяет и корректирует параметры перед запуском.
func validateParams(p PlayParams) PlayParams {
	// Если громкость не указана, ставим 1.0 (100%)
	if p.Volume <= 0 || p.Volume > 1 {
		p.Volume = 1.0
	}

	// Позиция не может быть отрицательной
	if p.Position < 0 {
		p.Position = 0
	}

	return p
}