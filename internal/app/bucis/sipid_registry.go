package bucis

import (
	"crypto/rand"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SipIDRegistry interface {
	GetOrCreate(mac string) string
	SetIP(sipID, ip string)
	GetIP(sipID string) (string, bool)
	FindBySenderIP(ip string) (string, bool)
}

type sipIDState struct {
	mu   sync.RWMutex
	seq  int
	boot uint64
	mod  uint64
	m    map[string]string // mac -> sipId
	ip   map[string]string // sipId -> bes ip (последний известный)
	ipr  map[string]string // bes ip -> sipId (последний ClientQuery с этого IP)
}

func newSipIDState(modulo uint64) *sipIDState {
	boot := uint64(time.Now().UnixNano())
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		boot = uint64(b[0])<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
	}
	return &sipIDState{
		boot: boot,
		mod:  modulo,
		m:    make(map[string]string),
		ip:   make(map[string]string),
		ipr:  make(map[string]string),
	}
}

func (s *sipIDState) GetOrCreate(mac string) string {
	mac = strings.TrimSpace(mac)
	// Важно: `strings.ToUpper(x)` не гарантирует, что `ToUpper(ToLower(x)) == ToUpper(x)` для всего Unicode
	// (пример: U+03F4 "ϴ"). А тест/прод-код могут получать MAC уже прогнанный через ToUpper/ToLower.
	// Поэтому делаем двухшаговую нормализацию: сначала ToLower, затем ToUpper — так ключ стабилен.
	mac = strings.ToUpper(strings.ToLower(mac))
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[mac]; ok {
		return v
	}
	s.seq++
	// По `docs/BUCIS_review.md`: sipId должен быть decimal integer string.
	// Поэтому делаем boot-уникальный числовой префикс через большой оффсет.
	raw := s.boot*1_000_000 + uint64(s.seq)
	if s.mod >= 2 {
		raw %= s.mod
		if raw == 0 {
			raw = s.mod
		}
	}
	v := strconv.FormatUint(raw, 10)
	s.m[mac] = v
	return v
}

func (s *sipIDState) SetIP(sipID, ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sipID == "" || ip == "" {
		return
	}

	// Если тот же IP "переехал" на другой sipID (например, БЭС перезапустилась и прислала ClientQuery с новым MAC),
	// важно чтобы обратный поиск по IP возвращал именно самый свежий sipID.
	oldIP := s.ip[sipID]
	s.ip[sipID] = ip

	if oldIP != "" && oldIP != ip {
		if cur := s.ipr[oldIP]; cur == sipID {
			delete(s.ipr, oldIP)
		}
	}
	s.ipr[ip] = sipID
}

func (s *sipIDState) GetIP(sipID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.ip[sipID]
	return v, ok
}

func (s *sipIDState) FindBySenderIP(ip string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.ipr[ip]
	return v, ok
}
