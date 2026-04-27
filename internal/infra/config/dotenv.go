package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

var loadDotEnvOnce sync.Once
var loadDotEnvErr error

func loadDotEnv() error {
	loadDotEnvOnce.Do(func() {
		f, err := os.Open(".env")
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			loadDotEnvErr = fmt.Errorf("open .env: %w", err)
			return
		}
		defer func() {
			if err := f.Close(); err != nil && loadDotEnvErr == nil {
				loadDotEnvErr = fmt.Errorf("close .env: %w", err)
			}
		}()

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				loadDotEnvErr = fmt.Errorf(".env line %q: missing '='", line)
				return
			}
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k == "" {
				continue
			}
			if _, exists := os.LookupEnv(k); exists {
				continue
			}
			v = strings.TrimPrefix(v, `"`)
			v = strings.TrimSuffix(v, `"`)
			v = strings.TrimPrefix(v, `'`)
			v = strings.TrimSuffix(v, `'`)
			if err := os.Setenv(k, v); err != nil {
				loadDotEnvErr = fmt.Errorf("set env %q: %w", k, err)
				return
			}
		}
		if err := sc.Err(); err != nil {
			loadDotEnvErr = fmt.Errorf("scan .env: %w", err)
			return
		}
	})

	return loadDotEnvErr
}
