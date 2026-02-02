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
