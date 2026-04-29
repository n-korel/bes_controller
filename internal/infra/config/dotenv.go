package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

var dotenvMu sync.Mutex

// dotenvSetValues хранит последние значения, которые мы установили из .env.
// Это нужно, чтобы при повторной загрузке другого .env мы могли обновлять только те ключи,
// которые всё ещё "принадлежат" .env, а не были переопределены явным t.Setenv.
var dotenvSetValues = make(map[string]string)

func loadDotEnv(path string) error {
	dotenvMu.Lock()
	defer dotenvMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, cerr)
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
			return fmt.Errorf("%s line %q: missing '='", path, line)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}

		v = strings.TrimPrefix(v, `"`)
		v = strings.TrimSuffix(v, `"`)
		v = strings.TrimPrefix(v, `'`)
		v = strings.TrimSuffix(v, `'`)

		cur, exists := os.LookupEnv(k)
		if !exists {
			if err := os.Setenv(k, v); err != nil {
				return fmt.Errorf("set env %q: %w", k, err)
			}
			dotenvSetValues[k] = v
			continue
		}

		// Если значение уже существует, но оно совпадает с тем, что мы ставили из .env ранее,
		// то считаем, что ключ всё ещё управляется .env и можем обновить его новым содержимым.
		if prev, ok := dotenvSetValues[k]; ok && cur == prev {
			if err := os.Setenv(k, v); err != nil {
				return fmt.Errorf("set env %q: %w", k, err)
			}
			dotenvSetValues[k] = v
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}
	return nil
}
