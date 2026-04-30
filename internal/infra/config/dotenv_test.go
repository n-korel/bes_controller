package config

// resetDotEnvForTest сбрасывает кеш загрузки .env между тестами.
// Файл *_test.go, чтобы эта возможность не попадала в продакшен-сборку.
func resetDotEnvForTest() {
	dotenvMu.Lock()
	defer dotenvMu.Unlock()

	dotenvSetValues = make(map[string]string)
}
