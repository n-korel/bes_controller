package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

type Bucis struct {
	EC EC
}

type Bes struct {
	EC  EC
	MAC string
}

type EC struct {
	ListenPort8890   int
	QueryPort6710    int
	QueryPort7777    int
	BucisAddr        string
	BesBroadcastAddr string
	BesAddrOverride  string

	ClientResetInterval time.Duration
	KeepAliveInterval   time.Duration
	KeepAliveTimeout    time.Duration

	ClientAnswerTimeout     time.Duration
	ClientQueryRetryInterval time.Duration
	ClientQueryMaxRetries    int

	CallSetupTimeout     time.Duration
	ConversationTimeout  time.Duration
}

func ParseBucis() (Bucis, error) {
	if err := loadDotEnv(); err != nil {
		return Bucis{}, err
	}
	ec, err := parseEC()
	if err != nil {
		return Bucis{}, err
	}
	return Bucis{EC: ec}, nil
}

func ParseBes() (Bes, error) {
	if err := loadDotEnv(); err != nil {
		return Bes{}, err
	}

	ec, err := parseEC()
	if err != nil {
		return Bes{}, err
	}

	mac := getEnvStringDefault("EC_MAC", "")
	return Bes{EC: ec, MAC: mac}, nil
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
	keepAliveTimeout, err := getEnvDurationDefault("EC_KEEPALIVE_TIMEOUT", 0)
	if err != nil {
		return EC{}, err
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
	conversationTimeout, err := getEnvDurationDefault("EC_CONVERSATION_TIMEOUT", 30*time.Second)
	if err != nil {
		return EC{}, err
	}

	return EC{
		ListenPort8890:   listen8890,
		QueryPort6710:    q6710,
		QueryPort7777:    q7777,
		BucisAddr:        bucisAddr,
		BesBroadcastAddr: besBcast,
		BesAddrOverride:  besOverride,

		ClientResetInterval: resetInterval,
		KeepAliveInterval:   keepAliveInterval,
		KeepAliveTimeout:    keepAliveTimeout,

		ClientAnswerTimeout:      answerTimeout,
		ClientQueryRetryInterval: retryInterval,
		ClientQueryMaxRetries:    maxRetries,

		CallSetupTimeout:    callSetupTimeout,
		ConversationTimeout: conversationTimeout,
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
