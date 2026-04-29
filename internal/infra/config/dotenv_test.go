package config

import (
	"os"
)

// resetDotEnvForTest сбрасывает кеш загрузки .env между тестами.
// Файл *_test.go, чтобы эта возможность не попадала в продакшен-сборку.
func resetDotEnvForTest() {
	dotenvMu.Lock()
	defer dotenvMu.Unlock()

	for k, v := range dotenvSetValues {
		if cur, ok := os.LookupEnv(k); ok && cur == v {
			_ = os.Unsetenv(k)
		}
	}
	dotenvSetValues = make(map[string]string)
}

