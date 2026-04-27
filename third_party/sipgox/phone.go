package sipgox

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/media"
	"github.com/emiago/media/sdp"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Phone is easy wrapper for creating phone like functionaliy
// but actions are creating clients and servers on a fly so
// it is not designed for long running apps

var (
	// Value must be zerolog.Logger
	ContextLoggerKey = "logger"
)

type Phone struct {
	UA *sipgo.UserAgent
	// listenAddrs is map of transport:addr which will phone use to listen incoming requests
	listenAddrs []ListenAddr

	log zerolog.Logger

	// Custom client or server
	// By default they are created
	client *sipgo.Client
	server *sipgo.Server
}

type ListenAddr struct {
	Network string
	Addr    string
	TLSConf *tls.Config
}

type Listener struct {
	ListenAddr
	io.Closer
	Listen func() error
}

type PhoneOption func(p *Phone)

// WithPhoneListenAddrs
// NOT TLS supported
func WithPhoneListenAddr(addr ListenAddr) PhoneOption {
	return func(p *Phone) {
		p.listenAddrs = append(p.listenAddrs, addr)
	}
}

func WithPhoneLogger(l zerolog.Logger) PhoneOption {
	return func(p *Phone) {
		p.log = l
	}
}

func NewPhone(ua *sipgo.UserAgent, options ...PhoneOption) *Phone {
	p := &Phone{
		UA: ua,
		// c:           client,
		listenAddrs: []ListenAddr{},
		log:         log.Logger,
	}

	for _, o := range options {
		o(p)
	}

	// In case ws we want to run http
	return p
}

func (p *Phone) Close() {
}

func (p *Phone) getLoggerCtx(ctx context.Context, caller string) zerolog.Logger {
	l := ctx.Value(ContextLoggerKey)
	if l != nil {
		log, ok := l.(zerolog.Logger)
		if ok {
			return log
		}
	}
	return p.log.With().Str("caller", caller).Logger()
}

func (p *Phone) getInterfaceAddr(network string, targetAddr string) (addr string, err error) {
	host, port, err := p.getInterfaceHostPort(network, targetAddr)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func (p *Phone) createServerListener(s *sipgo.Server, a ListenAddr) (*Listener, error) {

	network, addr := a.Network, a.Addr
	switch network {
	case "udp":
		// resolve local UDP endpoint
		laddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return nil, fmt.Errorf("fail to resolve address. err=%w", err)
		}
		udpConn, err := net.ListenUDP("udp", laddr)
		if err != nil {
			return nil, fmt.Errorf("listen udp error. err=%w", err)
		}

		// Port can be dynamic
		a.Addr = udpConn.LocalAddr().String()

		return &Listener{
			a,
			udpConn,
			func() error { return s.ServeUDP(udpConn) },
		}, nil

	case "ws", "tcp":
		laddr, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("fail to resolve address. err=%w", err)
		}

		conn, err := net.ListenTCP("tcp", laddr)
		if err != nil {
			return nil, fmt.Errorf("listen tcp error. err=%w", err)
		}

		a.Addr = conn.Addr().String()
		// and uses listener to buffer
		if network == "ws" {
			return &Listener{
				a,
				conn,
				func() error { return s.ServeWS(conn) },
			}, nil
		}

		return &Listener{
			a,
			conn,
			func() error { return s.ServeTCP(conn) },
		}, nil
	}
	return nil, fmt.Errorf("unsuported protocol")
}

func (p *Phone) createServerListeners(s *sipgo.Server) (listeners []*Listener, e error) {
	newListener := func(a ListenAddr) error {
		l, err := p.createServerListener(s, a)
		if err != nil {
			return err
		}

		listeners = append(listeners, l)
		return nil
	}

	if len(p.listenAddrs) == 0 {
		addr, err := p.getInterfaceAddr("udp", "")
		if err != nil {
			return listeners, err
		}
		err = newListener(ListenAddr{Network: "udp", Addr: addr})
		return listeners, err
	}

	for _, a := range p.listenAddrs {
		err := newListener(a)
		if err != nil {
			return nil, err
		}
	}
	return listeners, nil
}

func (p *Phone) getInterfaceHostPort(network string, targetAddr string) (host string, port int, err error) {
	for _, a := range p.listenAddrs {
		if a.Network == network {
			host, port, err = sip.ParseAddr(a.Addr)
			if err != nil {
				return
			}

			if port != 0 {
				return
			}

			ip := net.ParseIP(host)
			if ip != nil {
				port, err = findFreePort(network, ip)
				return
			}
			return
		}
	}

	ip, port, err := FindFreeInterfaceHostPort(network, targetAddr)
	if err != nil {
		return "", 0, err
	}
	return ip.String(), port, nil
}

var (
	ErrRegisterFail        = fmt.Errorf("register failed")
	ErrRegisterUnathorized = fmt.Errorf("register unathorized")
)

type RegisterResponseError struct {
	RegisterReq *sip.Request
	RegisterRes *sip.Response

	Msg string
}

func (e *RegisterResponseError) StatusCode() int {
	return e.RegisterRes.StatusCode
}

func (e RegisterResponseError) Error() string {
	return e.Msg
}

// Register the phone by sip uri. Pass username and password via opts
// NOTE: this will block and keep periodic registration. Use context to cancel
type RegisterOptions struct {
	Username string
	Password string

	Expiry        int
	AllowHeaders  []string
	UnregisterAll bool
}

func (p *Phone) Register(ctx context.Context, recipient sip.Uri, opts RegisterOptions) error {
	log := p.getLoggerCtx(ctx, "Register")
	network := recipient.Headers.GetOr("transport", "udp")
	lhost, lport, _ := p.getInterfaceHostPort(network, recipient.HostPort())

	server, err := sipgo.NewServer(p.UA)
	if err != nil {
		return err
	}
	defer server.Close()

	server.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) {
		res := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil)
		if err := tx.Respond(res); err != nil {
			log.Error().Err(err).Msg("OPTIONS 200 failed to respond")
		}
	})

	client, err := sipgo.NewClient(p.UA,
		sipgo.WithClientHostname(lhost),
		sipgo.WithClientPort(lport),
		sipgo.WithClientNAT(), // add rport support
	)
	defer client.Close()

	contactHdr := sip.ContactHeader{
		Address: sip.Uri{
			User:      p.UA.Name(),
			Host:      lhost,
			Port:      lport,
			Headers:   sip.HeaderParams{{K: "transport", V: network}},
			UriParams: sip.NewParams(),
		},
		Params: sip.NewParams(),
	}

	t, err := p.register(ctx, client, recipient, contactHdr, opts)
	if err != nil {
		return err
	}

	defer func() {
		ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
		err := t.Unregister(ctx)
		if err != nil {
			log.Error().Err(err).Msg("Fail to unregister")
		}
	}()

	return t.QualifyLoop(ctx)
}

func (p *Phone) register(ctx context.Context, client *sipgo.Client, recipient sip.Uri, contact sip.ContactHeader, opts RegisterOptions) (*RegisterTransaction, error) {
	t := NewRegisterTransaction(p.getLoggerCtx(ctx, "Register"), client, recipient, contact, opts)

	if opts.UnregisterAll {
		if err := t.Unregister(ctx); err != nil {
			return nil, ErrRegisterFail
		}
	}

	err := t.Register(ctx)
	if err != nil {
		return nil, err
	}

	return t, nil
}

type DialResponseError struct {
	InviteReq  *sip.Request
	InviteResp *sip.Response

	Msg string
}

func (e *DialResponseError) StatusCode() int {
	return e.InviteResp.StatusCode
}

func (e DialResponseError) Error() string {
	return e.Msg
}

type DialOptions struct {
	// Authentication via digest challenge
	Username string
	Password string

	// Custom headers passed on INVITE
	SipHeaders []sip.Header

	// SDP Formats to customize. NOTE: Only ulaw and alaw are fully supported
	Formats sdp.Formats

	// OnResponse is just callback called after INVITE is sent and all responses before final one
	// Useful for tracking call state
	OnResponse func(inviteResp *sip.Response)

	// OnRefer is called 2 times.
	// 1st with state NONE and dialog=nil. This is to have caller prepared
	// 2nd with state Established or Ended with dialog
	OnRefer func(state DialogReferState)

	// Experimental
	//
	// OnMedia handles INVITE updates and passes new MediaSession with new propertie
	OnMedia func(sess *media.MediaSession)
}

type DialogReferState struct {
	// Updates current transfer progress with state
	State sip.DialogState
	// Dialog present when state is answered (Confirmed dialog state)
	Dialog *DialogClientSession
}

// Dial creates dialog with recipient
//
// return DialResponseError in case non 200 responses
func (p *Phone) Dial(dialCtx context.Context, recipient sip.Uri, o DialOptions) (*DialogClientSession, error) {
	log := p.getLoggerCtx(dialCtx, "Dial")
	ctx, _ := context.WithCancel(dialCtx)

	network := "udp"
	if recipient.UriParams != nil {
		if t, ok := recipient.UriParams.Get("transport"); ok && t != "" {
			network = t
		}
	}
	recipient.Password = ""

	server, err := sipgo.NewServer(p.UA)
	if err != nil {
		return nil, err
	}

	host, port, err := p.getInterfaceHostPort(network, recipient.HostPort())
	if err != nil {
		return nil, err
	}

	contactHDR := sip.ContactHeader{
		Address: sip.Uri{User: p.UA.Name(), Host: host, Port: port},
		Params:  sip.HeaderParams{{K: "transport", V: network}},
	}

	client, err := sipgo.NewClient(p.UA,
		sipgo.WithClientHostname(host),
		sipgo.WithClientPort(port),
		sipgo.WithClientNAT(),
	)
	if err != nil {
		return nil, err
	}

	dc := sipgo.NewDialogClientCache(client, contactHDR)

	server.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
		if err := dc.ReadBye(req, tx); err != nil {
			if errors.Is(err, sipgo.ErrDialogDoesNotExists) {
				log.Info().Msg("Received BYE but dialog was already closed")
				return
			}

			log.Error().Err(err).Msg("Dialog reading BYE failed")
			return
		}
		log.Debug().Msg("Received BYE")
	})

	server.OnRefer(func(req *sip.Request, tx sip.ServerTransaction) {
		if o.OnRefer == nil {
			log.Warn().Str("req", req.StartLine()).Msg("Refer is not handled. Missing OnRefer")
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusMethodNotAllowed, "Method not allowed", nil))
			return
		}

		var dialog *sipgo.DialogClientSession
		var newDialog *DialogClientSession
		var err error

		parseRefer := func(req *sip.Request, tx sip.ServerTransaction, referUri *sip.Uri) (*sipgo.DialogClientSession, error) {
			dt, err := dc.MatchRequestDialog(req)
			if err != nil {
				return nil, err
			}

			cseq := req.CSeq().SeqNo
			if cseq <= dt.CSEQ() {
				return nil, sipgo.ErrDialogOutsideDialog
			}

			referToHdr := req.GetHeader("Refer-to")
			if referToHdr == nil {
				return nil, fmt.Errorf("no Refer-to header present")
			}

			if err := sip.ParseUri(referToHdr.Value(), referUri); err != nil {
				return nil, err
			}

			res := sip.NewResponseFromRequest(req, 202, "Accepted", nil)
			if err := tx.Respond(res); err != nil {
				return nil, err
			}
			return dt, nil
		}

		refer := func() error {
			referUri := sip.Uri{}
			dialog, err = parseRefer(req, tx, &referUri)
			if err != nil {
				return err
			}

			var rtpIp net.IP
			if lip := net.ParseIP(host); lip != nil && !lip.IsUnspecified() {
				rtpIp = lip
			} else {
				rtpIp, _, err = sip.ResolveInterfacesIP("ip4", nil)
				if err != nil {
					return err
				}
			}
			rtpPort := pickRTPPort(rtpIp)
			msess, err := media.NewMediaSession(&net.UDPAddr{IP: rtpIp, Port: rtpPort})
			if err != nil {
				return err
			}

			notifyAccepted := true
			{
				log.Info().Msg("Sending NOTIFY")
				notify := sip.NewRequest(sip.NOTIFY, req.Contact().Address)
				notify.AppendHeader(sip.NewHeader("Content-Type", "message/sipfrag;version=2.0"))
				notify.SetBody([]byte("SIP/2.0 100 Trying"))
				cliTx, err := dialog.TransactionRequest(dialog.Context(), notify)
				if err != nil {
					return err
				}
				defer cliTx.Terminate()

				select {
				case <-cliTx.Done():
				case res := <-cliTx.Responses():
					notifyAccepted = res.StatusCode == sip.StatusOK
				}
			}

			invite := sip.NewRequest(sip.INVITE, referUri)
			invite.SetTransport(network)
			invite.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
			invite.SetBody(msess.LocalSDP())

			newDialog, err = p.dial(context.TODO(), dc, invite, msess, o)
			if err != nil {
				return err
			}

			if notifyAccepted {
				notify := sip.NewRequest(sip.NOTIFY, req.Contact().Address)
				notify.AppendHeader(sip.NewHeader("Content-Type", "message/sipfrag;version=2.0"))
				notify.SetBody([]byte("SIP/2.0 200 OK"))
				cliTx, err := dialog.TransactionRequest(dialog.Context(), notify)
				if err != nil {
					return err
				}
				defer cliTx.Terminate()
			}
			return nil
		}

		o.OnRefer(DialogReferState{State: 0})
		if err := refer(); err != nil {
			log.Error().Err(err).Msg("Fail to dial REFER")
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusInternalServerError, err.Error(), nil))
			o.OnRefer(DialogReferState{State: sip.DialogStateEnded})
			return
		}

		o.OnRefer(DialogReferState{State: sip.DialogStateConfirmed, Dialog: newDialog})
	})

	var dialogRef *DialogClientSession

	server.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		id, err := sip.DialogIDFromRequestUAC(req)
		if err != nil {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusBadRequest, "Bad Request", nil))
			return
		}

		if dialogRef.ID != id {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusNotFound, "Dialog does not exist", nil))
			return
		}

		msess := dialogRef.MediaSession.Fork()

		if err := msess.RemoteSDP(req.Body()); err != nil {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusBadRequest, "SDP applying failed", nil))
			return
		}

		log.Info().
			Str("formats", logFormats(msess.Formats)).
			Str("localAddr", msess.Laddr.String()).
			Str("remoteAddr", msess.Raddr.String()).
			Msg("Media/RTP session updated")

		if o.OnMedia != nil {
			o.OnMedia(msess)
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil))
			return
		}
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusMethodNotAllowed, "Method not allowed", nil))
	})

	server.OnAck(func(req *sip.Request, tx sip.ServerTransaction) {
		id, err := sip.DialogIDFromRequestUAC(req)
		if err != nil {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusBadRequest, "Bad Request", nil))
			return
		}

		if dialogRef.ID != id {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusNotFound, "Dialog does not exist", nil))
			return
		}
	})

	var rtpIp net.IP
	if lip := net.ParseIP(host); lip != nil && !lip.IsUnspecified() {
		rtpIp = lip
	} else {
		rtpIp, _, err = sip.ResolveInterfacesIP("ip4", nil)
		if err != nil {
			return nil, err
		}
	}
	rtpPort := pickRTPPort(rtpIp)
	msess, err := media.NewMediaSession(&net.UDPAddr{IP: rtpIp, Port: rtpPort})
	if err != nil {
		return nil, err
	}

	// Create Generic SDP
	if len(o.Formats) > 0 {
		msess.Formats = o.Formats
	}
	sdpSend := msess.LocalSDP()

	req := sip.NewRequest(sip.INVITE, recipient)
	req.SetTransport(network)
	req.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	req.SetBody(sdpSend)

	for _, h := range o.SipHeaders {
		log.Info().Str(h.Name(), h.Value()).Msg("Adding SIP header")
		req.AppendHeader(h)
	}

	dialog, err := p.dial(ctx, dc, req, msess, o)
	if err != nil {
		return nil, err
	}

	dialogRef = dialog

	return dialog, nil
}

func (p *Phone) dial(ctx context.Context, dc *sipgo.DialogClientCache, invite *sip.Request, msess *media.MediaSession, o DialOptions) (*DialogClientSession, error) {
	log := p.getLoggerCtx(ctx, "Dial")
	dialog, err := dc.WriteInvite(ctx, invite)
	if err != nil {
		return nil, err
	}
	p.logSipRequest(&log, invite)
	return p.dialWaitAnswer(ctx, dialog, msess, o)
}

func (p *Phone) dialWaitAnswer(ctx context.Context, dialog *sipgo.DialogClientSession, msess *media.MediaSession, o DialOptions) (*DialogClientSession, error) {
	log := p.getLoggerCtx(ctx, "Dial")
	invite := dialog.InviteRequest
	waitStart := time.Now()
	err := dialog.WaitAnswer(ctx, sipgo.AnswerOptions{
		OnResponse: func(res *sip.Response) error {
			p.logSipResponse(&log, res)
			if o.OnResponse != nil {
				o.OnResponse(res)
			}
			return nil
		},
		Username: o.Username,
		Password: o.Password,
	})

	var rerr *sipgo.ErrDialogResponse
	if errors.As(err, &rerr) {
		return nil, &DialResponseError{
			InviteReq:  invite,
			InviteResp: rerr.Res,
			Msg:        fmt.Sprintf("Call not answered: %s", rerr.Res.StartLine()),
		}
	}

	if err != nil {
		return nil, err
	}

	r := dialog.InviteResponse
	log.Info().
		Int("code", int(r.StatusCode)).
		Str("duration", time.Since(waitStart).String()).
		Msg("Call answered")

	err = msess.RemoteSDP(r.Body())
	if err != nil {
		return nil, err
	}

	log.Info().
		Str("formats", logFormats(msess.Formats)).
		Str("localAddr", msess.Laddr.String()).
		Str("remoteAddr", msess.Raddr.String()).
		Msg("Media/RTP session created")

	if err := dialog.Ack(ctx); err != nil {
		return nil, fmt.Errorf("fail to send ACK: %w", err)
	}

	return &DialogClientSession{
		MediaSession:        msess,
		DialogClientSession: dialog,
	}, nil
}

var (
	AnswerReadyCtxKey = "AnswerReadyCtxKey"
)

type AnswerReadyCtxValue chan struct{}
type AnswerOptions struct {
	Ringtime   time.Duration
	SipHeaders []sip.Header

	Username string
	Password string
	Realm    string //default sipgo

	RegisterAddr string //If defined it will keep registration in background

	Formats sdp.Formats

	OnCall func(inviteRequest *sip.Request) int

	AnswerCode   int
	AnswerReason string
}

func (p *Phone) Answer(ansCtx context.Context, opts AnswerOptions) (*DialogServerSession, error) {
	dialog, err := p.answer(ansCtx, opts)
	if err != nil {
		return nil, err
	}
	log.Debug().Msg("Dialog answer created")
	if !dialog.InviteResponse.IsSuccess() {
		return dialog, dialog.Close()
	}

	return dialog, nil
}

func (p *Phone) answer(ansCtx context.Context, opts AnswerOptions) (*DialogServerSession, error) {
	log := p.getLoggerCtx(ansCtx, "Answer")
	ringtime := opts.Ringtime

	waitDialog := make(chan *DialogServerSession)
	var d *DialogServerSession

	server, err := sipgo.NewServer(p.UA)
	if err != nil {
		return nil, err
	}

	listeners, err := p.createServerListeners(server)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ansCtx)
	var exitErr error
	stopAnswer := sync.OnceFunc(func() {
		cancel()
		for _, l := range listeners {
			log.Debug().Str("addr", l.Addr).Msg("Closing listener")
			l.Close()
		}
	})

	exitError := func(err error) {
		exitErr = err
	}

	lhost, lport, _ := sip.ParseAddr(listeners[0].Addr)
	contactHdr := sip.ContactHeader{
		Address: sip.Uri{
			User:      p.UA.Name(),
			Host:      lhost,
			Port:      lport,
			Headers:   sip.HeaderParams{{K: "transport", V: listeners[0].Network}},
			UriParams: sip.NewParams(),
		},
		Params: sip.NewParams(),
	}

	client, err := sipgo.NewClient(p.UA,
		sipgo.WithClientNAT(),
		sipgo.WithClientHostname(lhost),
	)
	if err != nil {
		return nil, err
	}

	if opts.RegisterAddr != "" {
		rhost, rport, _ := sip.ParseAddr(opts.RegisterAddr)
		registerURI := sip.Uri{
			Host: rhost,
			Port: rport,
			User: p.UA.Name(),
		}

		regTr, err := p.register(ctx, client, registerURI, contactHdr, RegisterOptions{
			Username: opts.Username,
			Password: opts.Password,
			Expiry:   30,
		})
		if err != nil {
			return nil, err
		}

		contact := regTr.Origin.Contact()
		contactHdr = *contact.Clone()

		origStopAnswer := stopAnswer
		stopAnswer = sync.OnceFunc(func() {
			ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
			err := regTr.Unregister(ctx)
			if err != nil {
				log.Error().Err(err).Msg("Fail to unregister")
			}
			regTr = nil
			origStopAnswer()
		})
		go func(ctx context.Context) {
			err := regTr.QualifyLoop(ctx)
			exitError(err)
			stopAnswer()
		}(ctx)
	}

	ds := sipgo.NewDialogServerCache(client, contactHdr)
	var chal *digest.Challenge
	server.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		if d != nil {
			if d.InviteRequest != nil && d.InviteResponse != nil && req.CallID() != nil && req.CSeq() != nil &&
				d.InviteRequest.CallID() != nil && d.InviteRequest.CSeq() != nil &&
				req.CallID().Value() == d.InviteRequest.CallID().Value() &&
				req.CSeq().SeqNo == d.InviteRequest.CSeq().SeqNo {
				if err := tx.Respond(d.InviteResponse); err != nil {
					log.Error().Err(err).Msg("Fail to send retransmit response")
				}
				return
			}

			didAnswered, _ := sip.DialogIDFromResponse(d.InviteResponse)
			did, _ := sip.DialogIDFromRequestUAS(req)
			if did == didAnswered {

				if err := d.MediaSession.RemoteSDP(req.Body()); err != nil {
					res := sip.NewResponseFromRequest(req, 400, err.Error(), nil)
					if err := tx.Respond(res); err != nil {
						log.Error().Err(err).Msg("Fail to send 400")
						return
					}
					return
				}

				res := sip.NewResponseFromRequest(req, 200, "OK", nil)
				if err := tx.Respond(res); err != nil {
					log.Error().Err(err).Msg("Fail to send 200")
					return
				}
				return
			}
			log.Error().Msg("Received second INVITE is not yet supported")
			return
		}

		if opts.Password != "" && opts.RegisterAddr == "" {
			h := req.GetHeader("Authorization")

			if h == nil {
				if chal != nil {
					res := sip.NewResponseFromRequest(req, 403, "Forbidden", nil)
					tx.Respond(res)
					return
				}

				if opts.Realm == "" {
					opts.Realm = "sipgo"
				}

				chal = &digest.Challenge{
					Realm:     opts.Realm,
					Nonce:     fmt.Sprintf("%d", time.Now().UnixMicro()),
					Algorithm: "MD5",
				}

				res := sip.NewResponseFromRequest(req, 401, "Unathorized", nil)
				res.AppendHeader(sip.NewHeader("WWW-Authenticate", chal.String()))
				tx.Respond(res)
				return
			}

			cred, err := digest.ParseCredentials(h.Value())
			if err != nil {
				log.Error().Err(err).Msg("parsing creds failed")
				tx.Respond(sip.NewResponseFromRequest(req, 401, "Bad credentials", nil))
				return
			}

			digCred, err := digest.Digest(chal, digest.Options{
				Method:   "INVITE",
				URI:      cred.URI,
				Username: opts.Username,
				Password: opts.Password,
			})

			if err != nil {
				log.Error().Err(err).Msg("Calc digest failed")
				tx.Respond(sip.NewResponseFromRequest(req, 401, "Bad credentials", nil))
				return
			}

			if cred.Response != digCred.Response {
				tx.Respond(sip.NewResponseFromRequest(req, 401, "Unathorized", nil))
				return
			}
			log.Info().Str("username", cred.Username).Str("source", req.Source()).Msg("INVITE authorized")
		}
		p.logSipRequest(&log, req)

		dialog, err := ds.ReadInvite(req, tx)
		if err != nil {
			res := sip.NewResponseFromRequest(req, 400, err.Error(), nil)
			if err := tx.Respond(res); err != nil {
				log.Error().Err(err).Msg("Failed to send 400 response")
			}

			exitError(err)
			stopAnswer()
			return
		}

		err = func() error {
			if opts.OnCall != nil {
				res := opts.OnCall(req)
				switch {
				case res < 0:
					if err := dialog.Respond(sip.StatusBusyHere, "Busy", nil); err != nil {
						d = nil
						return fmt.Errorf("failed to respond oncall status code %d: %w", res, err)
					}
				case res > 0:
					if err := dialog.Respond(res, "", nil); err != nil {
						d = nil
						return fmt.Errorf("failed to respond oncall status code %d: %w", res, err)
					}
				}
			}

			if opts.AnswerCode > 0 && opts.AnswerCode != sip.StatusOK {
				log.Info().Int("code", int(opts.AnswerCode)).Msg("Answering call")
				if opts.AnswerReason == "" {
					switch opts.AnswerCode {
					case sip.StatusBusyHere:
						opts.AnswerReason = "Busy"
					case sip.StatusForbidden:
						opts.AnswerReason = "Forbidden"
					case sip.StatusUnauthorized:
						opts.AnswerReason = "Unathorized"
					}
				}

				if err := dialog.Respond(opts.AnswerCode, opts.AnswerReason, nil); err != nil {
					d = nil
					return fmt.Errorf("failed to respond custom status code %d: %w", int(opts.AnswerCode), err)
				}
				p.logSipResponse(&log, dialog.InviteResponse)

				d = &DialogServerSession{
					DialogServerSession: dialog,
				}
				select {
				case <-tx.Done():
					return tx.Err()
				case <-tx.Acks():
					log.Debug().Msg("ACK received. Returning dialog")
				case <-ctx.Done():
					return ctx.Err()
				}

				select {
				case waitDialog <- d:
				case <-ctx.Done():
					return ctx.Err()
				}
				return nil
			}

			if ringtime > 0 {
				res := sip.NewResponseFromRequest(req, 180, "Ringing", nil)
				if err := dialog.WriteResponse(res); err != nil {
					return fmt.Errorf("failed to send 180 response: %w", err)
				}
				p.logSipResponse(&log, res)

				select {
				case <-tx.Done():
					return fmt.Errorf("invite transaction finished while ringing: %w", tx.Err())
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(ringtime):
				}
			} else {
				res := sip.NewResponseFromRequest(req, 100, "Trying", nil)
				if err := dialog.WriteResponse(res); err != nil {
					return fmt.Errorf("failed to send 100 response: %w", err)
				}

				p.logSipResponse(&log, res)
			}

			contentType := req.ContentType()
			if contentType == nil || contentType.Value() != "application/sdp" {
				return fmt.Errorf("no SDP in INVITE provided")
			}

			var ip net.IP
			if lip := net.ParseIP(lhost); lip != nil && !lip.IsUnspecified() {
				ip = lip
			} else {
				ip, _, err = sip.ResolveInterfacesIP("ip4", nil)
				if err != nil {
					return err
				}
			}

			rtpPort := pickRTPPort(ip)
			msess, err := media.NewMediaSession(&net.UDPAddr{IP: ip, Port: rtpPort})
			if err != nil {
				return err
			}
			if len(opts.Formats) > 0 {
				msess.Formats = opts.Formats
			}

			err = msess.RemoteSDP(req.Body())
			if err != nil {
				return err
			}

			log.Info().
				Str("formats", logFormats(msess.Formats)).
				Str("localAddr", msess.Laddr.String()).
				Str("remoteAddr", msess.Raddr.String()).
				Msg("Media/RTP session created")

			res := sip.NewSDPResponseFromRequest(dialog.InviteRequest, msess.LocalSDP())

			for _, h := range opts.SipHeaders {
				log.Info().Str(h.Name(), h.Value()).Msg("Adding SIP header")
				res.AppendHeader(h)
			}

			d = &DialogServerSession{
				DialogServerSession: dialog,
				MediaSession:        msess,
			}
			dialog.InviteResponse = res

			log.Info().Msg("Answering call")
			if err := tx.Respond(res); err != nil {
				d = nil
				return fmt.Errorf("fail to send 200 response: %w", err)
			}
			p.logSipResponse(&log, res)
			return nil
		}()

		if err != nil {
			dialog.Close()
			exitError(err)
			stopAnswer()
		}

	})

	server.OnAck(func(req *sip.Request, tx sip.ServerTransaction) {
		if d == nil {
			if chal != nil {
				return
			}

			exitError(fmt.Errorf("received ack but no dialog"))
			stopAnswer()
		}

		if err := ds.ReadAck(req, tx); err != nil {
			exitError(fmt.Errorf("dialog ACK err: %w", err))
			stopAnswer()
			return
		}

		select {
		case waitDialog <- d:
			d = nil
		case <-ctx.Done():
		}
	})

	server.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
		if err := ds.ReadBye(req, tx); err != nil {
			exitError(fmt.Errorf("dialog BYE err: %w", err))
			return
		}

		stopAnswer()
	})

	server.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) {
		res := sip.NewResponseFromRequest(req, 200, "OK", nil)
		tx.Respond(res)
	})

	for _, l := range listeners {
		log.Info().Str("network", l.Network).Str("addr", l.Addr).Msg("Listening on")
		go l.Listen()
	}

	if v := ctx.Value(AnswerReadyCtxKey); v != nil {
		close(v.(AnswerReadyCtxValue))
	}

	log.Info().Msg("Waiting for INVITE...")
	select {
	case d = <-waitDialog:
		d.onClose = stopAnswer
		return d, nil
	case <-ctx.Done():
		if ansCtx.Err() != nil {
			stopAnswer()
			return nil, ansCtx.Err()
		}

		return nil, exitErr
	}
}

func (p *Phone) logSipRequest(log *zerolog.Logger, req *sip.Request) {
	log.Info().
		Str("request", req.StartLine()).
		Str("call_id", req.CallID().Value()).
		Str("from", req.From().Value()).
		Str("event", "SIP").
		Msg("Request")
}

func (p *Phone) logSipResponse(log *zerolog.Logger, res *sip.Response) {
	log.Info().
		Str("response", res.StartLine()).
		Str("call_id", res.CallID().Value()).
		Str("event", "SIP").
		Msg("Response")
}

func getResponse(ctx context.Context, tx sip.ClientTransaction) (*sip.Response, error) {
	select {
	case <-tx.Done():
		return nil, fmt.Errorf("transaction died")
	case res := <-tx.Responses():
		return res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func logFormats(f sdp.Formats) string {
	out := make([]string, len(f))
	for i, v := range f {
		switch v {
		case "0":
			out[i] = "0(ulaw)"
		case "8":
			out[i] = "8(alaw)"
		default:
			out[i] = v
		}
	}
	return strings.Join(out, ",")
}
