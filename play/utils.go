package play

func secondsToBytes(seconds, sampleRate int) int64 {
	// 4 байта = 2 канала * 2 байта на семпл (int16)
	return int64(seconds) * int64(sampleRate) * 4
}

func bytesToSeconds(b int64, sampleRate int) int {
	if sampleRate <= 0 {
		return 0
	}
	return int(b / int64(sampleRate*4))
}
