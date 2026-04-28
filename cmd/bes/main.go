package main

import (
	"context"
	"errors"
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
	"bucis-bes_simulator/internal/media/receiver"
	"bucis-bes_simulator/internal/media/rtp"
	"bucis-bes_simulator/internal/media/sender"
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
	var (
		stateUnregistered      uint32 = 0
		stateRegistrationIdle  uint32 = 1
		stateRegistrationQuery uint32 = 2
		stateInCall            uint32 = 3
	)
	var state atomic.Uint32
	state.Store(stateUnregistered)
	var lastKeepAliveUnixNano atomic.Int64

	type answerEvent struct {
		answer *protocol.ClientAnswer
		from   *net.UDPAddr
	}
	answers := make(chan answerEvent, 8)

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
				// UNREGISTERED -> REGISTRATION (but don't interrupt an active call)
				if state.Load() != stateInCall {
					state.Store(stateRegistrationIdle)
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

	doPress := func() (sipID string, newIP string, err error) {
		state.Store(stateRegistrationQuery)
		defer func() {
			if state.Load() == stateRegistrationQuery {
				state.Store(stateRegistrationIdle)
			}
		}()
		for attempt := 1; attempt <= cfg.EC.ClientQueryMaxRetries; attempt++ {
			if err := sendQueryOnce(); err != nil {
				logger.Warn("ec_client_query send failed", "err", err, "attempt", attempt)
			} else {
				logger.Info("ec_client_query sent", "attempt", attempt, "to", cfg.EC.BucisAddr, "port", cfg.EC.QueryPort6710)
			}

			timeout := time.NewTimer(cfg.EC.ClientAnswerTimeout)
			select {
			case <-ctx.Done():
				timeout.Stop()
				return "", "", ctx.Err()
			case ev := <-answers:
				timeout.Stop()
				logger.Info("ec_client_answer received", "sip_id", ev.answer.SipID, "new_ip", ev.answer.NewIP, "from", ev.from.IP.String())
				return ev.answer.SipID, ev.answer.NewIP, nil
			case <-timeout.C:
				// retry
			}

			if attempt < cfg.EC.ClientQueryMaxRetries {
				timer := time.NewTimer(cfg.EC.ClientQueryRetryInterval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return "", "", ctx.Err()
				case <-timer.C:
				}
			}
		}
		return "", "", fmt.Errorf("no ec_client_answer after %d retries", cfg.EC.ClientQueryMaxRetries)
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
						s := state.Load()
						if s == stateUnregistered {
							continue
						}
						last := lastKeepAliveUnixNano.Load()
						if last == 0 {
							continue
						}
						age := time.Since(time.Unix(0, last))
						if age <= timeout {
							warnedAt = time.Time{}
							continue
						}
						if warnedAt.IsZero() || time.Since(warnedAt) >= timeout {
							warnedAt = time.Now()
							logger.Warn("keepalive missing", "age", age.String(), "timeout", timeout.String())
						}
					}
				}
			}()
		}
	}

	// Раньше запуск ждал команду `press` из stdin. Теперь `bes` сразу инициирует ClientQuery и SIP звонок.
	state.Store(stateRegistrationIdle)

	sipID, newIP, err := doPress()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = lconn.Close()
		os.Exit(1)
	}
	state.Store(stateInCall)
	if err := runSIP(ctx, logger, cfg, newIP, sipID, conversations); err != nil {
		fmt.Fprintln(os.Stderr, err)
		stop()
		_ = lconn.Close()
		os.Exit(1)
	}
	state.Store(stateRegistrationIdle)

	// graceful shutdown of background goroutines
	stop()
	_ = lconn.Close()
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

func runSIP(ctx context.Context, logger interface {
	Info(string, ...any)
	Warn(string, ...any)
}, cfg config.Bes, domain string, sipID string, conversations <-chan string) error {
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

		rx := receiver.New(lport)
		if audio.Enabled() {
			if pb, err := audio.StartPlayback(rtpCtx, audio.Device()); err == nil {
				// RTP приходит с джиттером, а aplay ожидает непрерывный поток.
				// Делаем простую буферизацию + плейаут по таймеру 20ms: при отсутствии пакетов играем тишину.
				playout := make(chan []int16, 64)
				rx.SetPCMSink(func(pcm []int16) {
					select {
					case playout <- pcm:
					default:
					}
				})
				go func() {
					t := time.NewTicker(rtp.FrameDuration)
					defer t.Stop()
					silence := make([]int16, rtp.SamplesPerFrame)
					var last []int16
					for {
						select {
						case <-rtpCtx.Done():
							return
						case <-t.C:
							// Берём самый свежий кадр (сбрасывая накопившиеся), чтобы не раздувать задержку.
							for {
								select {
								case f := <-playout:
									last = f
								default:
									goto play
								}
							}
						play:
							if last == nil || len(last) != rtp.SamplesPerFrame {
								_ = pb.WritePCM(silence)
								continue
							}
							_ = pb.WritePCM(last)
						}
					}
				}()
				defer func() { _ = pb.Close() }()
			} else {
				logger.Warn("audio playback disabled", "err", err)
			}
		}
		var startErr error
		for attempt := 1; attempt <= 10; attempt++ {
			startErr = rx.Start()
			if startErr == nil {
				break
			}
			if errors.Is(startErr, syscall.EADDRINUSE) {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			break
		}
		if startErr != nil {
			logger.Warn("rtp rx start failed", "local_port", lport, "err", startErr)
			return startErr
		}
		logger.Info("rtp rx started", "local_port", rx.MediaPort())
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-rtpCtx.Done():
					return
				case <-t.C:
					s := rx.StatsSnapshot()
					age := time.Since(s.LastPacket)
					if s.Received == 0 {
						logger.Info("rtp health", "received", 0, "last_packet_age", age.String())
						continue
					}
					logger.Info("rtp health",
						"received", s.Received,
						"expected", s.Expected(),
						"lost", s.Lost(),
						"jitter_ms", s.JitterMs(),
						"last_packet_age", age.String(),
					)
				}
			}
		}()

		tx, err := sender.NewTo(raddr)
		if err != nil {
			if c := rx.Conn(); c != nil {
				tx, err = sender.NewFromConn(c, raddr)
			}
		}
		if err == nil {
			logger.Info("rtp tx started", "remote_ip", raddr.IP.String(), "remote_port", raddr.Port)
			var frames <-chan []int16
			if audio.Enabled() {
				ch, err := audio.StartCaptureFrames(rtpCtx, audio.Device())
				if err == nil {
					frames = ch
				} else {
					logger.Warn("audio capture disabled", "err", err)
				}
			}
			if frames == nil {
				silenceCh := make(chan []int16, 1)
				frames = silenceCh
				go func() {
					ticker := time.NewTicker(rtp.FrameDuration)
					defer ticker.Stop()
					silence := make([]int16, rtp.SamplesPerFrame)
					for {
						select {
						case <-rtpCtx.Done():
							close(silenceCh)
							return
						case <-ticker.C:
							select {
							case silenceCh <- silence:
							default:
							}
						}
					}
				}()
			}
			go func() {
				if err := tx.StreamFramesAt(rtpCtx, time.Now().UnixMilli(), frames); err != nil && rtpCtx.Err() == nil {
					logger.Warn("rtp tx stopped with error", "err", err)
				}
			}()
			defer func() { _ = tx.Close() }()
		} else {
			logger.Warn("rtp tx create failed", "remote_ip", raddr.IP.String(), "remote_port", raddr.Port, "err", err)
		}
		defer func() {
			stats, err := rx.Stop()
			if err != nil {
				logger.Warn("rtp rx stop failed", "err", err)
			}
			if stats.Received > 0 {
				logger.Info("rtp stats",
					"received", stats.Received,
					"expected", stats.Expected(),
					"lost", stats.Lost(),
					"jitter_ms", stats.JitterMs(),
				)
			}
		}()
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
