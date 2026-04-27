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
	"bucis-bes_simulator/internal/media/receiver"
	"bucis-bes_simulator/internal/media/rtp"
	"bucis-bes_simulator/internal/media/sender"
	"bucis-bes_simulator/pkg/log"

	"github.com/emiago/media/sdp"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgox"
	"github.com/rs/zerolog"
)

func main() {
	var (
		besAddrOverride string
		besBroadcast    string
		opensipsIP      string
		resetInterval   time.Duration
		keepAliveInt    time.Duration

		head1 string
		head2 string
	)

	flag.StringVar(&besBroadcast, "ec-bes-broadcast-addr", "", "EC destination broadcast address for reset/keepalive (default from EC_BES_BROADCAST_ADDR)")
	flag.StringVar(&besAddrOverride, "ec-bes-addr", "", "EC destination override for reset/keepalive (unicast) (default from EC_BES_ADDR)")
	flag.StringVar(&opensipsIP, "ec-opensips-ip", "", "OpenSIPS IP to put into keepalive (default to local IP)")
	flag.DurationVar(&resetInterval, "ec-reset-interval", 0, "ClientReset interval (default from EC_CLIENT_RESET_INTERVAL)")
	flag.DurationVar(&keepAliveInt, "ec-keepalive-interval", 0, "KeepAlive interval (default from EC_KEEPALIVE_INTERVAL)")
	flag.StringVar(&head1, "ec-head1-ip", "", "ClientReset head1 IP (default: local IP)")
	flag.StringVar(&head2, "ec-head2-ip", "", "ClientReset head2 IP (default: head1)")
	flag.Parse()
	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bucis")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.ParseBucis()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "ec-bes-broadcast-addr":
			cfg.EC.BesBroadcastAddr = besBroadcast
		case "ec-bes-addr":
			cfg.EC.BesAddrOverride = besAddrOverride
		case "ec-reset-interval":
			cfg.EC.ClientResetInterval = resetInterval
		case "ec-keepalive-interval":
			cfg.EC.KeepAliveInterval = keepAliveInt
		}
	})
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

	type sipIDState struct {
		mu  sync.Mutex
		seq int
		m   map[string]string // mac -> sipId
		ip  map[string]string // sipId -> bes ip
	}
	sipIDs := sipIDState{m: make(map[string]string), ip: make(map[string]string)}
	getSipID := func(mac string) string {
		sipIDs.mu.Lock()
		defer sipIDs.mu.Unlock()
		if v, ok := sipIDs.m[mac]; ok {
			return v
		}
		sipIDs.seq++
		v := strconv.Itoa(sipIDs.seq)
		sipIDs.m[mac] = v
		return v
	}
	setSipIDIP := func(sipID string, besIP string) {
		sipIDs.mu.Lock()
		defer sipIDs.mu.Unlock()
		if sipID == "" || besIP == "" {
			return
		}
		sipIDs.ip[sipID] = besIP
	}
	getSipIDIP := func(sipID string) (string, bool) {
		sipIDs.mu.Lock()
		defer sipIDs.mu.Unlock()
		v, ok := sipIDs.ip[sipID]
		return v, ok
	}

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
		_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		_, err = conn.Write([]byte(payload))
		return err
	}

	sendReset := func() {
		dst := cfg.EC.BesBroadcastAddr
		if cfg.EC.BesAddrOverride != "" {
			dst = cfg.EC.BesAddrOverride
		}
		msg := protocol.FormatClientReset(head1, head2)
		if err := sendTo(dst, cfg.EC.ListenPort8890, msg); err != nil {
			logger.Warn("ec_client_reset send failed", "err", err, "dst", dst)
			return
		}
		logger.Info("ec_client_reset sent", "dst", dst, "head1", head1, "head2", head2)
	}

	sendKeepAlive := func() {
		dst := cfg.EC.BesBroadcastAddr
		if cfg.EC.BesAddrOverride != "" {
			dst = cfg.EC.BesAddrOverride
		}
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
	{
		bucisUser := strings.TrimSpace(os.Getenv("SIP_USER_BUCIS"))
		bucisPass := os.Getenv("SIP_PASS_BUCIS")
		if bucisUser != "" {
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
			} else {
				remote := sip.Uri{Host: sipDomain, Port: sipPort}
				network := remote.Headers.GetOr("transport", "udp")
				if network != "udp" {
					logger.Warn("unsupported sip transport for answer", "network", network)
					return
				}
				lhost := localIP
				if ip := net.ParseIP(sipDomain); ip != nil && ip.IsLoopback() {
					lhost = "127.0.0.1"
				}
				laddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(lhost, "0"))
				if err != nil {
					logger.Warn("sip register: failed to resolve local addr", "err", err)
					return
				}
				tmpConn, err := net.ListenUDP("udp4", laddr)
				if err != nil {
					logger.Warn("sip register: failed to reserve local port", "err", err)
					return
				}
				lport := tmpConn.LocalAddr().(*net.UDPAddr).Port
				_ = tmpConn.Close()

				phone := sipgox.NewPhone(ua, sipgox.WithPhoneListenAddr(sipgox.ListenAddr{
					Network: network,
					Addr:    net.JoinHostPort(lhost, strconv.Itoa(lport)),
				}))
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer phone.Close()
					defer func() { _ = ua.Close() }()

					// План: "зарегистрироваться до ожидания INVITE".
					// В sipgox.Phone.Register() внутри QualifyLoop() (блокирует навсегда), поэтому для MVP делаем
					// один REGISTER (200 OK) и потом переходим к Answer().
					{
						regClient, err := sipgo.NewClient(ua,
							sipgo.WithClientHostname(lhost),
							sipgo.WithClientPort(lport),
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

					for {
						if ctx.Err() != nil {
							return
						}
						dialog, err := phone.Answer(ctx, sipgox.AnswerOptions{
							Formats:  sdp.NewFormats(strconv.Itoa(int(rtp.PayloadTypeG726()))),
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

						if dialog != nil && dialog.MediaSession != nil && dialog.Laddr != nil && dialog.Raddr != nil {
							lport := dialog.Laddr.Port
							raddr := dialog.Raddr
							logger.Info("rtp negotiated",
								"local_port", lport,
								"remote_ip", raddr.IP.String(),
								"remote_port", raddr.Port,
								"pt_g726", int(rtp.PayloadTypeG726()),
								"rtp_port_range", strings.TrimSpace(os.Getenv("RTP_PORT_RANGE")),
							)
							dialog.MediaSession.Close()

							callCtx, cancel := context.WithCancel(dialog.Context())
							go func() {
								defer cancel()
								rx := receiver.New(lport)
								if audio.Enabled() {
									if pb, err := audio.StartPlayback(callCtx, audio.Device()); err == nil {
										rx.SetPCMSink(func(pcm []int16) { _ = pb.WritePCM(pcm) })
										defer func() { _ = pb.Close() }()
									}
								}
								_ = rx.Start()
								defer func() { _, _ = rx.Stop() }()

								tx, err := sender.NewTo(raddr)
								if err != nil {
									return
								}
								defer func() { _ = tx.Close() }()

								var frames <-chan []int16
								if audio.Enabled() {
									ch, err := audio.StartCaptureFrames(callCtx, audio.Device())
									if err == nil {
										frames = ch
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
											case <-callCtx.Done():
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
								_ = tx.StreamFramesAt(callCtx, time.Now().UnixMilli(), frames)
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

						if sipID == "" {
							logger.Warn("ec_client_conversation skipped: cannot infer sip_id from SIP From header")
							continue
						}

						besIP, ok := getSipIDIP(sipID)
						if !ok {
							logger.Warn("ec_client_conversation skipped: unknown bes ip for sip_id", "sip_id", sipID)
							continue
						}

						msg := protocol.FormatClientConversation(sipID)
						if err := sendTo(besIP, cfg.EC.ListenPort8890, msg); err != nil {
							logger.Warn("ec_client_conversation send failed", "err", err, "dst", besIP, "sip_id", sipID)
							continue
						}
						logger.Info("ec_client_conversation sent", "dst", besIP, "sip_id", sipID)
					}
				}()
			}
		} else {
			logger.Info("sip disabled: SIP_USER_BUCIS not set")
		}
	}

	listenQuery := func(port int) (*net.UDPConn, error) {
		return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	}
	c6710, err := listenQuery(cfg.EC.QueryPort6710)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = c6710.Close() }()
	c7777, err := listenQuery(cfg.EC.QueryPort7777)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
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
			sipID := getSipID(query.MAC)

			dst := raddr.IP.String()
			if cfg.EC.BesAddrOverride != "" {
				dst = cfg.EC.BesAddrOverride
			}
			setSipIDIP(sipID, dst)
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
