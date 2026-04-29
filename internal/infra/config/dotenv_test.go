package config

import (
	"os"
	"sync"
)

// resetDotEnvForTest сбрасывает кеш загрузки .env между тестами.
// Файл *_test.go, чтобы эта возможность не попадала в продакшен-сборку.
func resetDotEnvForTest() {
	loadDotEnvOnce = sync.Once{}
	loadDotEnvErr = nil
	for _, k := range loadedDotEnvKeys {
		_ = os.Unsetenv(k)
	}
	loadedDotEnvKeys = nil
}

