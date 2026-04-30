package bes

import "testing"

func TestFirstHardwareMAC_DoesNotPanic(t *testing.T) {
	// Ничего не утверждаем про результат: на CI может не быть поднятых интерфейсов с MAC.
	_ = firstHardwareMAC()
}
