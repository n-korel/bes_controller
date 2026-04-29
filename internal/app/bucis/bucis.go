package bucis

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	MAC    string
	SipID  string
	NewIP  string
	DstIP  string
	DstPort int
}

type Options struct {
	BesBroadcastAddr string
	BesAddrOverride  string
	OpenSIPSIP       string
	ClientResetInterval time.Duration
	KeepAliveInterval   time.Duration
	Head1IP             string
	Head2IP             string

	OnQueryReceived chan<- QueryEvent
	OnAnswerSent    chan<- AnswerEvent
}

func Run(ctx context.Context, logger Logger, opts Options) error {
	cfg, err := config.ParseBucis()
	if err != nil {
		return err
	}
	applyECOverrides(&cfg, opts)

	localIP := firstNonLoopbackIPv4()
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
		"sip_domain", sipDomain,
		"opensips_ip", opensipsIP,
		"reset_interval", cfg.EC.ClientResetInterval.String(),
		"keepalive_interval", cfg.EC.KeepAliveInterval.String(),
	)

	sipIDs := newSipIDState()

	sendTo := func(ip string, port int, payload string) error {
		raddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(ip, strconv.Itoa(port)))
		if err != nil {
			return err
		}
		conn, err := net.DialUDP("udp4", nil, raddr)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		// Linux требует SO_BROADCAST для отправки на broadcast-адрес (иначе EACCES).
		_ = enableUDPBroadcast(conn)
		_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		_, err = conn.Write([]byte(payload))
		return err
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
	_ = initSIP(ctx, logger, &cfg, &sipIDs, localIP, sipDomain, sendTo, &wg)

	listenQuery := func(port int) (*net.UDPConn, error) {
		return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	}
	c6710, err := listenQuery(cfg.EC.QueryPort6710)
	if err != nil {
		return err
	}
	defer func() { _ = c6710.Close() }()
	c7777, err := listenQuery(cfg.EC.QueryPort7777)
	if err != nil {
		return err
	}
	defer func() { _ = c7777.Close() }()

	handleConn := func(conn *net.UDPConn, port int) {
		buf := make([]byte, 2048)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, raddr, err := conn.ReadFromUDP(buf)
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

			sipID := sipIDs.getSipID(pkt.Query.MAC)

			// Для ClientConversation нам нужен реальный IP источника (кто прислал ClientQuery),
			// иначе при dev-override адрес будет "просачиваться" дальше и ломать LAN-сценарий.
			senderIP := raddr.IP.String()

			dst := senderIP
			if cfg.EC.BesAddrOverride != "" {
				dst = cfg.EC.BesAddrOverride
			}
			sipIDs.setSipIDIP(sipID, senderIP)
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

type sipIDState struct {
	mu  sync.Mutex
	seq int
	m   map[string]string // mac -> sipId
	ip  map[string]string // sipId -> bes ip
}

func newSipIDState() sipIDState {
	return sipIDState{m: make(map[string]string), ip: make(map[string]string)}
}

func (s *sipIDState) getSipID(mac string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[mac]; ok {
		return v
	}
	s.seq++
	v := strconv.Itoa(s.seq)
	s.m[mac] = v
	return v
}

func (s *sipIDState) setSipIDIP(sipID string, besIP string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sipID == "" || besIP == "" {
		return
	}
	s.ip[sipID] = besIP
}

func (s *sipIDState) getSipIDIP(sipID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.ip[sipID]
	return v, ok
}

func (s *sipIDState) getSipIDByIP(besIP string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sipID, ip := range s.ip {
		if ip == besIP {
			return sipID, true
		}
	}
	return "", false
}

func chooseECdst(broadcastAddr, overrideAddr string) string {
	if overrideAddr != "" {
		return overrideAddr
	}
	return broadcastAddr
}

func enableUDPBroadcast(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return serr
}

func initSIP(
	ctx context.Context,
	logger Logger,
	cfg *config.Bucis,
	sipIDs *sipIDState,
	localIP string,
	sipDomain string,
	sendTo func(ip string, port int, payload string) error,
	wg *sync.WaitGroup,
) bool {
	bucisUser := strings.TrimSpace(os.Getenv("SIP_USER_BUCIS"))
	bucisPass := os.Getenv("SIP_PASS_BUCIS")
	if bucisUser == "" {
		logger.Info("sip disabled: SIP_USER_BUCIS not set")
		return false
	}

	sipPort := 5060
	if v := strings.TrimSpace(os.Getenv("SIP_PORT")); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			sipPort = p
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

	ua, err := sipgo.NewUA(
		sipgo.WithUserAgent(bucisUser),
		sipgo.WithUserAgentHostname(sipDomain),
	)
	if err != nil {
		logger.Warn("sip ua init failed", "err", err)
		return false
	}

	remote := sip.Uri{Host: sipDomain, Port: sipPort}
	network := remote.Headers.GetOr("transport", "udp")
	if network != "udp" {
		logger.Warn("unsupported sip transport for answer", "network", network)
		_ = ua.Close()
		return false
	}

	lhost := localIP
	if ip := net.ParseIP(sipDomain); ip != nil && ip.IsLoopback() {
		lhost = "127.0.0.1"
	}

	laddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(lhost, "0"))
	if err != nil {
		logger.Warn("sip register: failed to resolve local addr", "err", err)
		_ = ua.Close()
		return false
	}
	tmpConn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		logger.Warn("sip register: failed to reserve local port", "err", err)
		_ = ua.Close()
		return false
	}
	lport := tmpConn.LocalAddr().(*net.UDPAddr).Port

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
		{
			regClient, err := sipgo.NewClient(ua,
				sipgo.WithClientHostname(lhost),
				sipgo.WithClientNAT(),
			)
			if err != nil {
				logger.Warn("sip register: client init failed", "err", err)
				return
			}

			contactHdr := sip.ContactHeader{
				Address: sip.Uri{
					User:      bucisUser,
					Host:      lhost,
					Port:      lport,
					Headers:   sip.HeaderParams{{K: "transport", V: network}},
					UriParams: sip.NewParams(),
				},
				Params: sip.NewParams(),
			}

			zlog := zerolog.New(os.Stdout)
			regTx := sipgox.NewRegisterTransaction(zlog, regClient, remote, contactHdr, sipgox.RegisterOptions{
				Username: bucisUser,
				Password: bucisPass,
				Expiry:   3600,
			})

			regCtx, regCancel := context.WithTimeout(ctx, registerTotalTimeout)
			defer regCancel()

			for {
				if regCtx.Err() != nil {
					logger.Warn("sip register: giving up", "err", regCtx.Err(), "timeout", registerTotalTimeout.String())
					return
				}

				regAttemptCtx, cancel := context.WithTimeout(regCtx, 5*time.Second)
				err := regTx.Register(regAttemptCtx)
				cancel()
				if err == nil {
					logger.Info("sip registered", "domain", sipDomain, "port", sipPort, "user", bucisUser)
					break
				}

				logger.Warn("sip register failed", "err", err)
				timer := time.NewTimer(1 * time.Second)
				select {
				case <-regCtx.Done():
					timer.Stop()
					logger.Warn("sip register: giving up", "err", regCtx.Err(), "timeout", registerTotalTimeout.String())
					return
				case <-timer.C:
				}
			}
			if err := regClient.Close(); err != nil {
				logger.Warn("sip register: client close failed", "err", err)
			}
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
				logger.Warn("sip answer failed", "err", err)
				time.Sleep(500 * time.Millisecond)
				continue
			}
			logger.Info("sip call answered")

			var callCtx context.Context
			var cancel context.CancelFunc

			if dialog != nil && dialog.MediaSession != nil && dialog.Laddr != nil && dialog.Raddr != nil {
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
				dialog.MediaSession.Close()

				callCtx, cancel = context.WithCancel(dialog.Context())
				defer cancel()
				cleanup, streamDone, rtpErr := rtpsession.Start(callCtx, logger, lport, raddr)
				if cleanup != nil {
					defer cleanup()
				}
				if rtpErr != nil && cleanup == nil {
					cancel()
				} else if rtpErr == nil {
					go func() { <-streamDone; cancel() }()
				}
			} else {
				logger.Warn("rtp skipped: missing sdp addresses")
			}

			var sipID string
			if dialog != nil && dialog.InviteRequest != nil && dialog.InviteRequest.From() != nil {
				u := strings.TrimSpace(dialog.InviteRequest.From().Address.User)
				if strings.HasPrefix(u, "bes_") {
					sipID = strings.TrimPrefix(u, "bes_")
				} else {
					sipID = u
				}
			}

			var (
				besIP string
				ok    bool
			)
			if sipID != "" {
				besIP, ok = sipIDs.getSipIDIP(sipID)
			}
			if !ok && dialog != nil && dialog.InviteRequest != nil {
				src := strings.TrimSpace(dialog.InviteRequest.Source())
				host := src
				if h, _, err := net.SplitHostPort(src); err == nil && h != "" {
					host = h
				}
				if host != "" {
					if s, found := sipIDs.getSipIDByIP(host); found {
						sipID = s
						besIP = host
						ok = true
					}
				}
			}
			if !ok || sipID == "" || besIP == "" {
				logger.Warn("ec_client_conversation skipped: cannot resolve sip_id/bes_ip", "sip_id", sipID)
				select {
				case <-ctx.Done():
					return
				case <-dialog.Context().Done():
				}
				continue
			}

			msg := protocol.FormatClientConversation(sipID)
			if err := sendTo(besIP, cfg.EC.ListenPort8890, msg); err != nil {
				logger.Warn("ec_client_conversation send failed", "err", err, "dst", besIP, "sip_id", sipID)
				continue
			}
			logger.Info("ec_client_conversation sent", "dst", besIP, "sip_id", sipID)

			select {
			case <-ctx.Done():
				return
			case <-dialog.Context().Done():
			}
		}
	}()

	return true
}

func firstNonLoopbackIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
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
				return v4.String()
			}
		}
	}
	return "127.0.0.1"
}

func ValidateConfigForTests() error {
	// Быстрый sanity-check, чтобы тесты падали понятнее при ошибочной среде.
	if _, err := config.ParseBucis(); err != nil {
		return fmt.Errorf("ParseBucis: %w", err)
	}
	return nil
}

