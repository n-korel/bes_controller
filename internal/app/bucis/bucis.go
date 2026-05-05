package bucis

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
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
	"github.com/rs/zerolog"
)

var (
	startRTPSession         = rtpsession.Start
	startRTPSessionFromConn = rtpsession.StartFromConn
)

type Logger interface {
	Info(string, ...any)
	Warn(string, ...any)
	Debug(string, ...any)
}

type QueryEvent struct {
	MAC      string
	FromIP   string
	FromPort int
	Port     int
}

type AnswerEvent struct {
	MAC     string
	SipID   string
	NewIP   string
	DstIP   string
	DstPort int
}

type Options struct {
	BesBroadcastAddr    string
	BesAddrOverride     string
	OpenSIPSIP          string
	ClientResetInterval time.Duration
	KeepAliveInterval   time.Duration
	Head1IP             string
	Head2IP             string

	OnQueryReceived chan<- QueryEvent
	OnAnswerSent    chan<- AnswerEvent
}

type BUCIS struct {
	logger    Logger
	cfg       *config.Bucis
	sipIDs    *sipIDState
	localIP   string
	sipDomain string
	sendTo    func(ip string, port int, payload string) error
}

func Run(ctx context.Context, logger Logger, opts Options) error {
	cfg, err := config.ParseBucis()
	if err != nil {
		return fmt.Errorf("parse bucis config: %w", err)
	}
	applyECOverrides(&cfg, opts)

	localIP, ok := firstNonLoopbackIPv4()
	if !ok {
		logger.Warn("no non-loopback ipv4 found; using loopback fallback", "ip", "127.0.0.1")
		localIP = "127.0.0.1"
	}
	head1 := strings.TrimSpace(opts.Head1IP)
	head2 := strings.TrimSpace(opts.Head2IP)

	if head1 == "" {
		head1 = strings.TrimSpace(os.Getenv("EC_BUCIS_HEAD1_IP"))
	}
	if head2 == "" {
		head2 = strings.TrimSpace(os.Getenv("EC_BUCIS_HEAD2_IP"))
	}
	if head1 == "" {
		head1 = localIP
	}
	if head2 == "" {
		head2 = head1
	}

	sipDomain := strings.TrimSpace(os.Getenv("SIP_DOMAIN"))
	if sipDomain == "" {
		sipDomain = localIP
	}
	opensipsIP := strings.TrimSpace(opts.OpenSIPSIP)
	if opensipsIP == "" {
		opensipsIP = sipDomain
	}

	logger.Info("ec started",
		"listen_6710", cfg.EC.QueryPort6710,
		"listen_7777", cfg.EC.QueryPort7777,
		"send_8890_broadcast", cfg.EC.BesBroadcastAddr,
		"send_8890_override", cfg.EC.BesAddrOverride,
		"sip_id_modulo", cfg.EC.SipIDModulo,
		"sip_domain", sipDomain,
		"opensips_ip", opensipsIP,
		"reset_interval", cfg.EC.ClientResetInterval.String(),
		"keepalive_interval", cfg.EC.KeepAliveInterval.String(),
	)

	sipIDs := newSipIDState(cfg.EC.SipIDModulo)

	outConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return fmt.Errorf("udp outbound listen: %w", err)
	}
	defer func() { _ = outConn.Close() }()

	var sendMu sync.Mutex
	sendTo := func(ip string, port int, payload string) error {
		raddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(ip, strconv.Itoa(port)))
		if err != nil {
			return fmt.Errorf("resolve udp addr %s:%d: %w", ip, port, err)
		}
		sendMu.Lock()
		defer sendMu.Unlock()
		// Важно: broadcast (255.255.255.255) требует отдельной настройки сокета на Linux.
		// На Windows broadcast для BES отключён: используйте unicast (см. .env.bes.windows / EC_BES_ADDR_OVERRIDE).
		if isIPv4LimitedBroadcast(raddr.IP) {
			if err := enableUDPBroadcast(outConn); err != nil {
				return fmt.Errorf("enable udp broadcast: %w", err)
			}
		}
		_ = outConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		_, err = outConn.WriteTo([]byte(payload), raddr)
		if err != nil {
			return fmt.Errorf("udp write %s:%d: %w", ip, port, err)
		}
		return nil
	}

	sendReset := func() {
		dst := chooseECdst(cfg.EC.BesBroadcastAddr, cfg.EC.BesAddrOverride)
		msg := protocol.FormatClientReset(head1, head2)
		if err := sendTo(dst, cfg.EC.ListenPort8890, msg); err != nil {
			logger.Warn("ec_client_reset send failed", "err", err, "dst", dst)
			return
		}
		logger.Info("ec_client_reset sent", "dst", dst, "head1", head1, "head2", head2)
	}

	sendKeepAlive := func() {
		dst := chooseECdst(cfg.EC.BesBroadcastAddr, cfg.EC.BesAddrOverride)
		msg := protocol.FormatKeepAlive(opensipsIP, "0")
		if err := sendTo(dst, cfg.EC.ListenPort8890, msg); err != nil {
			logger.Warn("ec_server_keepalive send failed", "err", err, "dst", dst)
			return
		}
		logger.Debug("ec_server_keepalive sent", "dst", dst, "opensips_ip", opensipsIP, "status", "0")
	}

	sendReset()
	sendKeepAlive()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(cfg.EC.ClientResetInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sendReset()
			}
		}
	}()
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(cfg.EC.KeepAliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sendKeepAlive()
			}
		}
	}()

	// SIP (iteration B): register and answer incoming calls via OpenSIPS.
	b := &BUCIS{
		logger:    logger,
		cfg:       &cfg,
		sipIDs:    sipIDs,
		localIP:   localIP,
		sipDomain: sipDomain,
		sendTo:    sendTo,
	}
	_ = b.initSIP(ctx, &wg)

	listenQuery := func(port int) (*net.UDPConn, error) {
		return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	}
	c6710, err := listenQuery(cfg.EC.QueryPort6710)
	if err != nil {
		return fmt.Errorf("listen query port %d: %w", cfg.EC.QueryPort6710, err)
	}
	defer func() { _ = c6710.Close() }()
	c7777, err := listenQuery(cfg.EC.QueryPort7777)
	if err != nil {
		return fmt.Errorf("listen query port %d: %w", cfg.EC.QueryPort7777, err)
	}
	defer func() { _ = c7777.Close() }()

	handleConn := func(conn *net.UDPConn, port int) {
		buf := make([]byte, 2048)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, raddr, err := conn.ReadFromUDP(buf)
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
				logger.Warn("udp read failed", "err", err, "port", port)
				continue
			}

			pkt, ok := protocol.Parse(buf[:n])
			if !ok || pkt.Type != protocol.ECPacketClientQuery || pkt.Query == nil {
				continue
			}

			if opts.OnQueryReceived != nil {
				ev := QueryEvent{MAC: pkt.Query.MAC, FromIP: raddr.IP.String(), FromPort: raddr.Port, Port: port}
				select {
				case opts.OnQueryReceived <- ev:
				default:
				}
			}

			sipID := sipIDs.GetOrCreate(pkt.Query.MAC)

			senderIP := raddr.IP.String()

			dst := senderIP
			if cfg.EC.BesAddrOverride != "" {
				dst = cfg.EC.BesAddrOverride
			}
			// В реестре храним адрес назначения для последующих ClientConversation.
			// Если включен EC_BES_ADDR_OVERRIDE, то и ClientAnswer, и ClientConversation должны идти на override.
			sipIDs.SetIP(sipID, dst)
			msg := protocol.FormatClientAnswer(opensipsIP, sipID)
			if err := sendTo(dst, cfg.EC.ListenPort8890, msg); err != nil {
				logger.Warn("ec_client_answer send failed", "err", err, "dst", dst, "sip_id", sipID, "mac", pkt.Query.MAC)
				continue
			}

			if opts.OnAnswerSent != nil {
				ev := AnswerEvent{
					MAC: pkt.Query.MAC, SipID: sipID, NewIP: opensipsIP,
					DstIP: dst, DstPort: cfg.EC.ListenPort8890,
				}
				select {
				case opts.OnAnswerSent <- ev:
				default:
				}
			}

			logger.Info("ec_client_answer sent", "dst", dst, "sip_id", sipID, "mac", pkt.Query.MAC, "query_port", port)
		}
	}

	wg.Add(2)
	go func() { defer wg.Done(); handleConn(c6710, cfg.EC.QueryPort6710) }()
	go func() { defer wg.Done(); handleConn(c7777, cfg.EC.QueryPort7777) }()

	<-ctx.Done()
	wg.Wait()
	return nil
}

func applyECOverrides(cfg *config.Bucis, opts Options) {
	if strings.TrimSpace(opts.BesBroadcastAddr) != "" {
		cfg.EC.BesBroadcastAddr = strings.TrimSpace(opts.BesBroadcastAddr)
	}
	if strings.TrimSpace(opts.BesAddrOverride) != "" {
		cfg.EC.BesAddrOverride = strings.TrimSpace(opts.BesAddrOverride)
	}
	if opts.ClientResetInterval > 0 {
		cfg.EC.ClientResetInterval = opts.ClientResetInterval
	}
	if opts.KeepAliveInterval > 0 {
		cfg.EC.KeepAliveInterval = opts.KeepAliveInterval
	}
}

func chooseECdst(broadcastAddr, overrideAddr string) string {
	if overrideAddr != "" {
		return overrideAddr
	}
	return broadcastAddr
}

func isIPv4LimitedBroadcast(ip net.IP) bool {
	return ip != nil && ip.To4() != nil && ip.Equal(net.IPv4bcast)
}

func normalizeSIPID(fromUser string) string {
	u := strings.TrimSpace(fromUser)
	if strings.EqualFold(u, "bes") {
		// SIP_USER_BES задан явно как общий логин (не bes_<sipId>): sipId берём из реестра по IP отправителя.
		return ""
	}
	if strings.HasPrefix(u, "bes_") {
		return strings.TrimPrefix(u, "bes_")
	}
	return u
}

func hostFromSIPSource(src string) string {
	host := strings.TrimSpace(src)
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		return h
	}
	return host
}

func (b *BUCIS) handleAnsweredDialog(ctx context.Context, dialog *sipgox.DialogServerSession) (exitAnswerLoop bool) {
	b.logger.Info("sip call answered")

	var (
		callCancel context.CancelFunc
		cleanup    func()
	)
	defer func() {
		if callCancel != nil {
			callCancel()
		}
		if cleanup != nil {
			cleanup()
		}
	}()

	if dialog != nil && dialog.MediaSession != nil && dialog.Laddr != nil && dialog.Raddr != nil {
		lport := dialog.Laddr.Port
		raddr := dialog.Raddr
		b.logger.Info("rtp negotiated",
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

		{
			callCtx, cancel := context.WithCancel(dialog.Context())
			callCancel = cancel
			var cl func()
			var streamDone <-chan struct{}
			var rtpErr error
			if rtpConn != nil {
				cl, streamDone, rtpErr = startRTPSessionFromConn(callCtx, b.logger, rtpConn, raddr)
			} else {
				cl, streamDone, rtpErr = startRTPSession(callCtx, b.logger, lport, raddr)
			}
			if rtpErr != nil {
				return ctx.Err() != nil
			}
			cleanup = cl
			go func(done <-chan struct{}, stop context.CancelFunc, ctxDone <-chan struct{}) {
				select {
				case <-done:
					stop()
				case <-ctxDone:
				}
			}(streamDone, cancel, callCtx.Done())
		}
	} else {
		b.logger.Warn("rtp skipped: missing sdp addresses")
	}

	var sipID string
	if dialog != nil && dialog.InviteRequest != nil && dialog.InviteRequest.From() != nil {
		sipID = normalizeSIPID(dialog.InviteRequest.From().Address.User)
	}

	var (
		besIP string
		ok    bool
	)
	if sipID != "" {
		besIP, ok = b.sipIDs.GetIP(sipID)
	}
	if !ok && dialog != nil && dialog.InviteRequest != nil {
		host := hostFromSIPSource(dialog.InviteRequest.Source())
		if host != "" {
			if s, found := b.sipIDs.FindBySenderIP(host); found {
				sipID = s
				besIP = host
				ok = true
			}
		}
	}
	if !ok || sipID == "" || besIP == "" {
		b.logger.Warn("ec_client_conversation skipped: cannot resolve sip_id/bes_ip", "sip_id", sipID)
		select {
		case <-ctx.Done():
			return true
		case <-dialog.Context().Done():
		}
		return false
	}

	msg := protocol.FormatClientConversation(sipID)
	if err := b.sendTo(besIP, b.cfg.EC.ListenPort8890, msg); err != nil {
		b.logger.Warn("ec_client_conversation send failed", "err", err, "dst", besIP, "sip_id", sipID)
		select {
		case <-ctx.Done():
			return true
		case <-dialog.Context().Done():
		}
		return false
	}
	b.logger.Info("ec_client_conversation sent", "dst", besIP, "sip_id", sipID)

	select {
	case <-ctx.Done():
		return true
	case <-dialog.Context().Done():
	}
	return false
}

func (b *BUCIS) initSIP(ctx context.Context, wg *sync.WaitGroup) bool {
	cfg, ok := readSIPInitConfig(b.logger)
	if !ok {
		b.logger.Info("sip disabled: SIP_USER_BUCIS not set")
		return false
	}

	ua, err := sipgo.NewUA(
		sipgo.WithUserAgent(cfg.user),
		sipgo.WithUserAgentHostname(b.sipDomain),
	)
	if err != nil {
		b.logger.Warn("sip ua init failed", "err", err)
		return false
	}

	remote := sip.Uri{Host: b.sipDomain, Port: cfg.port}
	network := remote.Headers.GetOr("transport", "udp")
	if network != "udp" {
		b.logger.Warn("unsupported sip transport for answer", "network", network)
		_ = ua.Close()
		return false
	}

	lhost := b.localIP
	if ip := net.ParseIP(b.sipDomain); ip != nil && ip.IsLoopback() {
		lhost = "127.0.0.1"
	}

	tmpConn, lport, err := reserveUDPPort(lhost)
	if err != nil {
		b.logger.Warn("sip register: failed to reserve local port", "err", err)
		_ = ua.Close()
		return false
	}

	// Важно: держим порт до момента, когда sipgox.Phone начнет слушать.
	// Иначе возможна гонка между закрытием сокета регистрации и Listen на том же lport.
	phone := sipgox.NewPhone(ua, sipgox.WithPhoneListenAddr(sipgox.ListenAddr{
		Network: network,
		Addr:    net.JoinHostPort(lhost, strconv.Itoa(lport)),
	}), sipgox.WithPhoneUDPConn(tmpConn))

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer phone.Close()
		defer func() { _ = ua.Close() }()

		// Один REGISTER (200 OK), затем Answer().
		if !b.registerSIP(ctx, ua, remote, network, lhost, lport, cfg) {
			return
		}

		for {
			if ctx.Err() != nil {
				return
			}
			dialog, err := phone.Answer(ctx, sipgox.AnswerOptions{
				Formats: sdp.NewFormats(strconv.Itoa(int(rtp.PayloadTypeG726()))),
			})
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				b.logger.Warn("sip answer failed", "err", err)
				time.Sleep(500 * time.Millisecond)
				continue
			}
			if b.handleAnsweredDialog(ctx, dialog) {
				return
			}
		}
	}()

	return true
}

type sipInitConfig struct {
	user                 string
	pass                 string
	port                 int
	registerTotalTimeout time.Duration
}

func readSIPInitConfig(logger Logger) (sipInitConfig, bool) {
	user := strings.TrimSpace(os.Getenv("SIP_USER_BUCIS"))
	if user == "" {
		return sipInitConfig{}, false
	}
	pass := os.Getenv("SIP_PASS_BUCIS")

	port := 5060
	if v := strings.TrimSpace(os.Getenv("SIP_PORT")); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	}

	registerTotalTimeout := 30 * time.Second
	if v := strings.TrimSpace(os.Getenv("SIP_REGISTER_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			registerTotalTimeout = d
		} else {
			logger.Warn("sip register: invalid SIP_REGISTER_TIMEOUT, using default", "value", v, "err", err, "default", registerTotalTimeout.String())
		}
	}
	if registerTotalTimeout <= 0 {
		registerTotalTimeout = 30 * time.Second
	}

	return sipInitConfig{
		user:                 user,
		pass:                 pass,
		port:                 port,
		registerTotalTimeout: registerTotalTimeout,
	}, true
}

func reserveUDPPort(host string) (*net.UDPConn, int, error) {
	laddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, 0, fmt.Errorf("resolve udp addr: %w", err)
	}
	conn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		return nil, 0, fmt.Errorf("listen udp: %w", err)
	}
	la := conn.LocalAddr()
	ludp, ok := la.(*net.UDPAddr)
	if !ok || ludp == nil {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("unexpected local addr type: %T", la)
	}
	return conn, ludp.Port, nil
}

func (b *BUCIS) registerSIP(
	ctx context.Context,
	ua *sipgo.UserAgent,
	remote sip.Uri,
	network string,
	lhost string,
	lport int,
	cfg sipInitConfig,
) bool {
	regClient, err := sipgo.NewClient(ua,
		sipgo.WithClientHostname(lhost),
		sipgo.WithClientNAT(),
	)
	if err != nil {
		b.logger.Warn("sip register: client init failed", "err", err)
		return false
	}
	defer func() {
		if err := regClient.Close(); err != nil {
			b.logger.Warn("sip register: client close failed", "err", err)
		}
	}()

	contactHdr := sip.ContactHeader{
		Address: sip.Uri{
			User:      cfg.user,
			Host:      lhost,
			Port:      lport,
			Headers:   sip.HeaderParams{{K: "transport", V: network}},
			UriParams: sip.NewParams(),
		},
		Params: sip.NewParams(),
	}

	zlog := zerolog.New(os.Stdout)
	regTx := sipgox.NewRegisterTransaction(zlog, regClient, remote, contactHdr, sipgox.RegisterOptions{
		Username: cfg.user,
		Password: cfg.pass,
		Expiry:   3600,
	})

	regCtx, regCancel := context.WithTimeout(ctx, cfg.registerTotalTimeout)
	defer regCancel()

	for {
		if regCtx.Err() != nil {
			b.logger.Warn("sip register: giving up", "err", regCtx.Err(), "timeout", cfg.registerTotalTimeout.String())
			return false
		}

		regAttemptCtx, cancel := context.WithTimeout(regCtx, 5*time.Second)
		err := regTx.Register(regAttemptCtx)
		cancel()
		if err == nil {
			b.logger.Info("sip registered", "domain", b.sipDomain, "port", cfg.port, "user", cfg.user)
			return true
		}

		b.logger.Warn("sip register failed", "err", err)
		timer := time.NewTimer(1 * time.Second)
		select {
		case <-regCtx.Done():
			timer.Stop()
			b.logger.Warn("sip register: giving up", "err", regCtx.Err(), "timeout", cfg.registerTotalTimeout.String())
			return false
		case <-timer.C:
		}
	}
}

func firstNonLoopbackIPv4() (ip string, ok bool) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", false
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet == nil || ipnet.IP == nil {
				continue
			}
			if v4 := ipnet.IP.To4(); v4 != nil {
				return v4.String(), true
			}
		}
	}
	return "", false
}

func ValidateConfigForTests() error {
	// Быстрый sanity-check, чтобы тесты падали понятнее при ошибочной среде.
	if _, err := config.ParseBucis(); err != nil {
		return fmt.Errorf("ParseBucis: %w", err)
	}
	return nil
}
