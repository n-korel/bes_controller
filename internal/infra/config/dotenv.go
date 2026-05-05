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
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
			if line == "" {
				continue
			}
		}
		k, vRaw, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s line %q: missing '='", path, line)
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}

		v, perr := parseDotEnvValue(strings.TrimSpace(vRaw))
		if perr != nil {
			return fmt.Errorf("%s line %q: %w", path, line, perr)
		}

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

// parseDotEnvValue разбирает правую часть строки KEY=… в духе dotenv:
// двойные кавычки с escape, одинарные — литерал до закрывающей ', без обрезки внутренних апострофов в double-quoted.
func parseDotEnvValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	switch raw[0] {
	case '"':
		return parseDotEnvDoubleQuoted(raw[1:])
	case '\'':
		return parseDotEnvSingleQuoted(raw[1:])
	default:
		return stripDotEnvUnquotedTrailingComment(raw), nil
	}
}

func parseDotEnvDoubleQuoted(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			if i+1 < len(s) {
				rest := strings.TrimSpace(s[i+1:])
				if rest != "" && !strings.HasPrefix(rest, "#") {
					return "", fmt.Errorf("unexpected data after closing quote: %q", rest)
				}
			}
			return b.String(), nil
		}
		if c == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '\\', '"':
				b.WriteByte(s[i+1])
			default:
				b.WriteByte(s[i+1])
			}
			i++
			continue
		}
		b.WriteByte(c)
	}
	return "", fmt.Errorf("unterminated double-quoted value")
}

func parseDotEnvSingleQuoted(s string) (string, error) {
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			if i+1 < len(s) {
				rest := strings.TrimSpace(s[i+1:])
				if rest != "" && !strings.HasPrefix(rest, "#") {
					return "", fmt.Errorf("unexpected data after closing quote: %q", rest)
				}
			}
			return s[:i], nil
		}
	}
	return "", fmt.Errorf("unterminated single-quoted value")
}

func stripDotEnvUnquotedTrailingComment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '#' {
			continue
		}
		if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}
