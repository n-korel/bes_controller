package config

import "testing"

// resetDotEnvForTest сбрасывает кеш загрузки .env между тестами.
// Файл *_test.go, чтобы эта возможность не попадала в продакшен-сборку.
func resetDotEnvForTest() {
	dotenvMu.Lock()
	defer dotenvMu.Unlock()

	dotenvSetValues = make(map[string]string)
}

func TestParseDotEnvValue(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{raw: "", want: ""},
		{raw: "plain", want: "plain"},
		{raw: `hello world`, want: "hello world"},
		{raw: `it's a device`, want: "it's a device"},
		{raw: `"it's a device"`, want: "it's a device"},
		{raw: `'with spaces'`, want: "with spaces"},
		{raw: `"escaped \" quote"`, want: `escaped " quote`},
		{raw: `"line\nbreak"`, want: "line\nbreak"},
		{raw: `x#hash-in-value`, want: "x#hash-in-value"},
		{raw: `value # comment`, want: "value"},
		{raw: `x  # comment`, want: "x"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := parseDotEnvValue(tc.raw)
			if err != nil {
				t.Fatalf("parseDotEnvValue: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestParseDotEnvValue_Errors(t *testing.T) {
	_, err := parseDotEnvValue(`"unterminated`)
	if err == nil {
		t.Fatal("expected error")
	}
	_, err = parseDotEnvValue(`'unterminated`)
	if err == nil {
		t.Fatal("expected error")
	}
	_, err = parseDotEnvValue(`"ok" tail`)
	if err == nil {
		t.Fatal("expected error")
	}
}
