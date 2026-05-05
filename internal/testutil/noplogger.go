package testutil

// NopLogger реализует минимальный логгер для тестов (Info/Warn/Debug — no-op).
type NopLogger struct{}

func (NopLogger) Info(string, ...any)  {}
func (NopLogger) Warn(string, ...any)  {}
func (NopLogger) Debug(string, ...any) {}
