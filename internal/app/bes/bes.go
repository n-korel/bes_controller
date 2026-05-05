package bes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bucis-bes_simulator/internal/audio"
	"bucis-bes_simulator/internal/control/protocol"
	"bucis-bes_simulator/internal/infra/config"
	"bucis-bes_simulator/internal/media/rtp"
	"bucis-bes_simulator/internal/media/rtpsession"

	"github.com/emiago/media/sdp"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgox"
)

type Logger interface {
	Info(string, ...any)
	Warn(string, ...any)
}

type debugLogger interface {
	Debug(string, ...any)
}

type RunSIPFunc func(
	ctx context.Context,
	logger Logger,
	cfg config.Bes,
	domain string,
	sipID string,
	conversations <-chan string,
	dialEstablished chan<- struct{},
) error

type Options struct {
	MAC                      string
	PressCh                  <-chan struct{}
	AutoPressAfterFirstReset bool
	RunSIP                   RunSIPFunc

	// OnListenPort is used mainly for tests: when ListenPort8890==0,
	// Run binds to an ephemeral port and reports the actual port here.
	OnListenPort chan<- int

	OnResetReceived        chan<- struct{}
	OnAnswerReceived       chan<- *protocol.ClientAnswer
	OnConversationReceived chan<- string
}

//nolint:gocyclo // Оркестратор сценария: дробление на мелкие функции ухудшит читаемость потока.
func Run(ctx context.Context, logger Logger, cfg config.Bes, opts Options) error {
	mac := strings.TrimSpace(opts.MAC)
	if mac == "" {
		mac = strings.TrimSpace(cfg.MAC)
	}
	if mac == "" {
		mac = firstHardwareMAC()
	}
	if mac == "" {
		return fmt.Errorf("failed to detect MAC; set EC_MAC")
	}
	if _, err := protocol.FormatClientQuery(mac); err != nil {
		return fmt.Errorf("invalid EC MAC: %w", err)
	}

	if opts.RunSIP == nil {
		opts.RunSIP = DefaultRunSIP
	}

	lconn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: cfg.EC.ListenPort8890})
	if err != nil {
		return fmt.Errorf("listen udp 8890: %w", err)
	}
	defer func() { _ = lconn.Close() }()

	if cfg.EC.ListenPort8890 == 0 && opts.OnListenPort != nil {
		if la, ok := lconn.LocalAddr().(*net.UDPAddr); ok && la != nil {
			select {
			case opts.OnListenPort <- la.Port:
			default:
			}
		}
	}

	logger.Info("ec started",
		"listen_8890", cfg.EC.ListenPort8890,
		"bucis_addr", cfg.EC.BucisAddr,
		"query_port", cfg.EC.BesQueryDstPort,
		"mac", mac,
	)

	conversations := make(chan string, 16)
	var state atomic.Uint32
	state.Store(stateUnregistered)
	var lastKeepAliveUnixNano atomic.Int64

	answers := make(chan answerEvent, 8)
	resetCh := make(chan struct{}, 4)

	pressCh := opts.PressCh
	if pressCh == nil {
		pressCh = make(chan struct{}, 1)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 2048)
		for {
			_ = lconn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, raddr, err := lconn.ReadFromUDP(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
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

			pkt, ok := protocol.Parse(buf[:n])
			if !ok {
				continue
			}

			switch pkt.Type {
			case protocol.ECPacketUnknown, protocol.ECPacketClientQuery:
				// ignore: these packet types are handled by the other side
				continue
			case protocol.ECPacketClientReset:
				if pkt.Reset == nil {
					continue
				}
				logger.Info("ec_client_reset received", "head1", pkt.Reset.IPHead1, "head2", pkt.Reset.IPHead2, "from", raddr.IP.String())
				select {
				case resetCh <- struct{}{}:
				default:
				}
				if opts.OnResetReceived != nil {
					select {
					case opts.OnResetReceived <- struct{}{}:
					default:
					}
				}

			case protocol.ECPacketServerKeepAlive:
				if pkt.KeepAlive == nil {
					continue
				}
				if dl, ok := logger.(debugLogger); ok {
					dl.Debug("ec_server_keepalive received", "opensips_ip", pkt.KeepAlive.OpenSIPSIP, "status", pkt.KeepAlive.Status, "from", raddr.IP.String())
				}
				lastKeepAliveUnixNano.Store(time.Now().UnixNano())

			case protocol.ECPacketClientConversation:
				if pkt.Conversation == nil {
					continue
				}
				logger.Info("ec_client_conversation received", "sip_id", pkt.Conversation.SipID, "from", raddr.IP.String())
				select {
				case conversations <- pkt.Conversation.SipID:
				default:
				}
				if opts.OnConversationReceived != nil {
					select {
					case opts.OnConversationReceived <- pkt.Conversation.SipID:
					default:
					}
				}

			case protocol.ECPacketClientAnswer:
				if pkt.Answer == nil {
					continue
				}
				select {
				case answers <- answerEvent{answer: pkt.Answer, from: raddr}:
				default:
				}
				if opts.OnAnswerReceived != nil {
					select {
					case opts.OnAnswerReceived <- pkt.Answer:
					default:
					}
				}
			default:
				continue
			}
		}
	}()

	sendQueryOnce := func() error {
		raddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(cfg.EC.BucisAddr, strconv.Itoa(cfg.EC.BesQueryDstPort)))
		if err != nil {
			return fmt.Errorf("resolve query addr: %w", err)
		}
		conn, err := net.DialUDP("udp4", nil, raddr)
		if err != nil {
			return fmt.Errorf("dial query addr: %w", err)
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		queryLine, ferr := protocol.FormatClientQuery(mac)
		if ferr != nil {
			return fmt.Errorf("format client query: %w", ferr)
		}
		_, err = conn.Write([]byte(queryLine))
		if err != nil {
			return fmt.Errorf("send query: %w", err)
		}
		return nil
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

	fsm := besFSM{
		cfg:                      cfg,
		logger:                   logger,
		state:                    &state,
		sendQueryOnce:            sendQueryOnce,
		runSIP:                   opts.RunSIP,
		resetCh:                  resetCh,
		pressCh:                  pressCh,
		answers:                  answers,
		autoPressAfterFirstReset: opts.AutoPressAfterFirstReset,
	}

	_ = fsm.Run(ctx, conversations)

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

	return nil
}

const (
	stateUnregistered      uint32 = 0
	stateRegistrationIdle  uint32 = 1
	stateRegistrationQuery uint32 = 2
	stateCallSetup         uint32 = 3
	stateInCall            uint32 = 4
)

func keepaliveShouldWarn(
	state uint32,
	lastKeepAliveUnixNano int64,
	timeout time.Duration,
	now time.Time,
	warnedAt time.Time,
) (warn bool, newWarnedAt time.Time) {
	if timeout <= 0 {
		return false, warnedAt
	}
	if state == stateUnregistered {
		return false, warnedAt
	}
	if lastKeepAliveUnixNano == 0 {
		return false, warnedAt
	}
	age := now.Sub(time.Unix(0, lastKeepAliveUnixNano))
	if age <= timeout {
		return false, time.Time{}
	}
	if warnedAt.IsZero() || now.Sub(warnedAt) >= timeout {
		return true, now
	}
	return false, warnedAt
}

type answerEvent struct {
	answer *protocol.ClientAnswer
	from   *net.UDPAddr
}

func doPress(
	ctx context.Context,
	logger interface {
		Info(string, ...any)
		Warn(string, ...any)
	},
	cfg config.Bes,
	sendQueryOnce func() error,
	answers <-chan answerEvent,
	cancelCh <-chan struct{},
) (sipID string, newIP string, err error) {
	for attempt := 1; attempt <= cfg.EC.ClientQueryMaxRetries; attempt++ {
	Drain:
		for {
			select {
			case <-answers:
			default:
				break Drain
			}
		}

		if err := sendQueryOnce(); err != nil {
			logger.Warn("ec_client_query send failed", "err", err, "attempt", attempt)
		} else {
			logger.Info("ec_client_query sent", "attempt", attempt, "to", cfg.EC.BucisAddr, "port", cfg.EC.BesQueryDstPort)
		}

		timeout := time.NewTimer(cfg.EC.ClientAnswerTimeout)
		select {
		case <-ctx.Done():
			timeout.Stop()
			return "", "", fmt.Errorf("context done: %w", ctx.Err())
		case <-cancelCh:
			timeout.Stop()
			return "", "", fmt.Errorf("canceled: %w", context.Canceled)
		case ev := <-answers:
			timeout.Stop()
			if ev.answer == nil {
				return "", "", fmt.Errorf("received nil ec_client_answer")
			}
			fromIP := ""
			if ev.from != nil && ev.from.IP != nil {
				fromIP = ev.from.IP.String()
			}
			logger.Info("ec_client_answer received", "sip_id", ev.answer.SipID, "new_ip", ev.answer.NewIP, "from", fromIP)
			return ev.answer.SipID, ev.answer.NewIP, nil
		case <-timeout.C:
		}

		if attempt < cfg.EC.ClientQueryMaxRetries {
			timer := time.NewTimer(cfg.EC.ClientQueryRetryInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", "", fmt.Errorf("context done: %w", ctx.Err())
			case <-cancelCh:
				timer.Stop()
				return "", "", fmt.Errorf("canceled: %w", context.Canceled)
			case <-timer.C:
			}
		}
	}
	return "", "", fmt.Errorf("no ec_client_answer after %d retries", cfg.EC.ClientQueryMaxRetries)
}

type besFSM struct {
	cfg    config.Bes
	logger Logger

	state *atomic.Uint32

	sendQueryOnce func() error
	runSIP        RunSIPFunc

	resetCh                  <-chan struct{}
	pressCh                  <-chan struct{}
	answers                  <-chan answerEvent
	autoPressAfterFirstReset bool
}

func (f *besFSM) Run(ctx context.Context, conversations <-chan string) error {
	st := stateUnregistered
	f.state.Store(st)

	autoPressDone := !f.autoPressAfterFirstReset
	for ctx.Err() == nil {
		switch st {
		case stateUnregistered:
			select {
			case <-ctx.Done():
				return fmt.Errorf("context done: %w", ctx.Err())
			case <-f.resetCh:
				st = stateRegistrationIdle
				f.state.Store(st)
				if !autoPressDone {
					autoPressDone = true
					st = stateRegistrationQuery
					f.state.Store(st)
					f.drainAnswers()
					f.drainPress()
					f.drainConversations(conversations)
					f.drainResetBurst()
					sipID, newIP, err := doPress(ctx, f.logger, f.cfg, f.sendQueryOnce, f.answers, f.resetCh)
					if err != nil {
						if ctx.Err() != nil {
							return fmt.Errorf("context done: %w", ctx.Err())
						}
						f.logger.Warn("press failed", "err", err)
						st = stateRegistrationIdle
						f.state.Store(st)
						continue
					}

					st = sipCallOrchestrator{f: f}.run(ctx, sipID, newIP, conversations)
				} else {
					f.drainResetBurst()
				}
			}

		case stateRegistrationIdle:
			select {
			case <-ctx.Done():
				return fmt.Errorf("context done: %w", ctx.Err())
			case <-f.resetCh:
			case <-f.pressCh:
				st = stateRegistrationQuery
				f.state.Store(st)

				f.drainPress()
				f.drainAnswers()
				f.drainConversations(conversations)

				f.drainResetBurst()
				sipID, newIP, err := doPress(ctx, f.logger, f.cfg, f.sendQueryOnce, f.answers, f.resetCh)
				if err != nil {
					if ctx.Err() != nil {
						return fmt.Errorf("context done: %w", ctx.Err())
					}
					f.logger.Warn("press failed", "err", err)
					st = stateRegistrationIdle
					f.state.Store(st)
					continue
				}

				st = sipCallOrchestrator{f: f}.run(ctx, sipID, newIP, conversations)
			}

		default:
			f.logger.Warn("unexpected bes FSM state, resetting to registration idle", "state", st)
			besPanicOnUnexpectedFSMState(st)
			st = stateRegistrationIdle
			f.state.Store(st)
		}
	}

	return fmt.Errorf("context done: %w", ctx.Err())
}

func (f *besFSM) drainAnswers() {
	for {
		select {
		case <-f.answers:
		default:
			return
		}
	}
}

func (f *besFSM) drainConversations(conversations <-chan string) {
	for {
		select {
		case <-conversations:
		default:
			return
		}
	}
}

func (f *besFSM) drainPress() {
	for {
		select {
		case <-f.pressCh:
		default:
			return
		}
	}
}

// drainResetBurst drops coalesced reset signals already in resetCh.
// The channel is never closed; one wake from select plus this drain equals one logical reset burst.
func (f *besFSM) drainResetBurst() {
	for {
		select {
		case <-f.resetCh:
		default:
			return
		}
	}
}

func firstHardwareMAC() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	sort.Slice(ifaces, func(i, j int) bool { return ifaces[i].Name < ifaces[j].Name })
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

func DefaultRunSIP(
	ctx context.Context,
	logger Logger,
	cfg config.Bes,
	domain string,
	sipID string,
	conversations <-chan string,
	dialEstablished chan<- struct{},
) error {
	if d := strings.TrimSpace(cfg.SIPDomain); d != "" {
		domain = d
	}
	besUser := strings.TrimSpace(cfg.SIPUserBes)
	bucisUser := strings.TrimSpace(cfg.SIPUserBucis)
	sipPort := cfg.SIPPort
	if sipPort == 0 {
		sipPort = 5060
	}
	if sipPort < 1 || sipPort > 65535 {
		return fmt.Errorf("SIP_PORT invalid: %d out of range 1..65535", sipPort)
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
	besPass := cfg.SIPPassBes

	ua, err := sipgo.NewUA(
		sipgo.WithUserAgent(besUser),
		sipgo.WithUserAgentHostname(domain),
	)
	if err != nil {
		return fmt.Errorf("sip ua init: %w", err)
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

	callSetupTimeout := cfg.EC.CallSetupTimeout
	if callSetupTimeout <= 0 {
		callSetupTimeout = 15 * time.Second
	}

	callSetupDeadline := time.Now().Add(callSetupTimeout)
	dialCtx, cancelDial := context.WithDeadline(ctx, callSetupDeadline)
	defer cancelDial()
	dialog, err := phone.Dial(dialCtx, sip.Uri{User: bucisUser, Host: domain, Port: sipPort}, sipgox.DialOptions{
		Username: besUser,
		Password: besPass,
		Formats:  sdp.NewFormats(strconv.Itoa(int(rtp.PayloadTypeG726()))),
	})
	if err != nil {
		return fmt.Errorf("sip dial: %w", err)
	}
	defer func() { _ = dialog.Close() }()
	logger.Info("sip call established", "to", bucisUser, "domain", domain)

	stopRTP := func() {}
	hangupAndWait := func() {
		hangupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := dialog.Hangup(hangupCtx); err != nil {
			logger.Warn("sip hangup failed", "err", err)
			_ = dialog.Close()
		}
	}
	defer func() { stopRTP() }()

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
		// Take the conn before Close() so there is no window where lport is free
		// and can be grabbed by another process.
		rtpConn := dialog.TakeRTPConn()
		dialog.MediaSession.Close()

		rtpCtx, cancelRTP := context.WithCancel(ctx)
		var cleanup func()
		var rtpErr error
		if rtpConn != nil {
			cleanup, _, rtpErr = rtpsession.StartFromConn(rtpCtx, logger, rtpConn, raddr)
		} else {
			cleanup, _, rtpErr = rtpsession.Start(rtpCtx, logger, lport, raddr)
		}

		var stopOnce sync.Once
		stopRTP = func() {
			stopOnce.Do(func() {
				cancelRTP()
				if cleanup != nil {
					cleanup()
				}
			})
		}
		hangupAndWait = func() {
			hangupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := dialog.Hangup(hangupCtx); err != nil {
				logger.Warn("sip hangup failed", "err", err)
				_ = dialog.Close()
			}
			stopRTP()
		}

		if rtpErr != nil {
			stopRTP()
			hangupAndWait()
			return fmt.Errorf("rtp session start: %w", rtpErr)
		}
	} else {
		logger.Warn("rtp skipped: missing sdp addresses")
	}

	if dialEstablished != nil {
		select {
		case dialEstablished <- struct{}{}:
		default:
		}
	}

	applyConversationDurationLimit := sipID == ""
	if sipID != "" {
		waitConvCtx, cancelWaitConv := context.WithDeadline(ctx, callSetupDeadline)
		defer cancelWaitConv()
		for {
			select {
			case <-waitConvCtx.Done():
				if errors.Is(waitConvCtx.Err(), context.DeadlineExceeded) {
					logger.Warn("ec_client_conversation timeout", "sip_id", sipID, "timeout", callSetupTimeout.String())
					goto afterConversation
				}
				hangupAndWait()
				return fmt.Errorf("context done: %w", waitConvCtx.Err())
			case <-dialog.Context().Done():
				stopRTP()
				return nil
			case got := <-conversations:
				if got != sipID {
					continue
				}
				applyConversationDurationLimit = true
				logger.Info("conversation confirmed via ec", "sip_id", sipID)
				goto afterConversation
			}
		}
	}

afterConversation:
	convLimit := cfg.EC.ConversationTimeout
	if applyConversationDurationLimit && convLimit > 0 {
		lim := time.NewTimer(convLimit)
		defer lim.Stop()
		select {
		case <-ctx.Done():
			hangupAndWait()
			return fmt.Errorf("context done: %w", ctx.Err())
		case <-dialog.Context().Done():
			stopRTP()
			return nil
		case <-lim.C:
			logger.Info("conversation duration limit reached", "timeout", convLimit.String())
			hangupAndWait()
			return nil
		}
	}
	select {
	case <-ctx.Done():
		hangupAndWait()
		return fmt.Errorf("context done: %w", ctx.Err())
	case <-dialog.Context().Done():
		stopRTP()
		return nil
	}
}
