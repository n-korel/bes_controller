package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"bucis-bes_simulator/internal/audio"
	"bucis-bes_simulator/internal/control/protocol"
	"bucis-bes_simulator/internal/infra/config"
	"bucis-bes_simulator/internal/media/rtp"
	"bucis-bes_simulator/internal/media/rtpsession"
	"bucis-bes_simulator/pkg/log"

	"github.com/emiago/media/sdp"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgox"
)

func main() {
	flag.Parse()

	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bes")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.ParseBes()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	mac := cfg.MAC
	if mac == "" {
		mac = firstHardwareMAC()
	}
	if mac == "" {
		fmt.Fprintln(os.Stderr, "failed to detect MAC; set EC_MAC")
		os.Exit(1)
	}

	lconn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: cfg.EC.ListenPort8890})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = lconn.Close() }()

	logger.Info("ec started",
		"listen_8890", cfg.EC.ListenPort8890,
		"bucis_addr", cfg.EC.BucisAddr,
		"query_port", cfg.EC.QueryPort6710,
		"mac", mac,
	)

	conversations := make(chan string, 16)
	var state atomic.Uint32
	state.Store(stateUnregistered)
	var lastKeepAliveUnixNano atomic.Int64

	answers := make(chan answerEvent, 8)

	resetCh := make(chan struct{}, 4)
	pressCh := make(chan struct{}, 1)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 2048)
		for {
			_ = lconn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, raddr, err := lconn.ReadFromUDP(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					if ctx.Err() != nil {
						return
					}
					continue
				}
				if ctx.Err() != nil {
					return
				}
				logger.Warn("udp read failed", "err", err)
				continue
			}

			reset, _, answer, keepalive, conv, ok := protocol.Parse(buf[:n])
			if !ok {
				continue
			}
			if reset != nil {
				logger.Info("ec_client_reset received", "head1", reset.IPHead1, "head2", reset.IPHead2, "from", raddr.IP.String())
				select {
				case resetCh <- struct{}{}:
				default:
				}
			}
			if keepalive != nil {
				logger.Debug("ec_server_keepalive received", "opensips_ip", keepalive.OpenSIPSIP, "status", keepalive.Status, "from", raddr.IP.String())
				lastKeepAliveUnixNano.Store(time.Now().UnixNano())
			}
			if conv != nil {
				logger.Info("ec_client_conversation received", "sip_id", conv.SipID, "from", raddr.IP.String())
				select {
				case conversations <- conv.SipID:
				default:
				}
			}
			if answer != nil {
				select {
				case answers <- answerEvent{answer: answer, from: raddr}:
				default:
				}
			}
		}
	}()

	sendQueryOnce := func() error {
		raddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(cfg.EC.BucisAddr, strconv.Itoa(cfg.EC.QueryPort6710)))
		if err != nil {
			return err
		}
		conn, err := net.DialUDP("udp4", nil, raddr)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		_, err = conn.Write([]byte(protocol.FormatClientQuery(mac)))
		return err
	}

	// KeepAlive heartbeat: log if we stop receiving keepalives.
	{
		timeout := cfg.EC.KeepAliveTimeout
		if timeout > 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				t := time.NewTicker(500 * time.Millisecond)
				defer t.Stop()
				var warnedAt time.Time
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						now := time.Now()
						s := state.Load()
						last := lastKeepAliveUnixNano.Load()
						warn, wa := keepaliveShouldWarn(s, last, timeout, now, warnedAt)
						warnedAt = wa
						if warn {
							age := now.Sub(time.Unix(0, last))
							logger.Warn("keepalive missing", "age", age.String(), "timeout", timeout.String())
						}
					}
				}
			}()
		}
	}

	// Reader for external "press" command (one line: "press").
	// This is the trigger for REGISTRATION_IDLE -> REGISTRATION_QUERYING.
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "press" {
				continue
			}
			select {
			case pressCh <- struct{}{}:
			default:
				// "press" during active querying/call is ignored (buffer is full).
			}
		}
	}()

	fsm := besFSM{
		cfg:              cfg,
		logger:          logger,
		state:           &state,
		sendQueryOnce:  sendQueryOnce,
		runSIP:          runSIP,
		resetCh:         resetCh,
		pressCh:         pressCh,
		answers:         answers,
		autoPressAfterFirstReset: true,
	}

	_ = fsm.Run(ctx, conversations)

	// graceful shutdown of background goroutines
	stop()
	_ = lconn.Close()

	// Unblock stdin scanner goroutine.
	_ = os.Stdin.Close()

	done := make(chan struct{})
	go func() { defer close(done); wg.Wait() }()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		logger.Warn("shutdown timeout exceeded")
	}
}

func firstHardwareMAC() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(ifc.HardwareAddr) == 0 {
			continue
		}
		m := strings.ToUpper(ifc.HardwareAddr.String())
		if m != "" && m != "00:00:00:00:00:00" {
			return m
		}
	}
	return ""
}

func runSIP(
	ctx context.Context,
	logger besLogger,
	cfg config.Bes,
	domain string,
	sipID string,
	conversations <-chan string,
	dialEstablished chan<- struct{},
) error {
	if v := strings.TrimSpace(os.Getenv("SIP_DOMAIN")); v != "" {
		domain = v
	}
	besUser := strings.TrimSpace(os.Getenv("SIP_USER_BES"))
	bucisUser := strings.TrimSpace(os.Getenv("SIP_USER_BUCIS"))
	sipPortStr := strings.TrimSpace(os.Getenv("SIP_PORT"))
	if sipPortStr == "" {
		sipPortStr = "5060"
	}
	sipPort, err := strconv.Atoi(sipPortStr)
	if err != nil {
		return fmt.Errorf("SIP_PORT invalid: %w", err)
	}
	if besUser == "" {
		if sipID == "" {
			return fmt.Errorf("SIP_USER_BES must be set (or have sipId from ec_client_answer)")
		}
		besUser = "bes_" + sipID
	}
	if bucisUser == "" {
		return fmt.Errorf("SIP_USER_BUCIS must be set")
	}
	besPass := os.Getenv("SIP_PASS_BES")

	ua, err := sipgo.NewUA(
		sipgo.WithUserAgent(besUser),
		sipgo.WithUserAgentHostname(domain),
	)
	if err != nil {
		return err
	}
	defer func() { _ = ua.Close() }()

	listenHost := "0.0.0.0"
	if ip := net.ParseIP(domain); ip != nil && ip.IsLoopback() {
		listenHost = "127.0.0.1"
	}
	phone := sipgox.NewPhone(ua, sipgox.WithPhoneListenAddr(sipgox.ListenAddr{
		Network: "udp",
		Addr:    net.JoinHostPort(listenHost, "0"),
	}))
	defer phone.Close()

	dialCtx, cancelDial := context.WithTimeout(ctx, 15*time.Second)
	defer cancelDial()
	dialog, err := phone.Dial(dialCtx, sip.Uri{User: bucisUser, Host: domain, Port: sipPort}, sipgox.DialOptions{
		Username: besUser,
		Password: besPass,
		Formats:  sdp.NewFormats(strconv.Itoa(int(rtp.PayloadTypeG726()))),
	})
	if err != nil {
		return err
	}
	defer func() { _ = dialog.Close() }()
	logger.Info("sip call established", "to", bucisUser, "domain", domain)
	if dialEstablished != nil {
		select {
		case dialEstablished <- struct{}{}:
		default:
		}
	}

	if dialog.MediaSession != nil && dialog.Laddr != nil && dialog.Raddr != nil {
		lport := dialog.Laddr.Port
		raddr := dialog.Raddr
		logger.Info("rtp negotiated",
			"local_port", lport,
			"remote_ip", raddr.IP.String(),
			"remote_port", raddr.Port,
			"pt_g726", int(rtp.PayloadTypeG726()),
			"rtp_port_range", strings.TrimSpace(os.Getenv("RTP_PORT_RANGE")),
			"audio_enabled", audio.Enabled(),
			"audio_device", audio.Device(),
		)
		dialog.MediaSession.Close() // free port; we use our own RTP sockets

		rtpCtx, cancelRTP := context.WithCancel(ctx)
		defer cancelRTP()
		cleanup, _, rtpErr := rtpsession.Start(rtpCtx, logger, lport, raddr)
		if cleanup != nil {
			defer cleanup()
		}
		// Ошибка RX — критическая (как раньше), ошибка TX — уже залогирована и RX продолжаем.
		if rtpErr != nil && cleanup == nil {
			return rtpErr
		}
	} else {
		logger.Warn("rtp skipped: missing sdp addresses")
	}

	callSetupTimeout := cfg.EC.CallSetupTimeout
	if sipID != "" {
		timer := time.NewTimer(callSetupTimeout)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = dialog.Hangup(context.Background())
				return ctx.Err()
			case <-dialog.Context().Done():
				return nil
			case <-timer.C:
				logger.Warn("ec_client_conversation timeout", "sip_id", sipID, "timeout", callSetupTimeout.String())
				goto afterConversation
			case got := <-conversations:
				if got != sipID {
					continue
				}
				logger.Info("conversation confirmed via ec", "sip_id", sipID)
				goto afterConversation
			}
		}
	}

afterConversation:
	conversationTimeout := cfg.EC.ConversationTimeout
	select {
	case <-ctx.Done():
		_ = dialog.Hangup(context.Background())
		return ctx.Err()
	case <-dialog.Context().Done():
		return nil
	case <-time.After(conversationTimeout):
		_ = dialog.Hangup(context.Background())
		logger.Info("sip call hangup done", "timeout", conversationTimeout.String())
		return nil
	}
}
