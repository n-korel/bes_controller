package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
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
	"bucis-bes_simulator/pkg/log"

	"github.com/emiago/media/sdp"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgox"
	"github.com/rs/zerolog"
)

func main() {
	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bucis")

	var (
		besAddrOverride string
		besBroadcast    string
		opensipsIP      string
		resetInterval   time.Duration
		keepAliveInt    time.Duration

		head1 string
		head2 string
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&besBroadcast, "ec-bes-broadcast-addr", "", "EC destination broadcast address for reset/keepalive (default from EC_BES_BROADCAST_ADDR)")
	fs.StringVar(&besAddrOverride, "ec-bes-addr", "", "EC destination override for reset/keepalive (unicast) (default from EC_BES_ADDR)")
	fs.StringVar(&opensipsIP, "ec-opensips-ip", "", "OpenSIPS IP to put into keepalive (default to local IP)")
	fs.DurationVar(&resetInterval, "ec-reset-interval", 0, "ClientReset interval (default from EC_CLIENT_RESET_INTERVAL)")
	fs.DurationVar(&keepAliveInt, "ec-keepalive-interval", 0, "KeepAlive interval (default from EC_KEEPALIVE_INTERVAL)")
	fs.StringVar(&head1, "ec-head1-ip", "", "ClientReset head1 IP (default: local IP)")
	fs.StringVar(&head2, "ec-head2-ip", "", "ClientReset head2 IP (default: head1)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := run(ctx, logger, fs, besBroadcast, besAddrOverride, opensipsIP, resetInterval, keepAliveInt, head1, head2); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	logger interface {
		Info(string, ...any)
		Warn(string, ...any)
		Debug(string, ...any)
	},
	fs *flag.FlagSet,
	besBroadcast string,
	besAddrOverride string,
	opensipsIP string,
	resetInterval time.Duration,
	keepAliveInt time.Duration,
	head1 string,
	head2 string,
) error {
	cfg, err := config.ParseBucis()
	if err != nil {
		return err
	}
	applyECFlagOverrides(fs, &cfg, besBroadcast, besAddrOverride, resetInterval, keepAliveInt)
	localIP := firstNonLoopbackIPv4()

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
	if opensipsIP == "" {
		opensipsIP = sipDomain
	}

	logger.Info("ec started",
		"listen_6710", cfg.EC.QueryPort6710,
		"listen_7777", cfg.EC.QueryPort7777,
		"send_8890_broadcast", cfg.EC.BesBroadcastAddr,
		"send_8890_override", cfg.EC.BesAddrOverride,
		"sip_domain", sipDomain,
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

			_, query, _, _, _, ok := protocol.Parse(buf[:n])
			if !ok || query == nil {
				continue
			}
			sipID := sipIDs.getSipID(query.MAC)

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
				logger.Warn("ec_client_answer send failed", "err", err, "dst", dst, "sip_id", sipID, "mac", query.MAC)
				continue
			}
			logger.Info("ec_client_answer sent", "dst", dst, "sip_id", sipID, "mac", query.MAC, "query_port", port)
		}
	}

	wg.Add(2)
	go func() { defer wg.Done(); handleConn(c6710, cfg.EC.QueryPort6710) }()
	go func() { defer wg.Done(); handleConn(c7777, cfg.EC.QueryPort7777) }()

	<-ctx.Done()
	wg.Wait()
	return nil
}

func initSIP(
	ctx context.Context,
	logger interface {
		Info(string, ...any)
		Warn(string, ...any)
		Debug(string, ...any)
	},
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
	}))

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer phone.Close()
		defer func() { _ = ua.Close() }()
		tmpConnClosed := false
		defer func() {
			if !tmpConnClosed {
				_ = tmpConn.Close()
			}
		}()

		// План: "зарегистрироваться до ожидания INVITE".
		// В sipgox.Phone.Register() внутри QualifyLoop() (блокирует навсегда), поэтому для MVP делаем
		// один REGISTER (200 OK) и потом переходим к Answer().
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

			for {
				if ctx.Err() != nil {
					return
				}
				regAttemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := regTx.Register(regAttemptCtx)
				cancel()
				if err == nil {
					logger.Info("sip registered", "domain", sipDomain, "port", sipPort, "user", bucisUser)
					break
				}
				logger.Warn("sip register failed", "err", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
				}
			}
			if err := regClient.Close(); err != nil {
				logger.Warn("sip register: client close failed", "err", err)
			}
		}

		// Освобождаем lport: теперь sipgox.Phone сможет создать серверный слушатель.
		_ = tmpConn.Close()
		tmpConnClosed = true

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
				go func() {
					defer cancel()
					cleanup, streamDone, rtpErr := rtpsession.Start(callCtx, logger, lport, raddr)
					if cleanup != nil {
						defer cleanup()
					}
					if rtpErr != nil {
						return
					}
					<-streamDone
				}()
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
				// From.User может быть произвольным (SIP_USER_BES задан вручную). Fallback: берём IP источника INVITE.
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
					if cancel != nil {
						cancel()
					}
					return
				case <-dialog.Context().Done():
					if cancel != nil {
						cancel()
					}
				}
				continue
			}

			msg := protocol.FormatClientConversation(sipID)
			if err := sendTo(besIP, cfg.EC.ListenPort8890, msg); err != nil {
				logger.Warn("ec_client_conversation send failed", "err", err, "dst", besIP, "sip_id", sipID)
				continue
			}
			logger.Info("ec_client_conversation sent", "dst", besIP, "sip_id", sipID)

			// Не принимаем следующий INVITE, пока не завершён текущий диалог (иначе копятся RTP-горутины).
			select {
			case <-ctx.Done():
				if cancel != nil {
					cancel()
				}
				return
			case <-dialog.Context().Done():
				if cancel != nil {
					cancel()
				}
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
