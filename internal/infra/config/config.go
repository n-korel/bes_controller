package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Bucis struct {
	EC EC
}

type Bes struct {
	EC  EC
	MAC string

	// Поля SIP_* заполняются в ParseBes() из окружения (после loadDotEnv).
	// Для тестов и встраивания можно задать вручную; пустой SIPDomain не перезаписывает domain из аргумента RunSIP.
	SIPDomain    string
	SIPUserBes   string
	SIPUserBucis string
	SIPPort      int
	SIPPassBes   string
}

type EC struct {
	ListenPort8890   int
	QueryPort6710    int
	QueryPort7777    int
	BesQueryDstPort  int
	BucisAddr        string
	BesBroadcastAddr string
	BesAddrOverride  string

	ClientResetInterval time.Duration
	KeepAliveInterval   time.Duration
	KeepAliveTimeout    time.Duration

	ClientAnswerTimeout      time.Duration
	ClientQueryRetryInterval time.Duration
	ClientQueryMaxRetries    int

	CallSetupTimeout time.Duration

	// 0 => без лимита. Иначе максимальная длительность разговора после подтверждения
	// ec_client_conversation (или сразу после установления SIP, если sipId пустой).
	ConversationTimeout time.Duration

	// 0 => без ограничения (по умолчанию, совместимо с текущим поведением).
	// >0 => sipId = (raw % SipIDModulo), при этом 0 заменяется на SipIDModulo (чтобы sipId не был пустым/нулевым).
	SipIDModulo uint64
}

func ParseBucis() (Bucis, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Bucis{}, err
	}
	if err := validateRTPPortRangeEnv(); err != nil {
		return Bucis{}, err
	}
	ec, err := parseEC()
	if err != nil {
		return Bucis{}, err
	}
	return Bucis{EC: ec}, nil
}

func ParseBes() (Bes, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Bes{}, err
	}
	if err := validateRTPPortRangeEnv(); err != nil {
		return Bes{}, err
	}

	ec, err := parseEC()
	if err != nil {
		return Bes{}, err
	}

	mac := getEnvStringDefault("EC_MAC", "")

	sipPort, err := getEnvIntDefault("SIP_PORT", 5060)
	if err != nil {
		return Bes{}, fmt.Errorf("SIP_PORT: %w", err)
	}
	if sipPort < 1 || sipPort > 65535 {
		return Bes{}, fmt.Errorf("SIP_PORT must be in range 1..65535 (got %d)", sipPort)
	}

	return Bes{
		EC:           ec,
		MAC:          mac,
		SIPDomain:    strings.TrimSpace(os.Getenv("SIP_DOMAIN")),
		SIPUserBes:   strings.TrimSpace(os.Getenv("SIP_USER_BES")),
		SIPUserBucis: strings.TrimSpace(os.Getenv("SIP_USER_BUCIS")),
		SIPPort:      sipPort,
		SIPPassBes:   os.Getenv("SIP_PASS_BES"),
	}, nil
}

func getEnvStringDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getEnvIntDefault(key string, def int) (int, error) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("%s must be a valid integer: %w", key, err)
		}
		return n, nil
	}
	return def, nil
}

func getEnvUint64Default(key string, def uint64) (uint64, error) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be a valid unsigned integer: %w", key, err)
		}
		return n, nil
	}
	return def, nil
}

func parseEC() (EC, error) {
	listen8890, err := getEnvIntDefault("EC_LISTEN_PORT_8890", 8890)
	if err != nil {
		return EC{}, err
	}
	q6710, err := getEnvIntDefault("EC_BUCIS_QUERY_PORT_6710", 6710)
	if err != nil {
		return EC{}, err
	}
	q7777, err := getEnvIntDefault("EC_BUCIS_QUERY_PORT_7777", 7777)
	if err != nil {
		return EC{}, err
	}
	besQueryDst, err := getEnvIntDefault("EC_BES_QUERY_DST_PORT", 6710)
	if err != nil {
		return EC{}, err
	}
	if besQueryDst <= 0 || besQueryDst > 65535 {
		return EC{}, fmt.Errorf("EC_BES_QUERY_DST_PORT must be in range 1..65535")
	}

	bucisAddr := getEnvStringDefault("EC_BUCIS_ADDR", "127.0.0.1")
	besBcast := getEnvStringDefault("EC_BES_BROADCAST_ADDR", "192.168.5.255")
	if err := validateIPv4Addr(besBcast); err != nil {
		return EC{}, fmt.Errorf("EC_BES_BROADCAST_ADDR=%q invalid: %w", besBcast, err)
	}
	besOverride := getEnvStringDefault("EC_BES_ADDR", "")

	resetInterval, err := getEnvDurationDefault("EC_CLIENT_RESET_INTERVAL", 5*time.Second)
	if err != nil {
		return EC{}, err
	}
	keepAliveInterval, err := getEnvDurationDefault("EC_KEEPALIVE_INTERVAL", 2*time.Second)
	if err != nil {
		return EC{}, err
	}
	keepAliveTimeout := time.Duration(0)
	if raw, ok := os.LookupEnv("EC_KEEPALIVE_TIMEOUT"); ok && strings.TrimSpace(raw) != "" {
		raw = strings.TrimSpace(raw)
		if raw == "-1" {
			keepAliveTimeout = -1
		} else {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return EC{}, fmt.Errorf("EC_KEEPALIVE_TIMEOUT must be a valid duration or -1: %w", err)
			}
			keepAliveTimeout = d
		}
	}
	if keepAliveTimeout == 0 {
		keepAliveTimeout = 3 * keepAliveInterval
	}

	answerTimeout, err := getEnvDurationDefault("EC_CLIENT_ANSWER_TIMEOUT", 3*time.Second)
	if err != nil {
		return EC{}, err
	}
	retryInterval, err := getEnvDurationDefault("EC_CLIENT_QUERY_RETRY_INTERVAL", 1*time.Second)
	if err != nil {
		return EC{}, err
	}
	maxRetries, err := getEnvIntDefault("EC_CLIENT_QUERY_MAX_RETRIES", 3)
	if err != nil {
		return EC{}, err
	}
	if maxRetries <= 0 {
		return EC{}, fmt.Errorf("EC_CLIENT_QUERY_MAX_RETRIES must be > 0")
	}

	callSetupTimeout, err := getEnvDurationDefault("EC_CALL_SETUP_TIMEOUT", 10*time.Second)
	if err != nil {
		return EC{}, err
	}

	conversationTimeout, err := getEnvDurationDefault("EC_CONVERSATION_TIMEOUT", 0)
	if err != nil {
		return EC{}, err
	}
	if conversationTimeout < 0 {
		return EC{}, fmt.Errorf("EC_CONVERSATION_TIMEOUT must be >= 0")
	}

	sipIDModulo, err := getEnvUint64Default("EC_SIPID_MODULO", 0)
	if err != nil {
		return EC{}, err
	}
	if sipIDModulo == 1 {
		return EC{}, fmt.Errorf("EC_SIPID_MODULO must be 0 or >= 2")
	}

	return EC{
		ListenPort8890:   listen8890,
		QueryPort6710:    q6710,
		QueryPort7777:    q7777,
		BesQueryDstPort:  besQueryDst,
		BucisAddr:        bucisAddr,
		BesBroadcastAddr: besBcast,
		BesAddrOverride:  besOverride,

		ClientResetInterval: resetInterval,
		KeepAliveInterval:   keepAliveInterval,
		KeepAliveTimeout:    keepAliveTimeout,

		ClientAnswerTimeout:      answerTimeout,
		ClientQueryRetryInterval: retryInterval,
		ClientQueryMaxRetries:    maxRetries,

		CallSetupTimeout: callSetupTimeout,

		ConversationTimeout: conversationTimeout,

		SipIDModulo: sipIDModulo,
	}, nil
}

func getEnvDurationDefault(key string, def time.Duration) (time.Duration, error) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("%s must be a valid duration: %w", key, err)
		}
		return d, nil
	}
	return def, nil
}

func validateIPv4Addr(addr string) error {
	if addr == "" {
		return fmt.Errorf("empty")
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return fmt.Errorf("invalid ip")
	}
	if ip.To4() == nil {
		return fmt.Errorf("not ipv4")
	}
	return nil
}

func validateRTPPortRangeEnv() error {
	raw, ok := os.LookupEnv("RTP_PORT_RANGE")
	if !ok {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	min, max, ok := parsePortRange(raw)
	if !ok {
		return fmt.Errorf("RTP_PORT_RANGE=%q invalid (expected \"min-max\" or single port)", raw)
	}
	if min <= 0 || max <= 0 || min > 65535 || max > 65535 || min > max {
		return fmt.Errorf("RTP_PORT_RANGE=%q invalid (range %d-%d out of bounds)", raw, min, max)
	}
	return nil
}

func parsePortRange(raw string) (min int, max int, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, false
	}
	if !strings.Contains(raw, "-") {
		p, err := strconv.Atoi(raw)
		if err != nil || p <= 0 || p > 65535 {
			return 0, 0, false
		}
		return p, p, true
	}
	parts := strings.Split(raw, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	a, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	b, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	if a <= 0 || b <= 0 || a > 65535 || b > 65535 || a > b {
		return 0, 0, false
	}
	return a, b, true
}
